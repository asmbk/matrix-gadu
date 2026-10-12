package gadu

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Modern GG clients do not use the libgadu image protocol. They upload files
// to GG Drive (gg.pl/dysk) and send the link as text. GIFs come as Giphy links.

// MediaKind tells where a media link points.
type MediaKind int

const (
	MediaGGDrive MediaKind = iota + 1
	MediaGiphy
	// MediaWeb is any other http(s) link. It becomes media only if the address returns an image.
	MediaWeb
)

// MediaLink is a link to a file in a GG message.
type MediaLink struct {
	URL  string
	Kind MediaKind
	// GG Drive only.
	ID       string
	FileName string
}

// RemoteMedia is a downloaded file.
type RemoteMedia struct {
	Data     []byte
	FileName string
	MimeType string
}

// ErrNotDownloadable means that the link is valid, but the file cannot be
// downloaded without a browser (GG Drive files that are not images).
var ErrNotDownloadable = errors.New("gadu: the file can only be opened in a browser")

// MaxMediaSize limits downloads from GG Drive and Giphy.
const MaxMediaSize = 25 << 20

// The base addresses are variables so that tests can use a local server.
var (
	GGDrivePageBase  = "https://www.gg.pl/dysk/"
	GGDriveThumbBase = "https://p.gg.pl/thumb/p/d/"
)

var (
	ggDriveLinkRe = regexp.MustCompile(`https://www\.gg\.pl/dysk/([A-Za-z0-9_-]{8,64})/([^\s<>"']+)`)
	giphyLinkRe   = regexp.MustCompile(`https://(?:media\d*|i)\.giphy\.com/[^\s<>"']+\.(?:gif|webp|mp4)`)
	webLinkRe     = regexp.MustCompile(`https?://[^\s<>"']+`)
	metaRe        = regexp.MustCompile(`<meta\s+property="([a-z_:]+)"\s+content="([^"]*)"`)
)

// FindMediaLinks returns the GG Drive and Giphy links in a message, in order.
// With web true, it also returns all other http(s) links as MediaWeb.
func FindMediaLinks(text string, web bool) []MediaLink {
	type found struct {
		pos  int
		link MediaLink
	}
	var all []found
	for _, m := range ggDriveLinkRe.FindAllStringSubmatchIndex(text, -1) {
		all = append(all, found{m[0], MediaLink{
			URL:      text[m[0]:m[1]],
			Kind:     MediaGGDrive,
			ID:       text[m[2]:m[3]],
			FileName: text[m[4]:m[5]],
		}})
	}
	for _, m := range giphyLinkRe.FindAllStringIndex(text, -1) {
		all = append(all, found{m[0], MediaLink{URL: text[m[0]:m[1]], Kind: MediaGiphy}})
	}
	if web {
		taken := func(pos int) bool {
			for _, f := range all {
				if f.pos == pos {
					return true
				}
			}
			return false
		}
		for _, m := range webLinkRe.FindAllStringIndex(text, -1) {
			u := strings.TrimRight(text[m[0]:m[1]], ".,;:!?)]}")
			if !taken(m[0]) {
				all = append(all, found{m[0], MediaLink{URL: u, Kind: MediaWeb}})
			}
		}
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].pos < all[j-1].pos; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	links := make([]MediaLink, len(all))
	for i, f := range all {
		links[i] = f.link
	}
	return links
}

// FetchMedia downloads the file behind a media link.
func FetchMedia(ctx context.Context, client *http.Client, link MediaLink) (*RemoteMedia, error) {
	switch link.Kind {
	case MediaGGDrive:
		return fetchGGDrive(ctx, client, link)
	case MediaGiphy:
		name := path.Base(link.URL)
		return download(ctx, client, link.URL, name, true)
	case MediaWeb:
		name := "image"
		if u, err := url.Parse(link.URL); err == nil && path.Base(u.Path) != "/" && path.Base(u.Path) != "." {
			name = path.Base(u.Path)
		}
		return download(ctx, client, link.URL, name, false)
	}
	return nil, fmt.Errorf("unknown media link kind %d", link.Kind)
}

func fetchGGDrive(ctx context.Context, client *http.Client, link MediaLink) (*RemoteMedia, error) {
	page, err := get(ctx, client, GGDrivePageBase+link.ID+"/"+link.FileName, 1<<20, nil)
	if err != nil {
		return nil, err
	}
	meta := map[string]string{}
	for _, m := range metaRe.FindAllStringSubmatch(string(page.body), -1) {
		meta[m[1]] = html.UnescapeString(m[2])
	}
	if meta["og:type"] != "ggdrive:image" {
		return nil, ErrNotDownloadable
	}
	name := meta["ggdrive:filename"]
	if name == "" {
		name, _ = url.PathUnescape(link.FileName)
	}
	if size, err := strconv.Atoi(meta["ggdrive:file_size"]); err == nil && size > MaxMediaSize {
		return nil, fmt.Errorf("GG Drive file is too large (%d bytes)", size)
	}
	// The direct link needs a browser session, but the thumbnail address
	// returns the original image.
	return download(ctx, client, GGDriveThumbBase+link.ID+"/"+link.FileName+"?app_id=web-client&api_version=6", name, false)
}

type response struct {
	body        []byte
	contentType string
}

// ErrNotMedia means that the address does not return an image (or a video, where allowed).
var ErrNotMedia = errors.New("gadu: the address does not return media")

func mediaType(contentType string, allowVideo bool) (string, bool) {
	mimeType, _, _ := mime.ParseMediaType(contentType)
	return mimeType, strings.HasPrefix(mimeType, "image/") || (allowVideo && strings.HasPrefix(mimeType, "video/"))
}

// get downloads an address. If accept is not nil, it checks the Content-Type
// header first and stops before it reads the body.
func get(ctx context.Context, client *http.Client, u string, limit int64, accept func(contentType string) bool) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; matrix-gadu)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.URL.Host)
	}
	if accept != nil && !accept(resp.Header.Get("Content-Type")) {
		return nil, ErrNotMedia
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("response from %s is larger than %d bytes", req.URL.Host, limit)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response from %s is larger than %d bytes", req.URL.Host, limit)
	}
	return &response{body: body, contentType: resp.Header.Get("Content-Type")}, nil
}

func download(ctx context.Context, client *http.Client, u, name string, allowVideo bool) (*RemoteMedia, error) {
	resp, err := get(ctx, client, u, MaxMediaSize, func(ct string) bool {
		_, ok := mediaType(ct, allowVideo)
		return ok
	})
	if err != nil {
		return nil, err
	}
	mimeType, _ := mediaType(resp.contentType, allowVideo)
	return &RemoteMedia{Data: resp.body, FileName: name, MimeType: mimeType}, nil
}

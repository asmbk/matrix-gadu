package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// maxMediaPerMessage limits downloads for one GG message.
const maxMediaPerMessage = 5

// mediaHTTPClient connects only to public addresses, because the links come from other people.
var mediaHTTPClient = gadu.NewSafeHTTPClient(60 * time.Second)

func fetchGGMedia(ctx context.Context, link gadu.MediaLink) (*gadu.RemoteMedia, error) {
	return gadu.FetchMedia(ctx, mediaHTTPClient, link)
}

// convertGGText converts a GG message to Matrix. GG Drive images and Giphy
// links become Matrix media. If nothing else is left of the text, the message
// has only the media parts.
func (gc *GaduClient) convertGGText(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, plain, ggHTML string) *bridgev2.ConvertedMessage {
	log := zerolog.Ctx(ctx)
	links := gadu.FindMediaLinks(plain, gc.main.Config.ImageLinks)
	remaining := plain
	var media []*bridgev2.ConvertedMessagePart
	for _, link := range links {
		if len(media) >= maxMediaPerMessage {
			break
		}
		part, err := gc.mediaPart(ctx, portal, intent, link)
		if err != nil {
			if !errors.Is(err, gadu.ErrNotDownloadable) && !errors.Is(err, gadu.ErrNotMedia) {
				log.Debug().Err(err).Int("kind", int(link.Kind)).Msg("Failed to bridge media link, keeping the link")
			}
			continue
		}
		media = append(media, part)
		remaining = strings.Replace(remaining, link.URL, "", 1)
	}
	if len(media) == 0 {
		return textMessage(ggToMatrix(plain, ggHTML))
	}
	var parts []*bridgev2.ConvertedMessagePart
	if text := strings.TrimSpace(remaining); text != "" {
		// The formatting is lost here, because the links are cut out of the plain text.
		parts = append(parts, &bridgev2.ConvertedMessagePart{Type: event.EventMessage, Content: ggToMatrix(text, "")})
	}
	parts = append(parts, media...)
	for i, p := range parts {
		if i > 0 {
			p.ID = networkid.PartID(fmt.Sprintf("part%d", i))
		}
	}
	return &bridgev2.ConvertedMessage{Parts: parts}
}

func (gc *GaduClient) mediaPart(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, link gadu.MediaLink) (*bridgev2.ConvertedMessagePart, error) {
	m, err := gc.fetchMedia(ctx, link)
	if err != nil {
		return nil, err
	}
	content := &event.MessageEventContent{
		MsgType:  event.MsgImage,
		Body:     m.FileName,
		FileName: m.FileName,
		Info:     &event.FileInfo{MimeType: m.MimeType, Size: len(m.Data)},
	}
	if strings.HasPrefix(m.MimeType, "video/") {
		content.MsgType = event.MsgVideo
	} else if cfg, _, err := image.DecodeConfig(bytes.NewReader(m.Data)); err == nil {
		content.Info.Width, content.Info.Height = cfg.Width, cfg.Height
	}
	content.URL, content.File, err = intent.UploadMedia(ctx, portal.MXID, m.Data, m.FileName, m.MimeType)
	if err != nil {
		return nil, fmt.Errorf("upload to Matrix: %w", err)
	}
	return &bridgev2.ConvertedMessagePart{Type: event.EventMessage, Content: content}, nil
}

// publicMediaURLFromBridge returns a public link to a Matrix file, or "" if
// public media is disabled in the bridge config.
func (gc *GaduClient) publicMediaURLFromBridge(ctx context.Context, content *event.MessageEventContent) (string, error) {
	pm, ok := gc.main.br.Matrix.(bridgev2.MatrixConnectorWithPublicMedia)
	if !ok {
		return "", nil
	}
	return pm.GetPublicMediaAddressForEvent(ctx, content)
}

// mediaToGGText turns a Matrix file into GG text: the caption (if any) and a
// public link, because GG clients only show files as links.
func (gc *GaduClient) mediaToGGText(ctx context.Context, content *event.MessageEventContent) (*event.MessageEventContent, error) {
	link, err := gc.publicMediaURL(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("make public media link: %w", err)
	}
	if link == "" {
		return nil, fmt.Errorf("%w: enable public_media in the bridge config to send files", bridgev2.ErrUnsupportedMessageType)
	}
	text := link
	if content.FileName != "" && content.Body != "" && content.Body != content.FileName {
		text = content.Body + "\n" + link
	}
	return &event.MessageEventContent{MsgType: event.MsgText, Body: text}, nil
}

var fileCaps = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{"*/*": event.CapLevelPartialSupport},
	Caption:   event.CapLevelPartialSupport,
	MaxSize:   100 << 20,
}

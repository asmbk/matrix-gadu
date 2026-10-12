package gadu

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFindMediaLinks(t *testing.T) {
	text := "zobacz https://media4.giphy.com/media/v1.abc/ounTiW/giphy.gif i https://www.gg.pl/dysk/AbCdEfGh12345678IjKlMnO/photo.jpg oraz https://evil.example/x.gif"
	got := FindMediaLinks(text, false)
	want := []MediaLink{
		{URL: "https://media4.giphy.com/media/v1.abc/ounTiW/giphy.gif", Kind: MediaGiphy},
		{URL: "https://www.gg.pl/dysk/AbCdEfGh12345678IjKlMnO/photo.jpg", Kind: MediaGGDrive, ID: "AbCdEfGh12345678IjKlMnO", FileName: "photo.jpg"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if links := FindMediaLinks("no links here", true); len(links) != 0 {
		t.Errorf("found %+v", links)
	}
}

func TestFetchGGDrive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dysk/IMAGEID12345/photo.jpg":
			_, _ = w.Write([]byte(`<html><head><meta property="og:type" content="ggdrive:image" /><meta property="ggdrive:filename" content="photo &amp; me.jpg" /><meta property="ggdrive:file_size" content="3" /></head></html>`))
		case "/dysk/DOCID1234567/doc.pdf":
			_, _ = w.Write([]byte(`<meta property="og:type" content="ggdrive:document" />`))
		case "/thumb/IMAGEID12345/photo.jpg":
			if r.URL.Query().Get("app_id") != "web-client" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	oldPage, oldThumb := GGDrivePageBase, GGDriveThumbBase
	GGDrivePageBase, GGDriveThumbBase = srv.URL+"/dysk/", srv.URL+"/thumb/"
	defer func() { GGDrivePageBase, GGDriveThumbBase = oldPage, oldThumb }()

	media, err := FetchMedia(context.Background(), srv.Client(), MediaLink{Kind: MediaGGDrive, ID: "IMAGEID12345", FileName: "photo.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if media.FileName != "photo & me.jpg" || media.MimeType != "image/jpeg" || len(media.Data) != 3 {
		t.Errorf("media = %+v", media)
	}
	if _, err := FetchMedia(context.Background(), srv.Client(), MediaLink{Kind: MediaGGDrive, ID: "DOCID1234567", FileName: "doc.pdf"}); !errors.Is(err, ErrNotDownloadable) {
		t.Errorf("document error = %v, want ErrNotDownloadable", err)
	}
}

func TestDownloadRejectsNonMedia(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	if _, err := download(context.Background(), srv.Client(), srv.URL+"/x.gif", "x.gif", true); err == nil {
		t.Error("HTML response accepted as media")
	}
}

func TestFindWebLinks(t *testing.T) {
	text := "foto: https://example.org/a/cat.jpg, i strona (http://example.com/page). gg https://www.gg.pl/dysk/AbCdEfGh12345678IjKlMnO/x.jpg"
	got := FindMediaLinks(text, true)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].URL != "https://example.org/a/cat.jpg" || got[0].Kind != MediaWeb {
		t.Errorf("first = %+v (trailing comma must go)", got[0])
	}
	if got[1].URL != "http://example.com/page" || got[1].Kind != MediaWeb {
		t.Errorf("second = %+v (trailing bracket and dot must go)", got[1])
	}
	if got[2].Kind != MediaGGDrive {
		t.Errorf("third = %+v, want GG Drive, not a duplicate web link", got[2])
	}
}

func TestFetchWebImageStopsOnHTML(t *testing.T) {
	big := make([]byte, 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cat.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(big)
	}))
	defer srv.Close()
	m, err := FetchMedia(context.Background(), srv.Client(), MediaLink{Kind: MediaWeb, URL: srv.URL + "/cat.png"})
	if err != nil || m.FileName != "cat.png" || m.MimeType != "image/png" {
		t.Fatalf("image: %+v, %v", m, err)
	}
	if _, err := FetchMedia(context.Background(), srv.Client(), MediaLink{Kind: MediaWeb, URL: srv.URL + "/page"}); !errors.Is(err, ErrNotMedia) {
		t.Errorf("html page error = %v, want ErrNotMedia", err)
	}
}

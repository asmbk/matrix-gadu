package connector

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// fakeIntent implements only UploadMedia. Other MatrixAPI calls panic.
type fakeIntent struct {
	bridgev2.MatrixAPI
	uploads []string
}

func (f *fakeIntent) UploadMedia(_ context.Context, _ id.RoomID, _ []byte, fileName, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	f.uploads = append(f.uploads, fileName)
	return id.ContentURIString("mxc://example.org/" + fileName), nil, nil
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mediaTestClient(t *testing.T) *GaduClient {
	client := newClient(&GaduConnector{}, &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "7654321"}}, selfUIN)
	client.main.Config.MaxMessageLength = 1900
	data := tinyPNG(t)
	client.fetchMedia = func(_ context.Context, link gadu.MediaLink) (*gadu.RemoteMedia, error) {
		if link.Kind == gadu.MediaGGDrive && link.FileName == "doc.pdf" {
			return nil, gadu.ErrNotDownloadable
		}
		return &gadu.RemoteMedia{Data: data, FileName: "photo.png", MimeType: "image/png"}, nil
	}
	return client
}

var testPortal = &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.org"}}

func TestConvertLinkOnlyBecomesImage(t *testing.T) {
	client, intent := mediaTestClient(t), &fakeIntent{}
	link := "https://www.gg.pl/dysk/AbCdEfGh12345678IjKlMnO/photo.png"
	msg := client.convertGGText(context.Background(), testPortal, intent, link, ggSpanOpen+link+"</span>")
	if len(msg.Parts) != 1 {
		t.Fatalf("parts = %d, want 1 (image only)", len(msg.Parts))
	}
	c := msg.Parts[0].Content
	if msg.Parts[0].ID != "" || c.MsgType != event.MsgImage || c.URL != "mxc://example.org/photo.png" || c.Info.Width != 3 || c.Info.Height != 2 || c.Info.MimeType != "image/png" {
		t.Errorf("image part = %+v info %+v", c, c.Info)
	}
}

func TestConvertTextWithLinks(t *testing.T) {
	client, intent := mediaTestClient(t), &fakeIntent{}
	text := "patrz https://media.giphy.com/media/abc/giphy.gif i https://www.gg.pl/dysk/DOCID1234567/doc.pdf"
	msg := client.convertGGText(context.Background(), testPortal, intent, text, "")
	if len(msg.Parts) != 2 {
		t.Fatalf("parts = %d, want text + gif", len(msg.Parts))
	}
	body := msg.Parts[0].Content.Body
	if strings.Contains(body, "giphy") || !strings.Contains(body, "doc.pdf") || !strings.HasPrefix(body, "patrz") {
		t.Errorf("text part = %q (the gif link must go, the pdf link must stay)", body)
	}
	if msg.Parts[1].ID != "part1" || msg.Parts[1].Content.MsgType != event.MsgImage {
		t.Errorf("media part = %+v", msg.Parts[1])
	}
}

func TestConvertKeepsTextWhenDownloadFails(t *testing.T) {
	client, intent := mediaTestClient(t), &fakeIntent{}
	client.fetchMedia = func(context.Context, gadu.MediaLink) (*gadu.RemoteMedia, error) { return nil, errors.New("HTTP 500") }
	text := "https://www.gg.pl/dysk/AbCdEfGh12345678IjKlMnO/photo.png"
	msg := client.convertGGText(context.Background(), testPortal, intent, text, "")
	if len(msg.Parts) != 1 || msg.Parts[0].Content.Body != text || len(intent.uploads) != 0 {
		t.Errorf("parts = %+v", msg.Parts)
	}
}

func TestMediaToGGText(t *testing.T) {
	client := mediaTestClient(t)
	client.publicMediaURL = func(_ context.Context, c *event.MessageEventContent) (string, error) {
		return "https://matrix.example.org/_mautrix/publicmedia/x/" + c.FileName, nil
	}
	out, err := client.mediaToGGText(context.Background(), &event.MessageEventContent{MsgType: event.MsgImage, Body: "nasz kot", FileName: "kot.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Body != "nasz kot\nhttps://matrix.example.org/_mautrix/publicmedia/x/kot.jpg" || out.MsgType != event.MsgText {
		t.Errorf("out = %+v", out)
	}
	out, _ = client.mediaToGGText(context.Background(), &event.MessageEventContent{MsgType: event.MsgFile, Body: "a.pdf", FileName: "a.pdf"})
	if out.Body != "https://matrix.example.org/_mautrix/publicmedia/x/a.pdf" {
		t.Errorf("file without caption = %q", out.Body)
	}
	client.publicMediaURL = func(context.Context, *event.MessageEventContent) (string, error) { return "", nil }
	if _, err := client.mediaToGGText(context.Background(), &event.MessageEventContent{MsgType: event.MsgImage}); !errors.Is(err, bridgev2.ErrUnsupportedMessageType) {
		t.Errorf("disabled public media error = %v", err)
	}
}

func TestImageLinksOption(t *testing.T) {
	client, intent := mediaTestClient(t), &fakeIntent{}
	text := "https://example.org/cat.png"
	if msg := client.convertGGText(context.Background(), testPortal, intent, text, ""); msg.Parts[0].Content.MsgType != event.MsgText {
		t.Error("web link became media with image_links off")
	}
	client.main.Config.ImageLinks = true
	msg := client.convertGGText(context.Background(), testPortal, intent, text, "")
	if len(msg.Parts) != 1 || msg.Parts[0].Content.MsgType != event.MsgImage {
		t.Errorf("web image link with image_links on = %+v", msg.Parts)
	}
	client.fetchMedia = func(context.Context, gadu.MediaLink) (*gadu.RemoteMedia, error) { return nil, gadu.ErrNotMedia }
	if msg := client.convertGGText(context.Background(), testPortal, intent, "https://example.org/page", ""); msg.Parts[0].Content.Body != "https://example.org/page" {
		t.Error("web page link was not kept as text")
	}
}

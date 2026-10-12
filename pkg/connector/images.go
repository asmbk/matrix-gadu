package connector

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// imageRequestTTL is how long the bridge waits for an image. The sender's
// client must be online to answer, so an image can stay missing.
const imageRequestTTL = 5 * time.Minute

type imageKey struct {
	sender gadu.UIN
	ref    gadu.ImageRef
}

// requestImages asks the sender's client for the images of a message.
func (gc *GaduClient) requestImages(ctx context.Context, e *gadu.MessageEvent) {
	sess := gc.session()
	if sess == nil {
		return
	}
	now := time.Now()
	gc.mu.Lock()
	for k, t := range gc.images {
		if now.Sub(t) > imageRequestTTL {
			delete(gc.images, k)
		}
	}
	var want []gadu.ImageRef
	for i, ref := range e.Images {
		if i >= maxMediaPerMessage || ref.Size == 0 || ref.Size > gadu.MaxImageSizeKiB*1024 {
			continue
		}
		key := imageKey{e.Sender, ref}
		if _, pending := gc.images[key]; pending {
			continue
		}
		gc.images[key] = e.Time
		want = append(want, ref)
	}
	gc.mu.Unlock()
	for _, ref := range want {
		if err := sess.RequestImage(ctx, e.Sender, ref); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Stringer("sender_uin", e.Sender).Msg("Failed to request a GG image")
		}
	}
	if len(e.Images) > len(want) {
		zerolog.Ctx(ctx).Debug().Int("images", len(e.Images)).Int("requested", len(want)).Msg("Skipped some GG images")
	}
}

// handleImage posts an image that the bridge requested. Images that nobody
// asked for are dropped, because GG clients can send them unasked.
func (gc *GaduClient) handleImage(ctx context.Context, e *gadu.ImageEvent) {
	key := imageKey{e.Sender, e.Ref}
	gc.mu.Lock()
	sent, ok := gc.images[key]
	delete(gc.images, key)
	gc.mu.Unlock()
	if !ok {
		zerolog.Ctx(ctx).Debug().Stringer("sender_uin", e.Sender).Msg("Dropping an image that the bridge did not request")
		return
	}
	// Windows clients send the full path of the file.
	name := path.Base(strings.ReplaceAll(e.FileName, "\\", "/"))
	media := &gadu.RemoteMedia{Data: e.Data, MimeType: http.DetectContentType(e.Data), FileName: name}
	if !strings.HasPrefix(media.MimeType, "image/") {
		if byExt := mime.TypeByExtension(path.Ext(name)); strings.HasPrefix(byExt, "image/") {
			media.MimeType = byExt
		}
	}
	if media.FileName == "" || media.FileName == "." || media.FileName == "/" {
		media.FileName = "image"
		if exts, _ := mime.ExtensionsByType(media.MimeType); len(exts) > 0 {
			media.FileName += exts[0]
		}
	}
	gc.queueEvent(&simplevent.Message[*gadu.RemoteMedia]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Stringer("sender_uin", e.Sender).Bool("gg_image", true)
			},
			PortalKey:    gc.makePortalKey(e.Sender),
			Sender:       gc.makeEventSender(e.Sender),
			CreatePortal: true,
			Timestamp:    sent,
		},
		Data: media,
		ID:   networkid.MessageID(fmt.Sprintf("img:%d:%d:%08x:%d", e.Sender, sent.UnixNano(), e.Ref.CRC32, e.Ref.Size)),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *gadu.RemoteMedia) (*bridgev2.ConvertedMessage, error) {
			part, err := uploadMediaPart(ctx, portal, intent, data)
			if err != nil {
				return nil, err
			}
			return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}, nil
		},
	})
}

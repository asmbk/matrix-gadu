package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

func (gc *GaduClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	content := msg.Content
	// imageTag carries an image with the GG image protocol, next to the link.
	var imageTag string
	switch content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		if content.MsgType == event.MsgImage {
			imageTag = gc.prepareOutImage(ctx, msg.Portal, content)
		}
		text, err := gc.mediaToGGText(ctx, content)
		switch {
		case err != nil && imageTag == "":
			return nil, err
		case err != nil:
			// Without public media, the image alone goes to GG.
			text = &event.MessageEventContent{MsgType: event.MsgText, Body: " "}
		}
		content = text
	default:
		return nil, fmt.Errorf("%w %s", bridgev2.ErrUnsupportedMessageType, content.MsgType)
	}
	peer, err := parsePortalID(msg.Portal.ID)
	if err != nil {
		return nil, err
	}
	sess := gc.session()
	if sess == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	parts := matrixToGG(content, gc.main.Config.MaxMessageLength)
	if imageTag != "" && len(parts) > 0 {
		parts[0] = imageTag + parts[0]
	}
	now := time.Now()
	var firstID networkid.MessageID
	var lastSeq gadu.Seq
	for i, part := range parts {
		seq, err := sess.SendMessage(ctx, peer, part)
		if err != nil {
			if i > 0 {
				return nil, fmt.Errorf("sent %d of %d parts: %w", i, len(parts), err)
			}
			return nil, err
		}
		if i == 0 {
			firstID = makeOutgoingMessageID(seq, now)
		}
		lastSeq = seq
	}
	resp := &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        firstID,
			SenderID:  makeUserID(gc.uin),
			Timestamp: now,
		},
	}
	gc.trackAck(lastSeq, pendingAck{portal: msg.Portal.PortalKey, msgID: resp.DB.ID, peer: peer, sent: now})
	return resp, nil
}

func (gc *GaduClient) HandleMatrixTyping(ctx context.Context, msg *bridgev2.MatrixTyping) error {
	if msg.Type != bridgev2.TypingTypeText {
		return nil
	}
	peer, err := parsePortalID(msg.Portal.ID)
	if err != nil {
		return err
	}
	sess := gc.session()
	if sess == nil {
		return nil
	}
	length := 0
	if msg.IsTyping {
		length = 1
	}
	return sess.SendTyping(ctx, peer, length)
}

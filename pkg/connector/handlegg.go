package connector

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

const typingTimeout = 15 * time.Second

func (gc *GaduClient) handleGGEvent(ctx context.Context, evt gadu.Event) {
	log := zerolog.Ctx(ctx)
	switch e := evt.(type) {
	case *gadu.MessageEvent:
		gc.handleMessage(ctx, e)
	case *gadu.OwnMessageEvent:
		gc.handleOwnMessage(e)
	case *gadu.TypingEvent:
		timeout := typingTimeout
		if e.Length == 0 {
			timeout = 0
		}
		gc.queueEvent(&simplevent.Typing{
			EventMeta: simplevent.EventMeta{
				Type:      bridgev2.RemoteEventTyping,
				PortalKey: gc.makePortalKey(e.UIN),
				Sender:    gc.makeEventSender(e.UIN),
			},
			Timeout: timeout,
		})
	case *gadu.AckEvent:
		gc.handleAck(e)
	case *gadu.ContactListEvent:
		gc.handleContactList(ctx, e)
	case *gadu.ImageRequestEvent:
		gc.handleImageRequest(ctx, e)
	case *gadu.ImageEvent:
		gc.handleImage(ctx, e)
	case *gadu.ContactListPutEvent:
		gc.handleContactListPut(e)
	case *gadu.ContactListVersionEvent:
		gc.handleContactListVersion(ctx, e)
	case *gadu.StatusEvent:
		log.Trace().Stringer("uin", e.UIN).Stringer("status", e.Status).Msg("Contact status changed")
		gc.handleStatus(ctx, e)
	case *gadu.MultilogonInfoEvent:
		log.Debug().Int("sessions", len(e.Sessions)).Msg("Other GG sessions of this account")
	case *gadu.DisconnectedEvent:
		log.Debug().Msg("GG session ended")
	}
}

func (gc *GaduClient) handleMessage(ctx context.Context, e *gadu.MessageEvent) {
	if gc.isBlocked(e.Sender) {
		// The server drops these too, but a block reaches it only after the list loads.
		zerolog.Ctx(ctx).Debug().Stringer("sender_uin", e.Sender).Msg("Dropping a message from a blocked contact")
		return
	}
	// A message from a number outside the contact list creates a room, so follow its status.
	gc.subscribe(ctx, e.Sender)
	if len(e.Images) > 0 {
		gc.requestImages(ctx, e)
		e.HTML = gadu.StripHTMLImages(e.HTML)
		if strings.TrimSpace(e.Plain) == "" {
			// Kadu sends a space as the text of an image-only message.
			return
		}
	}
	gc.queueEvent(&simplevent.Message[*gadu.MessageEvent]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Stringer("sender_uin", e.Sender).Bool("offline", e.Offline)
			},
			PortalKey:    gc.makePortalKey(e.Sender),
			Sender:       gc.makeEventSender(e.Sender),
			CreatePortal: true,
			Timestamp:    e.Time,
		},
		Data: e,
		ID:   makeIncomingMessageID("", e.Sender, e.Time, e.Plain+e.HTML),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *gadu.MessageEvent) (*bridgev2.ConvertedMessage, error) {
			return gc.convertGGText(ctx, portal, intent, data.Plain, data.HTML), nil
		},
	})
}

func (gc *GaduClient) handleOwnMessage(e *gadu.OwnMessageEvent) {
	gc.queueEvent(&simplevent.Message[*gadu.OwnMessageEvent]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Stringer("recipient_uin", e.Recipient).Bool("multilogon", true)
			},
			PortalKey:    gc.makePortalKey(e.Recipient),
			Sender:       gc.makeEventSender(gc.uin),
			CreatePortal: true,
			Timestamp:    e.Time,
		},
		Data: e,
		ID:   makeIncomingMessageID("own:", e.Recipient, e.Time, e.Plain+e.HTML),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *gadu.OwnMessageEvent) (*bridgev2.ConvertedMessage, error) {
			return gc.convertGGText(ctx, portal, intent, data.Plain, data.HTML), nil
		},
	})
}

func textMessage(content *event.MessageEventContent) *bridgev2.ConvertedMessage {
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: content,
		}},
	}
}

func (gc *GaduClient) handleAck(e *gadu.AckEvent) {
	if e.State != gadu.AckDelivered {
		return
	}
	ack, ok := gc.popAck(e.Seq)
	if !ok {
		return
	}
	gc.queueEvent(&simplevent.Receipt{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventDeliveryReceipt,
			PortalKey: ack.portal,
			Sender:    gc.makeEventSender(ack.peer),
		},
		Targets: []networkid.MessageID{ack.msgID},
	})
}

func (gc *GaduClient) handleContactList(ctx context.Context, e *gadu.ContactListEvent) {
	log := zerolog.Ctx(ctx)
	uins := make([]gadu.UIN, 0, len(e.Contacts))
	gc.mu.Lock()
	gc.contacts = make(map[gadu.UIN]gadu.Contact, len(e.Contacts))
	gc.contactRaw, gc.contactVersion = e.Raw, e.Version
	gc.contactsLoaded = true
	for _, c := range e.Contacts {
		gc.contacts[c.UIN] = c
		uins = append(uins, c.UIN)
	}
	gc.mu.Unlock()
	log.Debug().Int("contacts", len(uins)).Msg("Received the GG contact list")

	// Numbers outside the contact list that have a room need a status too.
	peers := append(uins, gc.roomPeers(ctx)...)
	gc.subscribe(ctx, peers...)
	gc.applyBlocks(ctx, e.Contacts)
	gc.applyRoomPermissions(ctx, peers)
	go gc.syncAvatars(ctx, peers)
	// Update only ghosts that already exist. New ghosts get their name from GetUserInfo.
	for _, c := range e.Contacts {
		ghost, err := gc.existingGhost(ctx, makeUserID(c.UIN))
		if err != nil {
			log.Warn().Err(err).Stringer("uin", c.UIN).Msg("Failed to get ghost")
			continue
		} else if ghost == nil {
			continue
		}
		ghost.UpdateInfo(ctx, gc.userInfo(c.UIN))
	}
	if gc.main.Config.ContactRooms {
		go gc.createContactRooms(ctx, e.Contacts)
	}
	if gc.main.Config.StatusInName {
		gc.applyAllStatusMeta(ctx)
	}
}

// defaultContactRoomInterval spaces out room creation, so that a long contact
// list does not flood the homeserver.
const defaultContactRoomInterval = 200 * time.Millisecond

// createContactRooms creates a room for each contact that has none yet.
func (gc *GaduClient) createContactRooms(ctx context.Context, contacts []gadu.Contact) {
	created := 0
	for _, c := range contacts {
		if c.UIN == gc.uin {
			continue
		}
		key := gc.makePortalKey(c.UIN)
		if gc.hasRoom(ctx, key) {
			continue
		}
		peer := c.UIN
		gc.queueEvent(&simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				PortalKey:    key,
				CreatePortal: true,
				LogContext: func(lc zerolog.Context) zerolog.Context {
					return lc.Stringer("contact_uin", peer)
				},
			},
			ChatInfo: gc.dmChatInfo(peer, true),
		})
		created++
		select {
		case <-ctx.Done():
			return
		case <-time.After(gc.contactRoomInterval):
		}
	}
	if created > 0 {
		zerolog.Ctx(ctx).Info().Int("rooms", created).Msg("Queued rooms for GG contacts")
		gc.applyAllStatusMeta(ctx)
	}
}

package connector

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// statusReadLookback is how many recent room events the bridge reads to find
// out if the user has unread messages.
const statusReadLookback = 30

var _ bridgev2.ReadReceiptHandlingNetworkAPI = (*GaduClient)(nil)

// HandleMatrixReadReceipt records how far the user read a room. GG has no
// read receipts, so the bridge only uses this for status_changes_read.
func (gc *GaduClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	if msg.Portal == nil || msg.Portal.MXID == "" || msg.EventID == "" {
		return nil
	}
	gc.mu.Lock()
	gc.lastRead[msg.Portal.MXID] = msg.EventID
	gc.mu.Unlock()
	return nil
}

// isNotable tells if an event counts as unread in clients: messages, not state changes.
func isNotable(evt *event.Event) bool {
	if evt.StateKey != nil {
		return false
	}
	switch evt.Type {
	case event.EventMessage, event.EventEncrypted, event.EventSticker:
		return true
	}
	return false
}

// hasUnreadMessages tells if the user has unread messages in a room. The
// events go from the newest to the oldest. The user read everything up to
// the read marker, and everything before their own last message.
func hasUnreadMessages(events []*event.Event, readMarker id.EventID, user id.UserID) bool {
	for _, evt := range events {
		if readMarker != "" && evt.ID == readMarker {
			return false
		}
		if !isNotable(evt) {
			continue
		}
		return evt.Sender != user
	}
	return false
}

// markStatusChangeRead moves the read marker of the user over the status
// change that the bridge just sent, so that clients like NeoChat do not
// count the room name and topic change as unread. It does nothing if the user
// has unread messages in the room, so that real messages stay unread.
func (gc *GaduClient) markStatusChangeRead(ctx context.Context, portal *bridgev2.Portal) {
	if err := gc.markRead(ctx, portal); err != nil {
		zerolog.Ctx(ctx).Debug().Err(err).Msg("Failed to mark the status change as read")
	}
}

func (gc *GaduClient) markRead(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil || portal.MXID == "" {
		return nil
	}
	dp, ok := gc.login.User.DoublePuppet(ctx).(*matrix.ASIntent)
	if !ok || dp == nil {
		return nil
	}
	resp, err := dp.Matrix.Messages(ctx, portal.MXID, "", "", mautrix.DirectionBackward, nil, statusReadLookback)
	if err != nil {
		return fmt.Errorf("read recent events: %w", err)
	}
	if len(resp.Chunk) == 0 {
		return nil
	}
	gc.mu.Lock()
	marker := gc.lastRead[portal.MXID]
	gc.mu.Unlock()
	if marker == "" {
		var fullyRead event.FullyReadEventContent
		if err := dp.Matrix.GetRoomAccountData(ctx, portal.MXID, event.AccountDataFullyRead.Type, &fullyRead); err == nil {
			marker = fullyRead.EventID
		}
	}
	newest := resp.Chunk[0].ID
	if newest == marker {
		return nil
	}
	if hasUnreadMessages(resp.Chunk, marker, gc.login.UserMXID) {
		return nil
	}
	if err := dp.Matrix.SetReadMarkers(ctx, portal.MXID, &mautrix.ReqSetReadMarkers{Read: newest, FullyRead: newest}); err != nil {
		return fmt.Errorf("set read markers: %w", err)
	}
	gc.mu.Lock()
	gc.lastRead[portal.MXID] = newest
	gc.mu.Unlock()
	return nil
}

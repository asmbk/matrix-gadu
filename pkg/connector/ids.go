package connector

import (
	"crypto/sha256"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

func makeUserID(uin gadu.UIN) networkid.UserID {
	return networkid.UserID(uin.String())
}

func parseUserID(id networkid.UserID) (gadu.UIN, error) {
	return gadu.ParseUIN(string(id))
}

func makeUserLoginID(uin gadu.UIN) networkid.UserLoginID {
	return networkid.UserLoginID(uin.String())
}

func parseUserLoginID(id networkid.UserLoginID) (gadu.UIN, error) {
	return gadu.ParseUIN(string(id))
}

func parsePortalID(id networkid.PortalID) (gadu.UIN, error) {
	return gadu.ParseUIN(string(id))
}

// makeIncomingMessageID builds an ID for a received message. GG gives no
// stable message ID to clients, so the ID combines sender, time and a hash of the text.
func makeIncomingMessageID(prefix string, peer gadu.UIN, t time.Time, text string) networkid.MessageID {
	hash := sha256.Sum256([]byte(text))
	return networkid.MessageID(fmt.Sprintf("%s%d:%d:%x", prefix, peer, t.UnixNano(), hash[:4]))
}

// makeOutgoingMessageID builds an ID for a message sent from Matrix.
// The sequence number alone repeats across sessions, so the ID also has the send time.
func makeOutgoingMessageID(seq gadu.Seq, t time.Time) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("out:%d:%d", seq, t.UnixNano()))
}

func (gc *GaduClient) makePortalKey(peer gadu.UIN) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(peer.String()),
		Receiver: gc.login.ID,
	}
}

func (gc *GaduClient) makeEventSender(uin gadu.UIN) bridgev2.EventSender {
	if uin == gc.uin {
		return bridgev2.EventSender{IsFromMe: true, SenderLogin: gc.login.ID, Sender: makeUserID(uin)}
	}
	return bridgev2.EventSender{Sender: makeUserID(uin)}
}

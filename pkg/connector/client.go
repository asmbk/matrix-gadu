package connector

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

const (
	connectTimeout = 2 * time.Minute
	// A login is a conflict with another GG client when the server closes
	// the session conflictCount times within conflictWindow.
	conflictCount  = 3
	conflictWindow = 10 * time.Minute
	ackTTL         = time.Hour
)

var (
	// reconnectDelays is the backoff after failed connection attempts.
	reconnectDelays = []time.Duration{5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}
	conflictWait    = 15 * time.Minute
)

type pendingAck struct {
	portal networkid.PortalKey
	msgID  networkid.MessageID
	peer   gadu.UIN
	sent   time.Time
}

type GaduClient struct {
	main  *GaduConnector
	login *bridgev2.UserLogin
	uin   gadu.UIN

	loggedIn atomic.Bool

	// sendState, queueEvent and existingGhost are the bridge calls. Tests replace them.
	sendState      func(status.BridgeState)
	queueEvent     func(bridgev2.RemoteEvent)
	existingGhost  func(context.Context, networkid.UserID) (*bridgev2.Ghost, error)
	hasRoom        func(context.Context, networkid.PortalKey) bool
	setPresence    func(context.Context, gadu.UIN, contactStatus) error
	setLowPriority func(context.Context, gadu.UIN, bool) error
	markStatusRead func(context.Context, *bridgev2.Portal)
	roomPeers      func(context.Context) []gadu.UIN
	fetchAvatar    func(context.Context, gadu.UIN) (*gadu.Avatar, error)
	fetchMedia     func(context.Context, gadu.MediaLink) (*gadu.RemoteMedia, error)
	publicMediaURL func(context.Context, *event.MessageEventContent) (string, error)

	contactRoomInterval     time.Duration
	presenceRefreshInterval time.Duration
	offlineSweepDelay       time.Duration
	avatarFetchInterval     time.Duration
	contactPutTimeout       time.Duration

	// editMu lets one contact list edit run at a time.
	editMu   sync.Mutex
	putReply chan *gadu.ContactListPutEvent

	mu       sync.Mutex
	sess     gadu.Session
	stop     context.CancelFunc
	runDone  chan struct{}
	contacts map[gadu.UIN]gadu.Contact
	// contactRaw and contactVersion are the last list from the server, the start of edits.
	contactRaw     string
	contactVersion uint32
	// contactsLoaded is true after the first contact list arrived. Before that,
	// contact names are unknown, so the bridge does not change room names.
	contactsLoaded bool
	statuses       map[gadu.UIN]contactStatus
	// notified holds the numbers that the current session subscribed to.
	notified map[gadu.UIN]struct{}
	// blocked holds the numbers that the current session blocks.
	blocked map[gadu.UIN]bool
	// lastRead holds the last read receipt of the user in each room.
	lastRead map[id.RoomID]id.EventID
	// images holds the images that the bridge requested and not received yet.
	images  map[imageKey]time.Time
	avatars map[gadu.UIN]cachedAvatar
	// lowPriority holds the last low priority state that the bridge applied to each room.
	lowPriority map[gadu.UIN]bool
	acks        map[gadu.Seq]pendingAck
	disconnects []time.Time
}

var (
	_ bridgev2.NetworkAPI                    = (*GaduClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*GaduClient)(nil)
	_ bridgev2.GhostDMCreatingNetworkAPI     = (*GaduClient)(nil)
	_ bridgev2.ContactListingNetworkAPI      = (*GaduClient)(nil)
	_ bridgev2.TypingHandlingNetworkAPI      = (*GaduClient)(nil)
	_ bridgev2.UserSearchingNetworkAPI       = (*GaduClient)(nil)
	_ bridgev2.RoomAvatarHandlingNetworkAPI  = (*GaduClient)(nil)
)

func newClient(main *GaduConnector, login *bridgev2.UserLogin, uin gadu.UIN) *GaduClient {
	client := &GaduClient{
		main:      main,
		login:     login,
		uin:       uin,
		sendState: func(state status.BridgeState) { login.BridgeState.Send(state) },
		queueEvent: func(evt bridgev2.RemoteEvent) {
			login.QueueRemoteEvent(evt)
		},
		existingGhost: func(ctx context.Context, id networkid.UserID) (*bridgev2.Ghost, error) {
			return main.br.GetExistingGhostByID(ctx, id)
		},
		hasRoom: func(ctx context.Context, key networkid.PortalKey) bool {
			portal, err := main.br.GetExistingPortalByKey(ctx, key)
			return err == nil && portal != nil && portal.MXID != ""
		},
		contacts:                make(map[gadu.UIN]gadu.Contact),
		statuses:                make(map[gadu.UIN]contactStatus),
		notified:                make(map[gadu.UIN]struct{}),
		blocked:                 make(map[gadu.UIN]bool),
		lastRead:                make(map[id.RoomID]id.EventID),
		images:                  make(map[imageKey]time.Time),
		avatars:                 make(map[gadu.UIN]cachedAvatar),
		lowPriority:             make(map[gadu.UIN]bool),
		acks:                    make(map[gadu.Seq]pendingAck),
		contactRoomInterval:     defaultContactRoomInterval,
		presenceRefreshInterval: presenceRefreshInterval,
		offlineSweepDelay:       defaultOfflineSweepDelay,
		avatarFetchInterval:     defaultAvatarFetchInterval,
		contactPutTimeout:       contactPutTimeout,
		putReply:                make(chan *gadu.ContactListPutEvent, 1),
	}
	client.setPresence = client.setGhostPresence
	client.setLowPriority = client.setRoomLowPriority
	client.markStatusRead = client.markStatusChangeRead
	client.roomPeers = client.listRoomPeers
	client.fetchAvatar = fetchGGAvatar
	client.fetchMedia = fetchGGMedia
	client.publicMediaURL = client.publicMediaURLFromBridge
	return client
}

func (gc *GaduConnector) sessionConfig(ctx context.Context, uin gadu.UIN, password, statusText string) gadu.Config {
	return gadu.Config{
		UIN:         uin,
		Password:    password,
		Status:      gc.Config.initialStatus,
		Description: statusText,
		TLS:         gc.Config.tlsMode,
		Multilogon:  gc.Config.Multilogon,
		Log:         zerolog.Ctx(ctx).With().Str("component", "gadu").Logger(),
	}
}

func (gc *GaduClient) metadata() *UserLoginMetadata {
	return gc.login.Metadata.(*UserLoginMetadata)
}

// session returns the connected session, or nil.
func (gc *GaduClient) session() gadu.Session {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	return gc.sess
}

func (gc *GaduClient) Connect(ctx context.Context) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	if gc.stop != nil {
		return
	}
	runCtx, cancel := context.WithCancel(gc.login.Log.WithContext(context.WithoutCancel(ctx)))
	gc.stop = cancel
	gc.runDone = make(chan struct{})
	go gc.run(runCtx, gc.runDone)
}

func (gc *GaduClient) Disconnect() {
	gc.mu.Lock()
	stop, done, sess := gc.stop, gc.runDone, gc.sess
	gc.stop = nil
	gc.mu.Unlock()
	if stop == nil {
		return
	}
	stop()
	if sess != nil {
		_ = sess.Close()
	}
	<-done
}

func (gc *GaduClient) IsLoggedIn() bool {
	return gc.loggedIn.Load()
}

func (gc *GaduClient) LogoutRemote(ctx context.Context) {
	// GG has no tokens to invalidate. The bridge deletes the login with its stored password.
	gc.Disconnect()
}

func (gc *GaduClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return userID == makeUserID(gc.uin)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (gc *GaduClient) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	log := zerolog.Ctx(ctx)
	attempt := 0
	for ctx.Err() == nil {
		meta := gc.metadata()
		sess := gc.main.NewSession(gc.main.sessionConfig(ctx, gc.uin, meta.Password, meta.StatusText))
		gc.sendState(status.BridgeState{StateEvent: status.StateConnecting})
		connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		err := sess.Connect(connectCtx)
		cancel()
		if err != nil {
			_ = sess.Close()
			if ctx.Err() != nil {
				return
			}
			var loginErr *gadu.LoginError
			if errors.As(err, &loginErr) && loginErr.BadCredentials() {
				log.Warn().Stringer("reason", loginErr.Reason).Msg("GG rejected the credentials")
				gc.sendState(status.BridgeState{
					StateEvent: status.StateBadCredentials,
					Error:      "gg-bad-credentials",
					Message:    loginErr.Error(),
				})
				return
			}
			delay := reconnectDelays[min(attempt, len(reconnectDelays)-1)]
			attempt++
			log.Warn().Err(err).Stringer("retry_in", delay).Msg("Failed to connect to GG")
			gc.sendState(status.BridgeState{
				StateEvent: status.StateTransientDisconnect,
				Error:      "gg-connect-failed",
				Message:    err.Error(),
			})
			if !sleepCtx(ctx, delay) {
				return
			}
			continue
		}
		attempt = 0
		gc.mu.Lock()
		gc.sess = sess
		gc.notified = make(map[gadu.UIN]struct{})
		gc.blocked = make(map[gadu.UIN]bool)
		gc.mu.Unlock()
		gc.loggedIn.Store(true)
		gc.sendState(status.BridgeState{StateEvent: status.StateConnected})
		if err := sess.RequestContactList(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to request the contact list")
		}

		gc.consume(ctx, sess)

		gc.loggedIn.Store(false)
		gc.clearStatuses(context.WithoutCancel(ctx))
		gc.mu.Lock()
		gc.sess = nil
		gc.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if gc.recordDisconnect(time.Now()) {
			log.Warn().Msg("The GG server closed the session repeatedly, another client probably uses the account")
			gc.sendState(status.BridgeState{
				StateEvent: status.StateTransientDisconnect,
				Error:      "gg-conflict",
				Message:    "Another GG client uses this account. Enable multilogon in that client, or close it. The bridge retries in 15 minutes.",
			})
			if !sleepCtx(ctx, conflictWait) {
				return
			}
			continue
		}
		gc.sendState(status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      "gg-disconnected",
			Message:    "The GG server closed the connection",
		})
		if !sleepCtx(ctx, reconnectDelays[0]) {
			return
		}
	}
}

// recordDisconnect stores the time of a disconnect and returns true if it is a conflict.
func (gc *GaduClient) recordDisconnect(now time.Time) bool {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	recent := gc.disconnects[:0]
	for _, t := range gc.disconnects {
		if now.Sub(t) < conflictWindow {
			recent = append(recent, t)
		}
	}
	gc.disconnects = append(recent, now)
	if len(gc.disconnects) >= conflictCount {
		gc.disconnects = nil
		return true
	}
	return false
}

func (gc *GaduClient) consume(ctx context.Context, sess gadu.Session) {
	events := sess.Events()
	refresh := time.NewTicker(gc.presenceRefreshInterval)
	defer refresh.Stop()
	offlineSweep := time.NewTimer(gc.offlineSweepDelay)
	defer offlineSweep.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = sess.Close()
			return
		case <-refresh.C:
			gc.refreshPresence(ctx)
		case <-offlineSweep.C:
			gc.markSilentPeersOffline(ctx)
		case evt, ok := <-events:
			if !ok {
				return
			}
			gc.handleGGEvent(ctx, evt)
		}
	}
}

func (gc *GaduClient) trackAck(seq gadu.Seq, ack pendingAck) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	for s, a := range gc.acks {
		if ack.sent.Sub(a.sent) > ackTTL {
			delete(gc.acks, s)
		}
	}
	gc.acks[seq] = ack
}

func (gc *GaduClient) popAck(seq gadu.Seq) (pendingAck, bool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	ack, ok := gc.acks[seq]
	delete(gc.acks, seq)
	return ack, ok
}

func (gc *GaduClient) contact(uin gadu.UIN) (gadu.Contact, bool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	c, ok := gc.contacts[uin]
	return c, ok
}

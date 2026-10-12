// Package fake is an in-memory [gadu.Session] for tests.
package fake

import (
	"context"
	"sync"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// SentMessage is a message that the code under test sent.
type SentMessage struct {
	To   gadu.UIN
	HTML string
	Seq  gadu.Seq
}

// Network creates fake sessions and records what they do.
type Network struct {
	mu sync.Mutex
	// LoginErr is returned by the next Connect calls, if not nil.
	LoginErr error
	Sessions []*Session
	Sent     []SentMessage
	Typing   []gadu.TypingEvent
	Notified []gadu.UIN
	Status   gadu.Status
	Descr    string
	// ContactList is sent as a ContactListEvent on RequestContactList.
	ContactList []gadu.Contact
	// ContactListRaw, if set, is the GG 10 list. It replaces ContactList and
	// changes on PutContactList.
	ContactListRaw     string
	ContactListVersion uint32
	// Puts counts the PutContactList calls.
	Puts int
	// Blocked holds the numbers that the current sessions block.
	Blocked map[gadu.UIN]bool
	// ImageRequests holds the RequestImage calls.
	ImageRequests []gadu.ImageRef
	connects      chan *Session
}

// NewNetwork creates an empty fake network.
func NewNetwork() *Network {
	return &Network{connects: make(chan *Session, 16)}
}

// Factory returns a gadu.Factory that creates sessions on this network.
func (n *Network) Factory() gadu.Factory {
	return func(cfg gadu.Config) gadu.Session {
		s := &Session{net: n, Config: cfg, events: make(chan gadu.Event, 64), closed: make(chan struct{})}
		n.mu.Lock()
		n.Sessions = append(n.Sessions, s)
		n.mu.Unlock()
		return s
	}
}

// SetLoginErr sets the error that the next Connect calls return.
func (n *Network) SetLoginErr(err error) {
	n.mu.Lock()
	n.LoginErr = err
	n.mu.Unlock()
}

// Connected returns a channel that receives each session after a successful Connect.
func (n *Network) Connected() <-chan *Session {
	return n.connects
}

// NotifiedUINs returns a copy of the numbers passed to Notify.
func (n *Network) NotifiedUINs() []gadu.UIN {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]gadu.UIN(nil), n.Notified...)
}

// SentMessages returns a copy of the sent messages.
func (n *Network) SentMessages() []SentMessage {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]SentMessage(nil), n.Sent...)
}

// Session is a fake gadu.Session.
type Session struct {
	net       *Network
	Config    gadu.Config
	events    chan gadu.Event
	closed    chan struct{}
	closeOnce sync.Once
	// evMu guards sends to events against close(events).
	evMu     sync.RWMutex
	evClosed bool
	seq      gadu.Seq
}

var _ gadu.Session = (*Session)(nil)

func (s *Session) Connect(ctx context.Context) error {
	s.net.mu.Lock()
	err := s.net.LoginErr
	s.net.mu.Unlock()
	if err != nil {
		s.end()
		return err
	}
	s.net.connects <- s
	return nil
}

func (s *Session) end() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.evMu.Lock()
		s.evClosed = true
		close(s.events)
		s.evMu.Unlock()
	})
}

func (s *Session) Close() error {
	s.end()
	return nil
}

// Disconnect simulates the server closing the session.
func (s *Session) Disconnect() {
	s.Inject(&gadu.DisconnectedEvent{})
	s.end()
}

// Inject delivers an event to the code under test.
func (s *Session) Inject(evt gadu.Event) {
	s.evMu.RLock()
	defer s.evMu.RUnlock()
	if s.evClosed {
		return
	}
	select {
	case <-s.closed:
	case s.events <- evt:
	}
}

func (s *Session) Events() <-chan gadu.Event {
	return s.events
}

func (s *Session) check() error {
	select {
	case <-s.closed:
		return gadu.ErrClosed
	default:
		return nil
	}
}

func (s *Session) SendMessage(ctx context.Context, to gadu.UIN, html string) (gadu.Seq, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	s.seq++
	s.net.Sent = append(s.net.Sent, SentMessage{To: to, HTML: html, Seq: s.seq})
	return s.seq, nil
}

func (s *Session) SendTyping(ctx context.Context, to gadu.UIN, length int) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	s.net.Typing = append(s.net.Typing, gadu.TypingEvent{UIN: to, Length: length})
	return nil
}

func (s *Session) SetStatus(ctx context.Context, st gadu.Status, descr string) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	s.net.Status, s.net.Descr = st, descr
	return nil
}

func (s *Session) Notify(ctx context.Context, contacts []gadu.UIN) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	s.net.Notified = append(s.net.Notified, contacts...)
	return nil
}

func (s *Session) RequestContactList(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	evt := &gadu.ContactListEvent{Version: s.net.ContactListVersion, Raw: s.net.ContactListRaw}
	if evt.Raw != "" {
		evt.Contacts, _ = gadu.ParseContactList(evt.Raw)
	} else {
		evt.Contacts = append([]gadu.Contact(nil), s.net.ContactList...)
	}
	s.net.mu.Unlock()
	go s.Inject(evt)
	return nil
}

func (s *Session) SetBlocked(ctx context.Context, uin gadu.UIN, blocked bool) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	if s.net.Blocked == nil {
		s.net.Blocked = make(map[gadu.UIN]bool)
	}
	if blocked {
		s.net.Blocked[uin] = true
	} else {
		delete(s.net.Blocked, uin)
	}
	return nil
}

// RequestImage records the request. Tests answer with Inject(&gadu.ImageEvent{...}).
func (s *Session) RequestImage(ctx context.Context, from gadu.UIN, ref gadu.ImageRef) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	s.net.ImageRequests = append(s.net.ImageRequests, ref)
	return nil
}

// ImageRequestCount returns the number of image requests.
func (n *Network) ImageRequestCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.ImageRequests)
}

// IsBlocked tells if a session blocked the number.
func (n *Network) IsBlocked(uin gadu.UIN) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.Blocked[uin]
}

// PutContactList accepts the list if the version matches, like the GG server.
func (s *Session) PutContactList(ctx context.Context, version uint32, data string) error {
	if err := s.check(); err != nil {
		return err
	}
	s.net.mu.Lock()
	s.net.Puts++
	evt := &gadu.ContactListPutEvent{Version: s.net.ContactListVersion}
	if version == s.net.ContactListVersion {
		s.net.ContactListVersion++
		s.net.ContactListRaw = data
		evt = &gadu.ContactListPutEvent{Accepted: true, Version: s.net.ContactListVersion}
	}
	s.net.mu.Unlock()
	go s.Inject(evt)
	return nil
}

// ContactListState returns the server-side GG 10 list, its version and the number of puts.
func (n *Network) ContactListState() (raw string, version uint32, puts int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ContactListRaw, n.ContactListVersion, n.Puts
}

// SetContactListVersion changes the server version, like an edit in another client.
func (n *Network) SetContactListVersion(v uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ContactListVersion = v
}

// Package libgadu implements [gadu.Session] with the libgadu C library.
//
// libgadu is not thread-safe. Each session has one owner goroutine, locked to
// its OS thread, that makes every libgadu call for that session. Other
// goroutines send commands to the owner through a channel and wake it up
// through a pipe.
package libgadu

/*
#cgo pkg-config: libgadu
#include <stdlib.h>
#include <string.h>
#include <libgadu.h>

static struct gg_session *mg_login(uin_t uin, const char *password, int status, const char *descr, int tls, int multilogon) {
	struct gg_login_params p;
	memset(&p, 0, sizeof(p));
	p.uin = uin;
	p.password = (char *) password;
	p.async = 1;
	p.status = status;
	p.status_descr = (char *) descr;
	p.protocol_version = GG_PROTOCOL_VERSION_110;
	p.encoding = GG_ENCODING_UTF8;
	p.resolver = GG_RESOLVER_PTHREAD;
	p.tls = tls;
	// GG_FEATURE_ALL does not include GG_FEATURE_MULTILOGON.
	p.protocol_features = GG_FEATURE_ALL | (multilogon ? GG_FEATURE_MULTILOGON : 0);
	p.struct_size = sizeof(p);
	return gg_login(&p);
}

static int mg_fd(struct gg_session *s) { return s->fd; }
static int mg_check(struct gg_session *s) { return s->check; }
static int mg_timeout(struct gg_session *s) { return s->timeout; }
static void mg_set_timeout(struct gg_session *s, int t) { s->timeout = t; }

static int mg_failure(struct gg_event *e) { return e->event.failure; }
static struct gg_event_msg *mg_msg(struct gg_event *e) { return &e->event.msg; }
static struct gg_event_msg *mg_multilogon_msg(struct gg_event *e) { return &e->event.multilogon_msg; }
static struct gg_event_ack *mg_ack(struct gg_event *e) { return &e->event.ack; }
static struct gg_event_ack110 *mg_ack110(struct gg_event *e) { return &e->event.ack110; }
static struct gg_event_status60 *mg_status60(struct gg_event *e) { return &e->event.status60; }
static struct gg_event_notify60 *mg_notify60_at(struct gg_event *e, int i) { return &e->event.notify60[i]; }
static struct gg_event_typing_notification *mg_typing(struct gg_event *e) { return &e->event.typing_notification; }
static struct gg_event_userlist100_reply *mg_userlist100(struct gg_event *e) { return &e->event.userlist100_reply; }
static uint32_t mg_userlist100_version(struct gg_event *e) { return e->event.userlist100_version.version; }
static struct gg_event_multilogon_info *mg_multilogon_info(struct gg_event *e) { return &e->event.multilogon_info; }
static struct gg_multilogon_session *mg_multilogon_session_at(struct gg_event_multilogon_info *i, int n) { return &i->sessions[n]; }
*/
import "C"

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

const pingInterval = 60 * time.Second

type command func(gs *C.struct_gg_session)

type session struct {
	cfg gadu.Config

	events      chan gadu.Event
	cmds        chan command
	loginResult chan error
	quit        chan struct{}
	done        chan struct{}

	wakeMu       sync.Mutex
	wakeR, wakeW int
	wakeClosed   bool
	started      bool
	startMu      sync.Mutex
	closeOnce    sync.Once

	// loginReported is used only by the owner goroutine.
	loginReported bool
}

var _ gadu.Session = (*session)(nil)

// New creates a session. It does not connect. Call Connect to log in.
func New(cfg gadu.Config) gadu.Session {
	return &session{
		cfg:         cfg,
		events:      make(chan gadu.Event, 256),
		cmds:        make(chan command, 64),
		loginResult: make(chan error, 1),
		quit:        make(chan struct{}),
		done:        make(chan struct{}),
		wakeR:       -1,
		wakeW:       -1,
	}
}

// Version returns the version of the loaded libgadu library.
func Version() string {
	return C.GoString(C.gg_libgadu_version())
}

func (s *session) Events() <-chan gadu.Event {
	return s.events
}

func (s *session) Connect(ctx context.Context) error {
	s.startMu.Lock()
	if s.started {
		s.startMu.Unlock()
		return errors.New("gadu: session already started")
	}
	select {
	case <-s.quit:
		s.startMu.Unlock()
		return gadu.ErrClosed
	default:
	}
	var p [2]int
	if err := unix.Pipe2(p[:], unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
		s.startMu.Unlock()
		return fmt.Errorf("gadu: create wake pipe: %w", err)
	}
	s.wakeR, s.wakeW = p[0], p[1]
	s.started = true
	s.startMu.Unlock()

	go s.run()

	select {
	case err := <-s.loginResult:
		return err
	case <-ctx.Done():
		_ = s.Close()
		return ctx.Err()
	}
}

func (s *session) Close() error {
	s.closeOnce.Do(func() {
		close(s.quit)
		s.wake()
	})
	s.startMu.Lock()
	started := s.started
	s.startMu.Unlock()
	if started {
		<-s.done
	} else {
		// run never started, so nobody else closes the events channel.
		s.startMu.Lock()
		s.started = true // Connect must not start after Close.
		s.startMu.Unlock()
		close(s.events)
		close(s.done)
	}
	return nil
}

func (s *session) wake() {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.wakeClosed || s.wakeW < 0 {
		return
	}
	// EAGAIN means that the pipe already holds a wake-up byte.
	_, _ = unix.Write(s.wakeW, []byte{0})
}

func (s *session) closeWakePipe() {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	s.wakeClosed = true
	_ = unix.Close(s.wakeR)
	_ = unix.Close(s.wakeW)
}

// reportLogin sends the result of the login to Connect. Only the first call has an effect.
func (s *session) reportLogin(err error) {
	if s.loginReported {
		return
	}
	s.loginReported = true
	s.loginResult <- err
}

func (s *session) emit(evt gadu.Event) {
	select {
	case s.events <- evt:
	case <-s.quit:
	}
}

// do runs fn on the owner goroutine and returns its result.
func (s *session) do(ctx context.Context, fn func(gs *C.struct_gg_session) error) error {
	res := make(chan error, 1)
	cmd := func(gs *C.struct_gg_session) { res <- fn(gs) }
	select {
	case s.cmds <- cmd:
	case <-s.done:
		return gadu.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	s.wake()
	select {
	case err := <-res:
		return err
	case <-s.done:
		return gadu.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) run() {
	defer close(s.done)
	defer close(s.events)
	defer s.closeWakePipe()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	log := s.cfg.Log.With().Str("gg_uin", s.cfg.UIN.String()).Logger()

	cPassword := C.CString(s.cfg.Password)
	var cDescr *C.char
	if s.cfg.Description != "" {
		cDescr = C.CString(s.cfg.Description)
	}
	tls := C.int(C.GG_SSL_REQUIRED)
	if s.cfg.TLS == gadu.TLSEnabled {
		tls = C.GG_SSL_ENABLED
	}
	multilogon := C.int(0)
	if s.cfg.Multilogon {
		multilogon = 1
	}
	status := toGGStatus(s.cfg.Status, s.cfg.Description != "")
	gs := C.mg_login(C.uin_t(s.cfg.UIN), cPassword, status, cDescr, tls, multilogon)
	C.memset(unsafe.Pointer(cPassword), 0, C.size_t(len(s.cfg.Password)))
	C.free(unsafe.Pointer(cPassword))
	if cDescr != nil {
		C.free(unsafe.Pointer(cDescr))
	}
	defer s.reportLogin(gadu.ErrClosed)
	if gs == nil {
		log.Warn().Msg("gg_login() failed to start")
		s.reportLogin(&gadu.LoginError{Reason: gadu.FailureInternal})
		return
	}
	defer C.gg_free_session(gs)

	connected := false

	var deadline, nextPing time.Time
	updateDeadline := func() {
		if t := int(C.mg_timeout(gs)); t > 0 {
			deadline = time.Now().Add(time.Duration(t) * time.Second)
		} else {
			deadline = time.Time{}
		}
	}
	updateDeadline()

	for {
		select {
		case <-s.quit:
			if connected {
				C.gg_logoff(gs)
			}
			return
		default:
		}

		now := time.Now()
		wait := -1
		for _, t := range []time.Time{deadline, nextPing} {
			if t.IsZero() {
				continue
			}
			ms := max(int(t.Sub(now).Milliseconds()), 0)
			if wait < 0 || ms < wait {
				wait = ms
			}
		}
		var pollEvents int16
		check := int(C.mg_check(gs))
		if check&C.GG_CHECK_READ != 0 {
			pollEvents |= unix.POLLIN
		}
		if check&C.GG_CHECK_WRITE != 0 {
			pollEvents |= unix.POLLOUT
		}
		fds := []unix.PollFd{
			{Fd: int32(C.mg_fd(gs)), Events: pollEvents},
			{Fd: int32(s.wakeR), Events: unix.POLLIN},
		}
		_, err := unix.Poll(fds, wait)
		if err != nil && !errors.Is(err, unix.EINTR) {
			log.Err(err).Msg("poll() failed")
			if connected {
				s.emit(&gadu.DisconnectedEvent{})
			} else {
				s.reportLogin(&gadu.LoginError{Reason: gadu.FailureInternal})
			}
			return
		}

		if fds[1].Revents != 0 {
			var buf [64]byte
			for {
				if n, _ := unix.Read(s.wakeR, buf[:]); n <= 0 {
					break
				}
			}
		drain:
			for {
				select {
				case cmd := <-s.cmds:
					cmd(gs)
				default:
					break drain
				}
			}
		}

		now = time.Now()
		fdReady := fds[0].Revents != 0
		timedOut := !deadline.IsZero() && !now.Before(deadline)
		if fdReady || timedOut {
			if timedOut && !fdReady {
				// libgadu treats a zero timeout as "the time for this state ran out".
				C.mg_set_timeout(gs, 0)
			}
			ge := C.gg_watch_fd(gs)
			if ge == nil {
				if connected {
					log.Debug().Msg("Connection lost")
					s.emit(&gadu.DisconnectedEvent{})
				} else {
					reason := gadu.FailureConnecting
					if timedOut {
						reason = gadu.FailureTimeout
					}
					s.reportLogin(&gadu.LoginError{Reason: reason})
				}
				return
			}
			end := s.handleEvent(gs, ge, &connected, &nextPing, &log)
			C.gg_event_free(ge)
			if end {
				return
			}
			updateDeadline()
		}

		if connected && !nextPing.IsZero() && !time.Now().Before(nextPing) {
			C.gg_ping(gs)
			nextPing = time.Now().Add(pingInterval)
		}
	}
}

// handleEvent converts a libgadu event. It returns true when the session ends.
func (s *session) handleEvent(gs *C.struct_gg_session, ge *C.struct_gg_event, connected *bool, nextPing *time.Time, log *zerolog.Logger) bool {
	switch ge._type {
	case C.GG_EVENT_NONE, C.GG_EVENT_PONG, C.GG_EVENT_PONG110:
	case C.GG_EVENT_CONN_SUCCESS:
		*connected = true
		*nextPing = time.Now().Add(pingInterval)
		// The server delivers stored offline messages after it receives the contact list.
		C.gg_notify(gs, nil, 0)
		log.Debug().Msg("Logged in")
		s.reportLogin(nil)
	case C.GG_EVENT_CONN_FAILED:
		reason := failureReason(int(C.mg_failure(ge)))
		log.Debug().Stringer("reason", reason).Msg("Login failed")
		s.reportLogin(&gadu.LoginError{Reason: reason})
		return true
	case C.GG_EVENT_DISCONNECT:
		log.Debug().Msg("Server closed the session")
		s.emit(&gadu.DisconnectedEvent{})
		return true
	case C.GG_EVENT_MSG:
		msg := C.mg_msg(ge)
		if msg.chat_id != 0 || msg.sender == 0 {
			// Conferences are not supported. UIN 0 sends system messages.
			log.Debug().Uint32("sender", uint32(msg.sender)).Uint64("chat_id", uint64(msg.chat_id)).Msg("Ignoring message")
			return false
		}
		s.emit(&gadu.MessageEvent{
			Sender:  gadu.UIN(msg.sender),
			Time:    time.Unix(int64(msg.time), 0),
			Plain:   C.GoString((*C.char)(unsafe.Pointer(msg.message))),
			HTML:    C.GoString(msg.xhtml_message),
			Offline: msg.msgclass&C.GG_CLASS_QUEUED != 0,
		})
	case C.GG_EVENT_MULTILOGON_MSG:
		msg := C.mg_multilogon_msg(ge)
		if msg.chat_id != 0 {
			return false
		}
		// For own messages, libgadu puts the other party in the sender field.
		s.emit(&gadu.OwnMessageEvent{
			Recipient: gadu.UIN(msg.sender),
			Time:      time.Unix(int64(msg.time), 0),
			Plain:     C.GoString((*C.char)(unsafe.Pointer(msg.message))),
			HTML:      C.GoString(msg.xhtml_message),
		})
	case C.GG_EVENT_ACK:
		ack := C.mg_ack(ge)
		s.emit(&gadu.AckEvent{Seq: gadu.Seq(ack.seq), Recipient: gadu.UIN(ack.recipient), State: ackState(int(ack.status))})
	case C.GG_EVENT_ACK110:
		ack := C.mg_ack110(ge)
		s.emit(&gadu.AckEvent{Seq: gadu.Seq(ack.seq), State: gadu.AckSent})
	case C.GG_EVENT_STATUS60:
		st := C.mg_status60(ge)
		s.emit(&gadu.StatusEvent{UIN: gadu.UIN(st.uin), Status: fromGGStatus(int(st.status)), Description: C.GoString(st.descr)})
	case C.GG_EVENT_NOTIFY60:
		for i := 0; ; i++ {
			n := C.mg_notify60_at(ge, C.int(i))
			if n.uin == 0 {
				break
			}
			s.emit(&gadu.StatusEvent{UIN: gadu.UIN(n.uin), Status: fromGGStatus(int(n.status)), Description: C.GoString(n.descr)})
		}
	case C.GG_EVENT_TYPING_NOTIFICATION:
		t := C.mg_typing(ge)
		s.emit(&gadu.TypingEvent{UIN: gadu.UIN(t.uin), Length: int(t.length)})
	case C.GG_EVENT_USERLIST100_REPLY:
		r := C.mg_userlist100(ge)
		switch r._type {
		case C.GG_USERLIST100_REPLY_LIST:
		case C.GG_USERLIST100_REPLY_ACK:
			s.emit(&gadu.ContactListPutEvent{Accepted: true, Version: uint32(r.version)})
			return false
		case C.GG_USERLIST100_REPLY_REJECT:
			s.emit(&gadu.ContactListPutEvent{Accepted: false, Version: uint32(r.version)})
			return false
		default:
			log.Debug().Int("type", int(r._type)).Msg("Ignoring contact list reply")
			return false
		}
		raw := C.GoString(r.reply)
		contacts, err := gadu.ParseContactList(raw)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to parse the contact list")
			return false
		}
		s.emit(&gadu.ContactListEvent{Version: uint32(r.version), Contacts: contacts, Raw: raw})
	case C.GG_EVENT_USERLIST100_VERSION:
		s.emit(&gadu.ContactListVersionEvent{Version: uint32(C.mg_userlist100_version(ge))})
	case C.GG_EVENT_MULTILOGON_INFO:
		info := C.mg_multilogon_info(ge)
		sessions := make([]gadu.MultilogonSession, 0, int(info.count))
		for i := 0; i < int(info.count); i++ {
			ms := C.mg_multilogon_session_at(info, C.int(i))
			id := C.GoBytes(unsafe.Pointer(&ms.id.id[0]), 8)
			sessions = append(sessions, gadu.MultilogonSession{
				ID:        binary.LittleEndian.Uint64(id),
				Name:      C.GoString(ms.name),
				LogonTime: time.Unix(int64(ms.logon_time), 0),
			})
		}
		s.emit(&gadu.MultilogonInfoEvent{Sessions: sessions})
	default:
		log.Trace().Int("type", int(ge._type)).Msg("Ignoring libgadu event")
	}
	return false
}

func (s *session) SendMessage(ctx context.Context, to gadu.UIN, html string) (gadu.Seq, error) {
	var seq gadu.Seq
	err := s.do(ctx, func(gs *C.struct_gg_session) error {
		cHTML := C.CString(html)
		defer C.free(unsafe.Pointer(cHTML))
		ret, err := C.gg_send_message_html(gs, C.GG_CLASS_CHAT, C.uin_t(to), (*C.uchar)(unsafe.Pointer(cHTML)))
		if ret < 0 {
			return fmt.Errorf("gg_send_message_html: %w", err)
		}
		seq = gadu.Seq(ret)
		return nil
	})
	return seq, err
}

func (s *session) SendTyping(ctx context.Context, to gadu.UIN, length int) error {
	return s.do(ctx, func(gs *C.struct_gg_session) error {
		if ret, err := C.gg_typing_notification(gs, C.uin_t(to), C.int(length)); ret < 0 {
			return fmt.Errorf("gg_typing_notification: %w", err)
		}
		return nil
	})
}

func (s *session) SetStatus(ctx context.Context, st gadu.Status, descr string) error {
	return s.do(ctx, func(gs *C.struct_gg_session) error {
		code := toGGStatus(st, descr != "")
		var ret C.int
		var err error
		if descr == "" {
			ret, err = C.gg_change_status(gs, code)
		} else {
			cDescr := C.CString(descr)
			defer C.free(unsafe.Pointer(cDescr))
			ret, err = C.gg_change_status_descr(gs, code, cDescr)
		}
		if ret < 0 {
			return fmt.Errorf("gg_change_status: %w", err)
		}
		return nil
	})
}

func (s *session) Notify(ctx context.Context, contacts []gadu.UIN) error {
	return s.do(ctx, func(gs *C.struct_gg_session) error {
		for _, uin := range contacts {
			if ret, err := C.gg_add_notify(gs, C.uin_t(uin)); ret < 0 {
				return fmt.Errorf("gg_add_notify: %w", err)
			}
		}
		return nil
	})
}

func (s *session) RequestContactList(ctx context.Context) error {
	return s.do(ctx, func(gs *C.struct_gg_session) error {
		ret, err := C.gg_userlist100_request(gs, C.GG_USERLIST100_GET, 0, C.GG_USERLIST100_FORMAT_TYPE_GG100, nil)
		if ret < 0 {
			return fmt.Errorf("gg_userlist100_request: %w", err)
		}
		return nil
	})
}

func (s *session) SetBlocked(ctx context.Context, uin gadu.UIN, blocked bool) error {
	from, to := C.char(C.GG_USER_NORMAL), C.char(C.GG_USER_BLOCKED)
	if !blocked {
		from, to = to, from
	}
	return s.do(ctx, func(gs *C.struct_gg_session) error {
		// The same steps as Pidgin: drop the old entry type, then add the new one.
		C.gg_remove_notify_ex(gs, C.uin_t(uin), from)
		if ret, err := C.gg_add_notify_ex(gs, C.uin_t(uin), to); ret < 0 {
			return fmt.Errorf("gg_add_notify_ex: %w", err)
		}
		return nil
	})
}

func (s *session) PutContactList(ctx context.Context, version uint32, data string) error {
	cdata := C.CString(data)
	defer C.free(unsafe.Pointer(cdata))
	return s.do(ctx, func(gs *C.struct_gg_session) error {
		ret, err := C.gg_userlist100_request(gs, C.GG_USERLIST100_PUT, C.uint(version), C.GG_USERLIST100_FORMAT_TYPE_GG100, cdata)
		if ret < 0 {
			return fmt.Errorf("gg_userlist100_request: %w", err)
		}
		return nil
	})
}

func failureReason(f int) gadu.FailureReason {
	switch f {
	case C.GG_FAILURE_RESOLVING:
		return gadu.FailureResolving
	case C.GG_FAILURE_CONNECTING:
		return gadu.FailureConnecting
	case C.GG_FAILURE_INVALID:
		return gadu.FailureInvalid
	case C.GG_FAILURE_READING:
		return gadu.FailureReading
	case C.GG_FAILURE_WRITING:
		return gadu.FailureWriting
	case C.GG_FAILURE_PASSWORD:
		return gadu.FailurePassword
	case C.GG_FAILURE_TLS:
		return gadu.FailureTLS
	case C.GG_FAILURE_NEED_EMAIL:
		return gadu.FailureNeedEmail
	case C.GG_FAILURE_INTRUDER:
		return gadu.FailureIntruder
	case C.GG_FAILURE_UNAVAILABLE:
		return gadu.FailureUnavailable
	case C.GG_FAILURE_PROXY:
		return gadu.FailureProxy
	case C.GG_FAILURE_HUB:
		return gadu.FailureHub
	case C.GG_FAILURE_INTERNAL:
		return gadu.FailureInternal
	}
	return gadu.FailureUnknown
}

func ackState(status int) gadu.AckState {
	switch status {
	case C.GG_ACK_DELIVERED:
		return gadu.AckDelivered
	case C.GG_ACK_QUEUED:
		return gadu.AckQueued
	case C.GG_ACK_BLOCKED:
		return gadu.AckBlocked
	case C.GG_ACK_MBOXFULL:
		return gadu.AckMailboxFull
	case C.GG_ACK_NOT_DELIVERED:
		return gadu.AckNotDelivered
	}
	return gadu.AckUnknown
}

func toGGStatus(st gadu.Status, withDescr bool) C.int {
	pick := func(plain, descr C.int) C.int {
		if withDescr {
			return descr
		}
		return plain
	}
	switch st {
	case gadu.StatusFreeForChat:
		return pick(C.GG_STATUS_FFC, C.GG_STATUS_FFC_DESCR)
	case gadu.StatusBusy:
		return pick(C.GG_STATUS_BUSY, C.GG_STATUS_BUSY_DESCR)
	case gadu.StatusDND:
		return pick(C.GG_STATUS_DND, C.GG_STATUS_DND_DESCR)
	case gadu.StatusInvisible:
		return pick(C.GG_STATUS_INVISIBLE, C.GG_STATUS_INVISIBLE_DESCR)
	case gadu.StatusOffline:
		return pick(C.GG_STATUS_NOT_AVAIL, C.GG_STATUS_NOT_AVAIL_DESCR)
	}
	return pick(C.GG_STATUS_AVAIL, C.GG_STATUS_AVAIL_DESCR)
}

func fromGGStatus(code int) gadu.Status {
	switch code &^ (C.GG_STATUS_DESCR_MASK | C.GG_STATUS_FRIENDS_MASK | C.GG_STATUS_IMAGE_MASK) {
	case C.GG_STATUS_AVAIL, C.GG_STATUS_AVAIL_DESCR:
		return gadu.StatusAvailable
	case C.GG_STATUS_FFC, C.GG_STATUS_FFC_DESCR:
		return gadu.StatusFreeForChat
	case C.GG_STATUS_BUSY, C.GG_STATUS_BUSY_DESCR:
		return gadu.StatusBusy
	case C.GG_STATUS_DND, C.GG_STATUS_DND_DESCR:
		return gadu.StatusDND
	case C.GG_STATUS_INVISIBLE, C.GG_STATUS_INVISIBLE_DESCR:
		return gadu.StatusInvisible
	case C.GG_STATUS_BLOCKED:
		return gadu.StatusBlocked
	}
	return gadu.StatusOffline
}

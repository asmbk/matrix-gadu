// Package gadu is the Go API for the Gadu-Gadu (GG) network.
//
// The package has no Matrix code. Implementations of [Session] live in
// sub-packages: libgadu (cgo) and fake (in-memory, for tests).
package gadu

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// UIN is a GG account number.
type UIN uint32

func (u UIN) String() string {
	return strconv.FormatUint(uint64(u), 10)
}

// ParseUIN parses a GG number. It accepts an optional "gg:" prefix and
// surrounding white space.
func ParseUIN(s string) (UIN, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "gg:")
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("invalid GG number %q", s)
	}
	return UIN(n), nil
}

// Seq is the sequence number of a sent message.
type Seq uint32

// TLSMode selects how strictly the session checks the TLS connection.
type TLSMode int

const (
	// TLSRequired rejects connections with a bad server certificate.
	TLSRequired TLSMode = iota
	// TLSEnabled uses TLS but accepts a bad server certificate.
	TLSEnabled
)

// Config holds the parameters of one GG session.
type Config struct {
	UIN         UIN
	Password    string
	Status      Status
	Description string
	TLS         TLSMode
	// Multilogon lets other GG sessions of the same account stay online.
	Multilogon bool
	// Log receives protocol-level log lines. It never receives message text or passwords.
	Log zerolog.Logger
}

// Session is one connection to the GG servers.
//
// A Session is used once: Connect logs in, Events delivers events until the
// connection ends, and then the Events channel closes. To reconnect, create a
// new Session.
type Session interface {
	// Connect logs in and blocks until the login succeeds or fails.
	// A failed login returns a *LoginError.
	Connect(ctx context.Context) error
	// Close logs off and frees the session. It is safe to call more than once.
	Close() error
	// Events returns the event channel. The channel closes when the session ends.
	Events() <-chan Event
	// SendMessage sends a message in the GG HTML format and returns its sequence number.
	SendMessage(ctx context.Context, to UIN, html string) (Seq, error)
	// SendTyping sends a typing notification. A length of 0 means "stopped typing".
	SendTyping(ctx context.Context, to UIN, length int) error
	// SetStatus changes the status and the status text of the account.
	SetStatus(ctx context.Context, s Status, descr string) error
	// Notify subscribes to the status of the given contacts.
	Notify(ctx context.Context, contacts []UIN) error
	// SetBlocked tells the server to block or unblock messages from a number
	// for this session. Blocks must be sent again after each login.
	SetBlocked(ctx context.Context, uin UIN, blocked bool) error
	// RequestContactList asks for the server-side contact list.
	// The result arrives as a *ContactListEvent.
	RequestContactList(ctx context.Context) error
	// PutContactList replaces the server-side contact list (GG 10 format).
	// The version must be the version of the list that the edit started
	// from. The result arrives as a *ContactListPutEvent.
	PutContactList(ctx context.Context, version uint32, data string) error
}

// Factory creates a new, unconnected session.
type Factory func(cfg Config) Session

// ErrClosed is returned by session methods after the session ended.
var ErrClosed = errors.New("gadu: session closed")

// FailureReason tells why a login failed.
type FailureReason int

const (
	FailureUnknown FailureReason = iota
	FailureResolving
	FailureConnecting
	FailureInvalid
	FailureReading
	FailureWriting
	FailurePassword
	FailureTLS
	FailureNeedEmail
	FailureIntruder
	FailureUnavailable
	FailureProxy
	FailureHub
	FailureInternal
	FailureTimeout
)

var failureNames = map[FailureReason]string{
	FailureUnknown:     "unknown error",
	FailureResolving:   "cannot resolve the server address",
	FailureConnecting:  "cannot connect to the server",
	FailureInvalid:     "the server sent invalid data",
	FailureReading:     "the connection broke while reading",
	FailureWriting:     "the connection broke while writing",
	FailurePassword:    "incorrect password",
	FailureTLS:         "TLS negotiation failed",
	FailureNeedEmail:   "the server asks to set an e-mail address",
	FailureIntruder:    "too many attempts with an incorrect password",
	FailureUnavailable: "the GG servers are unavailable",
	FailureProxy:       "proxy error",
	FailureHub:         "cannot connect to the GG hub",
	FailureInternal:    "internal libgadu error",
	FailureTimeout:     "login timed out",
}

func (r FailureReason) String() string {
	if s, ok := failureNames[r]; ok {
		return s
	}
	return fmt.Sprintf("failure %d", int(r))
}

// LoginError is returned by Session.Connect when the login fails.
type LoginError struct {
	Reason FailureReason
}

func (e *LoginError) Error() string {
	return "GG login failed: " + e.Reason.String()
}

// BadCredentials tells if a retry with the same password cannot succeed.
func (e *LoginError) BadCredentials() bool {
	return e.Reason == FailurePassword || e.Reason == FailureIntruder || e.Reason == FailureNeedEmail
}

// Event is one of the *XxxEvent types below.
type Event interface {
	isEvent()
}

// DisconnectedEvent means that the server closed the session.
// Another client that logs in to the same account can cause it.
type DisconnectedEvent struct{}

// MessageEvent is an incoming message from a contact.
type MessageEvent struct {
	Sender UIN
	Time   time.Time
	Plain  string
	HTML   string
	// Offline is true if the server stored the message while the account was offline.
	Offline bool
}

// OwnMessageEvent is a message that the account owner sent from another GG session (multilogon).
type OwnMessageEvent struct {
	Recipient UIN
	Time      time.Time
	Plain     string
	HTML      string
}

// AckState is the delivery state of a sent message.
type AckState int

const (
	AckUnknown AckState = iota
	// AckSent means that the server accepted the message.
	AckSent
	AckDelivered
	AckQueued
	AckBlocked
	AckMailboxFull
	AckNotDelivered
)

// AckEvent reports the delivery state of a sent message.
type AckEvent struct {
	Seq       Seq
	Recipient UIN
	State     AckState
}

// StatusEvent reports the status of a contact.
type StatusEvent struct {
	UIN         UIN
	Status      Status
	Description string
}

// TypingEvent reports that a contact types. A length of 0 means "stopped typing".
type TypingEvent struct {
	UIN    UIN
	Length int
}

// ContactListPutEvent is the answer to PutContactList.
type ContactListPutEvent struct {
	// Accepted is false if the list changed on the server since the edit started.
	Accepted bool
	// Version is the new version, or the current server version after a rejection.
	Version uint32
}

// ContactListVersionEvent means that the contact list changed on the server,
// for example after an edit in another GG client.
type ContactListVersionEvent struct {
	Version uint32
}

// ContactListEvent carries the server-side contact list.
type ContactListEvent struct {
	Version  uint32
	Contacts []Contact
	// Raw is the list as the server sent it. Edits start from it, so that
	// fields that the bridge does not read (groups, phone numbers) stay.
	Raw string
}

// MultilogonSession describes another active session of the account.
type MultilogonSession struct {
	ID        uint64
	Name      string
	LogonTime time.Time
}

// MultilogonInfoEvent lists the other active sessions of the account.
type MultilogonInfoEvent struct {
	Sessions []MultilogonSession
}

func (*DisconnectedEvent) isEvent()       {}
func (*MessageEvent) isEvent()            {}
func (*OwnMessageEvent) isEvent()         {}
func (*AckEvent) isEvent()                {}
func (*StatusEvent) isEvent()             {}
func (*TypingEvent) isEvent()             {}
func (*ContactListEvent) isEvent()        {}
func (*ContactListPutEvent) isEvent()     {}
func (*ContactListVersionEvent) isEvent() {}
func (*MultilogonInfoEvent) isEvent()     {}

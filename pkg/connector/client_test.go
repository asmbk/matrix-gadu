package connector

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
	"github.com/asmbk/matrix-gadu/pkg/gadu/fake"
)

const (
	selfUIN    gadu.UIN = 7654321
	contactUIN gadu.UIN = 1234567
)

type presenceCall struct {
	uin    gadu.UIN
	status contactStatus
}

type recorder struct {
	mu        sync.Mutex
	states    []status.BridgeState
	events    []bridgev2.RemoteEvent
	presences []presenceCall
	notify    chan struct{}
}

func (r *recorder) state(s status.BridgeState) {
	r.mu.Lock()
	r.states = append(r.states, s)
	r.mu.Unlock()
	r.ping()
}

func (r *recorder) presence(_ context.Context, uin gadu.UIN, s contactStatus) error {
	r.mu.Lock()
	r.presences = append(r.presences, presenceCall{uin, s})
	r.mu.Unlock()
	r.ping()
	return nil
}

func (r *recorder) event(e bridgev2.RemoteEvent) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	r.ping()
}

// resyncs returns the queued ChatResync events. The caller holds r.mu.
func (r *recorder) resyncs() []*simplevent.ChatResync {
	var out []*simplevent.ChatResync
	for _, e := range r.events {
		if c, ok := e.(*simplevent.ChatResync); ok {
			out = append(out, c)
		}
	}
	return out
}

// statusChanges returns the queued topic or name changes. The caller holds r.mu.
func (r *recorder) statusChanges() []*bridgev2.ChatInfo {
	var out []*bridgev2.ChatInfo
	for _, e := range r.events {
		if c, ok := e.(*simplevent.ChatInfoChange); ok && (c.ChatInfoChange.ChatInfo.Topic != nil || c.ChatInfoChange.ChatInfo.Name != nil) {
			out = append(out, c.ChatInfoChange.ChatInfo)
		}
	}
	return out
}

// avatarChanges returns the queued room avatar changes. The caller holds r.mu.
func (r *recorder) avatarChanges() []*simplevent.ChatInfoChange {
	var out []*simplevent.ChatInfoChange
	for _, e := range r.events {
		if c, ok := e.(*simplevent.ChatInfoChange); ok && c.ChatInfoChange.ChatInfo.Avatar != nil {
			out = append(out, c)
		}
	}
	return out
}

func (r *recorder) ping() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// waitFor polls cond until it is true or the test times out.
func (r *recorder) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		r.mu.Lock()
		ok := cond()
		r.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-r.notify:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (r *recorder) hasState(s status.BridgeStateEvent) func() bool {
	return func() bool {
		for _, st := range r.states {
			if st.StateEvent == s {
				return true
			}
		}
		return false
	}
}

func newTestClient(t *testing.T, net *fake.Network) (*GaduClient, *recorder) {
	t.Helper()
	oldDelays, oldWait := reconnectDelays, conflictWait
	reconnectDelays = []time.Duration{time.Millisecond}
	conflictWait = time.Millisecond
	t.Cleanup(func() { reconnectDelays, conflictWait = oldDelays, oldWait })

	main := &GaduConnector{NewSession: net.Factory()}
	main.Config.DisplaynameTemplate = "{{or .ShowName .UIN}}"
	if err := main.Config.PostProcess(); err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       makeUserLoginID(selfUIN),
			Metadata: &UserLoginMetadata{Password: "secret", StatusText: "hello"},
		},
		Log: zerolog.Nop(),
	}
	rec := &recorder{notify: make(chan struct{}, 1)}
	client := newClient(main, login, selfUIN)
	client.sendState = rec.state
	client.queueEvent = rec.event
	client.existingGhost = func(context.Context, networkid.UserID) (*bridgev2.Ghost, error) { return nil, nil }
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return false }
	client.setPresence = rec.presence
	client.roomPeers = func(context.Context) []gadu.UIN { return nil }
	client.fetchAvatar = func(context.Context, gadu.UIN) (*gadu.Avatar, error) { return nil, nil }
	client.avatarFetchInterval = time.Millisecond
	client.fetchMedia = func(context.Context, gadu.MediaLink) (*gadu.RemoteMedia, error) { return nil, gadu.ErrNotDownloadable }
	client.publicMediaURL = func(context.Context, *event.MessageEventContent) (string, error) { return "", nil }
	client.downloadMedia = func(context.Context, *event.MessageEventContent) ([]byte, error) {
		return nil, errors.New("no media in tests")
	}
	t.Cleanup(client.Disconnect)
	return client, rec
}

func waitSession(t *testing.T, net *fake.Network) *fake.Session {
	t.Helper()
	select {
	case s := <-net.Connected():
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a GG session")
		return nil
	}
}

func TestConnectAndReceiveMessage(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN, ShowName: "Kuba"}}
	client, rec := newTestClient(t, net)

	client.Connect(context.Background())
	sess := waitSession(t, net)
	if sess.Config.Password != "secret" || sess.Config.Description != "hello" || sess.Config.Multilogon {
		t.Errorf("unexpected session config %+v", sess.Config)
	}
	rec.waitFor(t, "connected state", rec.hasState(status.StateConnected))
	rec.waitFor(t, "contact list", func() bool { _, ok := client.contact(contactUIN); return ok })
	if name := *client.userInfo(contactUIN).Name; name != "Kuba" {
		t.Errorf("ghost name = %q", name)
	}
	if name := *client.userInfo(42).Name; name != "42" {
		t.Errorf("unknown ghost name = %q", name)
	}

	sentAt := time.Unix(1760000000, 0)
	sess.Inject(&gadu.MessageEvent{Sender: contactUIN, Time: sentAt, Plain: "hi\r\nthere", HTML: ggSpanOpen + "hi<br>there</span>"})
	rec.waitFor(t, "message event", func() bool { return len(rec.events) == 1 })

	msg, ok := rec.events[0].(*simplevent.Message[*gadu.MessageEvent])
	if !ok {
		t.Fatalf("event type %T", rec.events[0])
	}
	wantKey := networkid.PortalKey{ID: "1234567", Receiver: "7654321"}
	if msg.PortalKey != wantKey || msg.Sender.Sender != "1234567" || msg.Sender.IsFromMe || !msg.CreatePortal || !msg.Timestamp.Equal(sentAt) {
		t.Errorf("unexpected event meta %+v", msg.EventMeta)
	}
	converted, err := msg.ConvertMessageFunc(context.Background(), nil, nil, msg.Data)
	if err != nil {
		t.Fatal(err)
	}
	if body := converted.Parts[0].Content.Body; body != "hi\nthere" {
		t.Errorf("body = %q", body)
	}
}

func TestOwnMessageIsFromMe(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	sess := waitSession(t, net)
	sess.Inject(&gadu.OwnMessageEvent{Recipient: contactUIN, Time: time.Now(), Plain: "sent from phone"})
	rec.waitFor(t, "own message", func() bool { return len(rec.events) == 1 })
	msg := rec.events[0].(*simplevent.Message[*gadu.OwnMessageEvent])
	if !msg.Sender.IsFromMe || msg.Sender.SenderLogin != "7654321" || msg.PortalKey.ID != "1234567" {
		t.Errorf("unexpected own message meta %+v", msg.EventMeta)
	}
}

func TestBadCredentialsStopsRetries(t *testing.T) {
	net := fake.NewNetwork()
	net.LoginErr = &gadu.LoginError{Reason: gadu.FailurePassword}
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	rec.waitFor(t, "bad credentials state", rec.hasState(status.StateBadCredentials))
	time.Sleep(20 * time.Millisecond)
	if n := len(net.Sessions); n != 1 {
		t.Errorf("created %d sessions, want 1", n)
	}
}

func TestReconnectAfterNetworkError(t *testing.T) {
	net := fake.NewNetwork()
	net.LoginErr = &gadu.LoginError{Reason: gadu.FailureConnecting}
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	rec.waitFor(t, "transient disconnect", rec.hasState(status.StateTransientDisconnect))
	net.SetLoginErr(nil)
	waitSession(t, net)
	rec.waitFor(t, "connected state", rec.hasState(status.StateConnected))
}

func TestReconnectAfterServerDisconnect(t *testing.T) {
	net := fake.NewNetwork()
	client, _ := newTestClient(t, net)
	client.Connect(context.Background())
	first := waitSession(t, net)
	first.Disconnect()
	second := waitSession(t, net)
	if first == second {
		t.Fatal("the client reused the closed session")
	}
	if !client.IsLoggedIn() {
		// The flag is set right after Connect returns. Allow the loop to get there.
		time.Sleep(20 * time.Millisecond)
	}
	if !client.IsLoggedIn() {
		t.Error("client is not logged in after reconnect")
	}
}

func TestRecordDisconnectDetectsConflict(t *testing.T) {
	client := &GaduClient{}
	now := time.Now()
	if client.recordDisconnect(now) || client.recordDisconnect(now.Add(time.Minute)) {
		t.Fatal("conflict after two disconnects")
	}
	if !client.recordDisconnect(now.Add(2 * time.Minute)) {
		t.Fatal("no conflict after three disconnects in 10 minutes")
	}
	if client.recordDisconnect(now.Add(30*time.Minute)) || client.recordDisconnect(now.Add(45*time.Minute)) {
		t.Fatal("conflict for disconnects far apart")
	}
}

func TestHandleMatrixMessage(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	waitSession(t, net)
	rec.waitFor(t, "connected state", rec.hasState(status.StateConnected))

	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: client.makePortalKey(contactUIN)}}
	msg := &bridgev2.MatrixMessage{}
	msg.Portal = portal
	msg.Content = &event.MessageEventContent{MsgType: event.MsgText, Body: "a\nb"}
	resp, err := client.HandleMatrixMessage(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	sent := net.SentMessages()
	if len(sent) != 1 || sent[0].To != contactUIN || sent[0].HTML != ggSpanOpen+"a<br>b</span>" {
		t.Fatalf("sent = %+v", sent)
	}
	if resp.DB.SenderID != "7654321" || resp.DB.ID == "" {
		t.Errorf("unexpected response %+v", resp.DB)
	}

	msg.Content = &event.MessageEventContent{MsgType: event.MsgImage, Body: "x.png"}
	if _, err := client.HandleMatrixMessage(context.Background(), msg); err == nil {
		t.Error("image message did not fail")
	}
}

func TestDeliveryAckBecomesReceipt(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	sess := waitSession(t, net)
	rec.waitFor(t, "connected state", rec.hasState(status.StateConnected))

	msg := &bridgev2.MatrixMessage{}
	msg.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: client.makePortalKey(contactUIN)}}
	msg.Content = &event.MessageEventContent{MsgType: event.MsgText, Body: "ping"}
	resp, err := client.HandleMatrixMessage(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	seq := net.SentMessages()[0].Seq
	sess.Inject(&gadu.AckEvent{Seq: seq, State: gadu.AckSent})
	sess.Inject(&gadu.AckEvent{Seq: seq, Recipient: contactUIN, State: gadu.AckDelivered})
	rec.waitFor(t, "delivery receipt", func() bool { return len(rec.events) == 1 })
	receipt := rec.events[0].(*simplevent.Receipt)
	if receipt.Type != bridgev2.RemoteEventDeliveryReceipt || len(receipt.Targets) != 1 || receipt.Targets[0] != resp.DB.ID {
		t.Errorf("unexpected receipt %+v", receipt)
	}
}

func TestContactRoomsCreatesMissingRooms(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN, ShowName: "Kuba"}, {UIN: 42}, {UIN: selfUIN}}
	client, rec := newTestClient(t, net)
	client.main.Config.ContactRooms = true
	client.contactRoomInterval = time.Millisecond
	client.hasRoom = func(_ context.Context, key networkid.PortalKey) bool { return key.ID == "42" }

	client.Connect(context.Background())
	waitSession(t, net)
	rec.waitFor(t, "chat resync", func() bool { return len(rec.resyncs()) >= 1 })
	time.Sleep(50 * time.Millisecond)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if n := len(rec.resyncs()); n != 1 {
		t.Fatalf("got %d room creations, want 1 (own number and existing room skipped)", n)
	}
	resync := rec.resyncs()[0]
	if resync.ChatInfo.Members.PowerLevels != contactRoomPowerLevels {
		t.Error("new room does not let the user change the room picture")
	}
	if !resync.CreatePortal || resync.PortalKey != client.makePortalKey(contactUIN) || resync.ChatInfo.Members.OtherUserID != "1234567" {
		t.Errorf("unexpected resync %+v", resync.EventMeta)
	}
}

func TestContactRoomsOff(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN}}
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	waitSession(t, net)
	rec.waitFor(t, "contact list", func() bool { _, ok := client.contact(contactUIN); return ok })
	time.Sleep(50 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Errorf("got %d events, want none", len(rec.events))
	}
}

func TestSearchUsers(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{
		{UIN: contactUIN, ShowName: "Kuba", FirstName: "Jakub", LastName: "Kowalski"},
		{UIN: 4321, ShowName: "Marcin Źdźbło"},
	}
	client, _ := newTestClient(t, net)
	client.Connect(context.Background())
	waitSession(t, net)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := client.contact(4321); ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for query, want := range map[string][]networkid.UserID{
		"kowal":  {"1234567"},
		"ŹDŹB":   {"4321"},
		"432":    {"4321"},
		"nobody": {},
		"  ":     {},
	} {
		res, err := client.SearchUsers(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		got := []networkid.UserID{}
		for _, r := range res {
			got = append(got, r.UserID)
		}
		if len(got) != len(want) || (len(want) > 0 && got[0] != want[0]) {
			t.Errorf("SearchUsers(%q) = %v, want %v", query, got, want)
		}
	}
}

func TestStatusSetsPresenceAndTopic(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN, ShowName: "Kuba"}}
	client, rec := newTestClient(t, net)
	client.main.Config.StatusInTopic = true
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	client.Connect(context.Background())
	sess := waitSession(t, net)

	sess.Inject(&gadu.StatusEvent{UIN: selfUIN, Status: gadu.StatusAvailable})
	sess.Inject(&gadu.StatusEvent{UIN: contactUIN, Status: gadu.StatusBusy, Description: "w pracy"})
	sess.Inject(&gadu.StatusEvent{UIN: contactUIN, Status: gadu.StatusBusy, Description: "w pracy"})
	rec.waitFor(t, "presence and topic", func() bool { return len(rec.presences) >= 1 && len(rec.statusChanges()) >= 1 })
	time.Sleep(50 * time.Millisecond)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.presences) != 1 || rec.presences[0].uin != contactUIN || rec.presences[0].status.Status != gadu.StatusBusy {
		t.Fatalf("presences = %+v (own status and repeats must be skipped)", rec.presences)
	}
	changes := rec.statusChanges()
	if len(changes) != 1 {
		t.Fatalf("status changes = %d, want 1 (repeat skipped)", len(changes))
	}
	if topic := *changes[0].Topic; topic != "🟡 Busy – w pracy" {
		t.Errorf("topic = %q", topic)
	}
	for _, e := range rec.events {
		if s := e.(*simplevent.ChatInfoChange).Sender.Sender; s != "" {
			t.Errorf("room change sender = %q, want the bot (empty sender)", s)
		}
	}
	if info := client.dmChatInfo(contactUIN, true); info.Topic == nil || *info.Topic != "🟡 Busy – w pracy" {
		t.Errorf("new room topic = %v", info.Topic)
	}
}

func TestStatusTopicOff(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	client.Connect(context.Background())
	sess := waitSession(t, net)
	sess.Inject(&gadu.StatusEvent{UIN: contactUIN, Status: gadu.StatusAvailable})
	rec.waitFor(t, "presence", func() bool { return len(rec.presences) == 1 })
	time.Sleep(30 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Errorf("got %d events with status_in_topic off", len(rec.events))
	}
	if client.dmChatInfo(contactUIN, true).Topic != nil {
		t.Error("new room gets a topic with status_in_topic off")
	}
}

func TestPresenceRefreshAndClearOnDisconnect(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.presenceRefreshInterval = 20 * time.Millisecond
	client.Connect(context.Background())
	sess := waitSession(t, net)
	sess.Inject(&gadu.StatusEvent{UIN: contactUIN, Status: gadu.StatusAvailable})
	sess.Inject(&gadu.StatusEvent{UIN: 42, Status: gadu.StatusOffline})
	rec.waitFor(t, "refresh", func() bool {
		n := 0
		for _, p := range rec.presences {
			if p.uin == contactUIN {
				n++
			}
		}
		return n >= 3
	})
	for _, p := range rec.presences {
		if p.uin == 42 && p.status.Status != gadu.StatusOffline {
			t.Errorf("offline contact refreshed as %v", p.status.Status)
		}
	}
	sess.Disconnect()
	rec.waitFor(t, "offline after disconnect", func() bool {
		last := rec.presences[len(rec.presences)-1]
		return last.uin == contactUIN && last.status.Status == gadu.StatusOffline
	})
	if _, ok := client.status(contactUIN); ok {
		t.Error("status kept after disconnect")
	}
}

func TestStatusLines(t *testing.T) {
	client := newClient(&GaduConnector{}, &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "7654321"}}, selfUIN)
	client.main.Config.DisplaynameTemplate = "{{or .ShowName .UIN}}"
	if err := client.main.Config.PostProcess(); err != nil {
		t.Fatal(err)
	}
	client.contacts = map[gadu.UIN]gadu.Contact{
		1: {UIN: 1, ShowName: "Zenon"},
		2: {UIN: 2, ShowName: "adam_b"},
		3: {UIN: 3, ShowName: "Basia"},
	}
	client.statuses = map[gadu.UIN]contactStatus{
		1:   {Status: gadu.StatusAvailable},
		2:   {Status: gadu.StatusBusy, Description: "*praca*"},
		777: {Status: gadu.StatusAvailable},
	}
	got := client.statusLines("")
	want := []string{
		"- 777 (777): 🟢 Available",
		`- adam\_b (2): 🟡 Busy – \*praca\*`,
		"- Zenon (1): 🟢 Available",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("online lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := client.statusLines("basia"); len(got) != 1 || got[0] != "- Basia (3): ⚫ Offline" {
		t.Errorf("query lines = %q", got)
	}
}

func TestSubscribeContactsRoomsAndNewChats(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN}, {UIN: selfUIN}}
	client, rec := newTestClient(t, net)
	client.roomPeers = func(context.Context) []gadu.UIN { return []gadu.UIN{777, contactUIN} }
	client.Connect(context.Background())
	sess := waitSession(t, net)
	rec.waitFor(t, "subscription", func() bool { return len(net.NotifiedUINs()) >= 2 })

	sess.Inject(&gadu.MessageEvent{Sender: 4242, Time: time.Now(), Plain: "hi"})
	rec.waitFor(t, "message", func() bool { return len(rec.events) == 1 })
	sess.Inject(&gadu.MessageEvent{Sender: 4242, Time: time.Now().Add(time.Second), Plain: "again"})
	rec.waitFor(t, "second message", func() bool { return len(rec.events) == 2 })
	if _, err := client.CreateChatWithGhost(context.Background(), &bridgev2.Ghost{Ghost: &database.Ghost{ID: "5555"}}); err != nil {
		t.Fatal(err)
	}

	got := map[gadu.UIN]int{}
	for _, u := range net.NotifiedUINs() {
		got[u]++
	}
	want := map[gadu.UIN]int{contactUIN: 1, 777: 1, 4242: 1, 5555: 1}
	if len(got) != len(want) {
		t.Fatalf("notified = %v, want %v", got, want)
	}
	for u, n := range want {
		if got[u] != n {
			t.Errorf("number %d subscribed %d times, want %d (all: %v)", u, got[u], n, got)
		}
	}
}

func TestStatusInName(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN, ShowName: "Kuba"}}
	client, rec := newTestClient(t, net)
	client.main.Config.StatusInName = true
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	client.Connect(context.Background())
	sess := waitSession(t, net)
	rec.waitFor(t, "contact list", func() bool { _, ok := client.contact(contactUIN); return ok })

	sess.Inject(&gadu.StatusEvent{UIN: contactUIN, Status: gadu.StatusDND, Description: "spotkanie"})
	rec.waitFor(t, "name change", func() bool { return len(rec.statusChanges()) >= 1 })
	rec.mu.Lock()
	info := rec.statusChanges()[0]
	rec.mu.Unlock()
	if info.Name == nil || *info.Name != "🔴 Kuba" || info.Topic != nil {
		t.Errorf("name = %v, topic = %v; want name only", info.Name, info.Topic)
	}
	if got := client.dmChatInfo(contactUIN, true); got.Name == nil || *got.Name != "🔴 Kuba" {
		t.Errorf("new room name = %v", got.Name)
	}

	// A renamed contact gets the new name with the same emoji after the list reloads.
	sess.Inject(&gadu.ContactListEvent{Contacts: []gadu.Contact{{UIN: contactUIN, ShowName: "Jakub"}}})
	rec.waitFor(t, "rename", func() bool { return len(rec.statusChanges()) >= 2 })
	rec.mu.Lock()
	changes := rec.statusChanges()
	rec.mu.Unlock()
	if name := *changes[len(changes)-1].Name; name != "🔴 Jakub" {
		t.Errorf("name after rename = %q", name)
	}
}

func TestStatusInNameAndTopic(t *testing.T) {
	client := newClient(&GaduConnector{}, &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "7654321"}}, selfUIN)
	client.main.Config.DisplaynameTemplate = "{{or .ShowName .UIN}}"
	if err := client.main.Config.PostProcess(); err != nil {
		t.Fatal(err)
	}
	if client.statusChatInfo(1, contactStatus{Status: gadu.StatusAvailable}) != nil {
		t.Error("status info with both options off")
	}
	client.main.Config.StatusInName, client.main.Config.StatusInTopic = true, true
	client.contacts = map[gadu.UIN]gadu.Contact{1: {UIN: 1, ShowName: "Zenon"}}
	client.contactsLoaded = true
	info := client.statusChatInfo(1, contactStatus{Status: gadu.StatusFreeForChat, Description: "hej"})
	if *info.Name != "🟢 Zenon" || *info.Topic != "🟢 Free for chat – hej" || !info.ExcludeChangesFromTimeline {
		t.Errorf("info = name %q topic %q", *info.Name, *info.Topic)
	}
	if name := client.statusRoomName(9, contactStatus{Status: gadu.StatusInvisible}); name != "⚫ 9" {
		t.Errorf("unknown contact name = %q", name)
	}
}

func TestGGAvatarsAndCustomRoomPicture(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactList = []gadu.Contact{{UIN: contactUIN}, {UIN: 42}}
	client, rec := newTestClient(t, net)
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	var mu sync.Mutex
	version := "v1"
	fetched := 0
	client.fetchAvatar = func(_ context.Context, uin gadu.UIN) (*gadu.Avatar, error) {
		mu.Lock()
		defer mu.Unlock()
		fetched++
		if uin == 42 {
			return nil, nil
		}
		return &gadu.Avatar{Data: []byte("jpeg-" + version), Version: version}, nil
	}
	client.Connect(context.Background())
	sess := waitSession(t, net)
	rec.waitFor(t, "avatars", func() bool { return len(rec.avatarChanges()) >= 2 })

	rec.mu.Lock()
	byPeer := map[networkid.PortalID]*bridgev2.Avatar{}
	var first *simplevent.ChatInfoChange
	for _, c := range rec.avatarChanges() {
		byPeer[c.PortalKey.ID] = c.ChatInfoChange.ChatInfo.Avatar
		if c.PortalKey.ID == "1234567" {
			first = c
		}
	}
	rec.mu.Unlock()
	if av := byPeer["1234567"]; av == nil || av.ID != "gg:v1" || av.Remove {
		t.Errorf("contact avatar = %+v", av)
	} else if data, _ := av.Get(context.Background()); string(data) != "jpeg-v1" {
		t.Errorf("avatar data = %q", data)
	}
	if av := byPeer["42"]; av == nil || !av.Remove {
		t.Errorf("no-avatar contact = %+v, want Remove", av)
	}
	if ui := client.userInfo(contactUIN); ui.Avatar == nil || ui.Avatar.ID != "gg:v1" {
		t.Error("ghost info has no GG avatar")
	}

	// A custom room picture blocks the GG avatar for that room.
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: client.makePortalKey(contactUIN), Metadata: &PortalMetadata{}}}
	first.PreHandleFunc(context.Background(), portal)
	if first.ChatInfoChange.ChatInfo.Avatar == nil {
		t.Error("GG avatar dropped for a room without a custom picture")
	}
	msg := &bridgev2.MatrixRoomAvatar{}
	msg.Portal = portal
	msg.Content = &event.RoomAvatarEventContent{URL: "mxc://example.org/custom"}
	if ok, err := client.HandleMatrixRoomAvatar(context.Background(), msg); !ok || err != nil {
		t.Fatalf("HandleMatrixRoomAvatar = %v, %v", ok, err)
	}
	if !hasCustomAvatar(portal) || portal.AvatarMXC != "mxc://example.org/custom" {
		t.Errorf("custom picture not stored: %+v", portal.Portal)
	}
	first.PreHandleFunc(context.Background(), portal)
	if first.ChatInfoChange.ChatInfo.Avatar != nil {
		t.Error("GG avatar not dropped for a room with a custom picture")
	}
	if client.dmChatInfo(contactUIN, false).Avatar != nil {
		t.Error("resync info carries the GG avatar for a custom-picture room")
	}

	// The same version is not applied again. A new version is.
	rec.mu.Lock()
	before := len(rec.avatarChanges())
	rec.mu.Unlock()
	mu.Lock()
	version = "v2"
	mu.Unlock()
	sess.Inject(&gadu.ContactListEvent{Contacts: []gadu.Contact{{UIN: contactUIN}, {UIN: 42}}})
	rec.waitFor(t, "new avatar version", func() bool { return len(rec.avatarChanges()) > before })
	time.Sleep(30 * time.Millisecond)
	rec.mu.Lock()
	after := rec.avatarChanges()[before:]
	rec.mu.Unlock()
	if len(after) != 1 || after[0].PortalKey.ID != "1234567" || after[0].ChatInfoChange.ChatInfo.Avatar.ID != "gg:v2" {
		t.Errorf("changes after reload: %d (want only the new version of 1234567)", len(after))
	}
}

func TestOfflineLowPriority(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.main.Config.OfflineLowPriority = true
	client.hasRoom = func(_ context.Context, key networkid.PortalKey) bool { return key.ID == "42" }
	type call struct {
		uin     gadu.UIN
		offline bool
	}
	var mu sync.Mutex
	var calls []call
	client.setLowPriority = func(_ context.Context, uin gadu.UIN, offline bool) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{uin, offline})
		return nil
	}
	client.Connect(context.Background())
	sess := waitSession(t, net)

	for _, s := range []gadu.Status{gadu.StatusOffline, gadu.StatusInvisible, gadu.StatusAvailable, gadu.StatusBusy, gadu.StatusOffline} {
		sess.Inject(&gadu.StatusEvent{UIN: 42, Status: s})
	}
	// A number without a room gets no tag.
	sess.Inject(&gadu.StatusEvent{UIN: 43, Status: gadu.StatusOffline})
	rec.waitFor(t, "presence", func() bool { return len(rec.presences) >= 6 })
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	want := []call{{42, true}, {42, false}, {42, true}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v (only online/offline changes, rooms only)", calls, want)
	}
}

func TestOfflineLowPriorityOff(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	called := make(chan struct{}, 1)
	client.setLowPriority = func(context.Context, gadu.UIN, bool) error {
		called <- struct{}{}
		return nil
	}
	client.Connect(context.Background())
	sess := waitSession(t, net)
	sess.Inject(&gadu.StatusEvent{UIN: 42, Status: gadu.StatusOffline})
	rec.waitFor(t, "presence", func() bool { return len(rec.presences) >= 1 })
	select {
	case <-called:
		t.Error("tag changed with offline_low_priority off")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOfflineLowPriorityMarksSilentPeers(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.main.Config.OfflineLowPriority = true
	client.offlineSweepDelay = 100 * time.Millisecond
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	client.roomPeers = func(context.Context) []gadu.UIN { return []gadu.UIN{42, 43, selfUIN} }
	tagged := make(chan gadu.UIN, 10)
	client.setLowPriority = func(_ context.Context, uin gadu.UIN, offline bool) error {
		if offline {
			tagged <- uin
		}
		return nil
	}
	client.Connect(context.Background())
	sess := waitSession(t, net)
	sess.Inject(&gadu.StatusEvent{UIN: 42, Status: gadu.StatusAvailable})
	rec.waitFor(t, "presence", func() bool { return len(rec.presences) >= 1 })

	select {
	case uin := <-tagged:
		if uin != 43 {
			t.Errorf("tagged %v, want 43 (42 is online, the own number is skipped)", uin)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("silent peer was not tagged")
	}
	select {
	case uin := <-tagged:
		t.Errorf("extra tag for %v", uin)
	case <-time.After(100 * time.Millisecond):
	}
}

const testBook = `<ContactBook><Groups></Groups><Contacts><Contact><Guid>g1</Guid><GGNumber>42</GGNumber><ShowName>Kuba</ShowName><MobilePhone>500</MobilePhone><Groups><GroupId>00000000-0000-0000-0000-000000000000</GroupId></Groups></Contact></Contacts></ContactBook>`

func TestEditContactList(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactListRaw, net.ContactListVersion = testBook, 7
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	waitSession(t, net)
	rec.waitFor(t, "contact list", func() bool { return client.contactName(42) == "Kuba" })

	ctx := context.Background()
	if err := client.editContactList(ctx, func(b *gadu.ContactBook) error { return b.Add(43, "Ola") }); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "reloaded list", func() bool { return client.contactName(43) == "Ola" })
	if err := client.editContactList(ctx, func(b *gadu.ContactBook) error { return b.Rename(42, "Jakub") }); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "renamed contact", func() bool { return client.contactName(42) == "Jakub" })
	if uin, err := client.findContact("jak"); err != nil || uin != 42 {
		t.Errorf("findContact(jak) = %v, %v", uin, err)
	}
	if err := client.editContactList(ctx, func(b *gadu.ContactBook) error { return b.Remove(43) }); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "removed contact", func() bool { return client.contactName(43) == "43" })

	raw, version, puts := net.ContactListState()
	if puts != 3 || version != 10 || !strings.Contains(raw, "<MobilePhone>500</MobilePhone>") {
		t.Errorf("server list: puts=%d version=%d raw=%s", puts, version, raw)
	}
}

func TestEditContactListRejected(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactListRaw, net.ContactListVersion = testBook, 7
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	waitSession(t, net)
	rec.waitFor(t, "contact list", func() bool { return client.contactName(42) == "Kuba" })
	// Another client changes the list.
	net.SetContactListVersion(8)
	err := client.editContactList(context.Background(), func(b *gadu.ContactBook) error { return b.Add(43, "Ola") })
	if !errors.Is(err, errListChanged) {
		t.Fatalf("err = %v, want errListChanged", err)
	}
	if raw, _, _ := net.ContactListState(); strings.Contains(raw, "43") {
		t.Error("rejected edit changed the server list")
	}
}

func TestBlockContact(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactListRaw, net.ContactListVersion = testBook, 7
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	sess := waitSession(t, net)
	rec.waitFor(t, "contact list", func() bool { return client.contactName(42) == "Kuba" })
	ctx := context.Background()

	if err := client.setContactBlocked(ctx, 42, true); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "block on the server", func() bool { return net.IsBlocked(42) && client.isBlocked(42) })
	sess.Inject(&gadu.MessageEvent{Sender: 42, Plain: "spam", Time: time.Now()})

	// A number outside the list is added and blocked.
	if err := client.setContactBlocked(ctx, 43, true); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "second block", func() bool { return net.IsBlocked(43) })

	if err := client.setContactBlocked(ctx, 42, false); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "unblock", func() bool { return !net.IsBlocked(42) && !client.isBlocked(42) })
	if err := client.setContactBlocked(ctx, 44, false); !errors.Is(err, gadu.ErrContactNotFound) {
		t.Errorf("unblock outside the list = %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, e := range rec.events {
		if m, ok := e.(*simplevent.Message[*gadu.MessageEvent]); ok && m.Data.Sender == 42 {
			t.Error("message from a blocked contact reached Matrix")
		}
	}
	raw, _, _ := net.ContactListState()
	if !strings.Contains(raw, "<MobilePhone>500</MobilePhone>") {
		t.Error("block lost other fields")
	}
}

func TestBlocksSentAfterLogin(t *testing.T) {
	net := fake.NewNetwork()
	net.ContactListRaw, net.ContactListVersion = strings.Replace(testBook, "<GroupId>00000000-0000-0000-0000-000000000000</GroupId>", "<GroupId>"+gadu.IgnoredGroupID+"</GroupId>", 1), 7
	client, _ := newTestClient(t, net)
	client.Connect(context.Background())
	waitSession(t, net)
	deadline := time.Now().Add(5 * time.Second)
	for !net.IsBlocked(42) {
		if time.Now().After(deadline) {
			t.Fatal("block from the list was not sent after login")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNoRoomNameBeforeContactList(t *testing.T) {
	client, _ := newTestClient(t, fake.NewNetwork())
	client.main.Config.StatusInName = true
	s := contactStatus{Status: gadu.StatusAvailable}
	if info := client.statusChatInfo(42, s); info != nil {
		t.Errorf("room name before the contact list = %+v", info)
	}
	client.handleContactList(context.Background(), &gadu.ContactListEvent{Contacts: []gadu.Contact{{UIN: 42, ShowName: "Kuba"}}})
	if info := client.statusChatInfo(42, s); info == nil || info.Name == nil || *info.Name != "🟢 Kuba" {
		t.Errorf("room name after the contact list = %+v", info)
	}
}

func TestGGImageProtocol(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	sess := waitSession(t, net)
	ref := gadu.ImageRef{CRC32: 0xdeadbeef, Size: 4}
	sess.Inject(&gadu.MessageEvent{Sender: contactUIN, Plain: " ", HTML: `<img name="deadbeef00000004">`, Time: time.Now(), Images: []gadu.ImageRef{ref}})
	deadline := time.Now().Add(5 * time.Second)
	for net.ImageRequestCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no image request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// An image that nobody asked for is dropped.
	sess.Inject(&gadu.ImageEvent{Sender: contactUIN, Ref: gadu.ImageRef{CRC32: 1, Size: 4}, Data: []byte("\x89PNG")})
	sess.Inject(&gadu.ImageEvent{Sender: contactUIN, Ref: ref, FileName: "C:\\x\\kot.png", Data: []byte("\x89PNG\r\n\x1a\n")})
	rec.waitFor(t, "image message", func() bool {
		for _, e := range rec.events {
			if _, ok := e.(*simplevent.Message[*gadu.RemoteMedia]); ok {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var images, texts int
	for _, e := range rec.events {
		switch m := e.(type) {
		case *simplevent.Message[*gadu.RemoteMedia]:
			images++
			if m.Data.MimeType != "image/png" || m.Data.FileName != "kot.png" {
				t.Errorf("image = %q %q", m.Data.MimeType, m.Data.FileName)
			}
		case *simplevent.Message[*gadu.MessageEvent]:
			texts++
		}
	}
	if images != 1 || texts != 0 {
		t.Errorf("images=%d texts=%d, want 1 image and no empty text message", images, texts)
	}
}

func testPNG(t *testing.T, w, h int, noise bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{uint8(x), uint8(y), 128, 255}
			if noise {
				c = color.NRGBA{uint8(x*7919 + y*104729), uint8(x * y), uint8(x ^ y), 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFitImage(t *testing.T) {
	small := testPNG(t, 20, 10, false)
	out, name, err := fitImage(small, "a.png", 255*1024)
	if err != nil || !bytes.Equal(out, small) || name != "a.png" {
		t.Errorf("small image changed: %d bytes, %q, %v", len(out), name, err)
	}
	big := testPNG(t, 2000, 1500, true)
	if len(big) <= 255*1024 {
		t.Fatalf("test image too small: %d", len(big))
	}
	out, name, err = fitImage(big, "photo.png", 255*1024)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "jpeg" || len(out) > 255*1024 || name != "photo.jpg" || cfg.Width > 1600 {
		t.Errorf("fitted: %d bytes, %s %dx%d, %q, %v", len(out), format, cfg.Width, cfg.Height, name, err)
	}
	if _, _, err := fitImage([]byte("not an image"), "x", 1000); err == nil {
		t.Error("garbage accepted")
	}
}

func TestSendGGImage(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	pic := testPNG(t, 30, 20, false)
	client.downloadMedia = func(context.Context, *event.MessageEventContent) ([]byte, error) { return pic, nil }
	client.publicMediaURL = func(_ context.Context, c *event.MessageEventContent) (string, error) {
		return "https://matrix.example.org/m/" + c.FileName, nil
	}
	client.Connect(context.Background())
	sess := waitSession(t, net)
	rec.waitFor(t, "connected state", rec.hasState(status.StateConnected))

	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: client.makePortalKey(contactUIN)}}
	msg := &bridgev2.MatrixMessage{}
	msg.Portal = portal
	msg.Content = &event.MessageEventContent{MsgType: event.MsgImage, Body: "kot.png", FileName: "kot.png"}
	if _, err := client.HandleMatrixMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	ref := gadu.ImageRefFor(pic)
	sent := net.SentMessages()
	if len(sent) != 1 || !strings.HasPrefix(sent[0].HTML, gadu.HTMLImageTag(ref)+"<br>") || !strings.Contains(sent[0].HTML, "https://matrix.example.org/m/kot.png") {
		t.Fatalf("sent = %+v", sent)
	}
	// Another contact cannot get the image.
	sess.Inject(&gadu.ImageRequestEvent{Sender: contactUIN + 1, Ref: ref})
	sess.Inject(&gadu.ImageRequestEvent{Sender: contactUIN, Ref: ref})
	deadline := time.Now().Add(5 * time.Second)
	for len(net.SentImages()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no image sent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	images := net.SentImages()
	if len(images) != 1 || images[0].To != contactUIN || !bytes.Equal(images[0].Data, pic) || images[0].FileName != "kot.png" {
		t.Errorf("images = %d, first to %v name %q", len(images), images[0].To, images[0].FileName)
	}
}

func TestOutImageLimitFromStatus(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.Connect(context.Background())
	sess := waitSession(t, net)
	if got := client.outImageLimit(contactUIN); got != 255*1024 {
		t.Errorf("default limit = %d", got)
	}
	sess.Inject(&gadu.StatusEvent{UIN: contactUIN, Status: gadu.StatusAvailable, ImageSizeKiB: 64})
	rec.waitFor(t, "presence", func() bool { return len(rec.presences) >= 1 })
	if got := client.outImageLimit(contactUIN); got != 64*1024 {
		t.Errorf("announced limit = %d", got)
	}
}


func TestLargeGGImageRequested(t *testing.T) {
	net := fake.NewNetwork()
	client, _ := newTestClient(t, net)
	client.Connect(context.Background())
	sess := waitSession(t, net)
	big := gadu.ImageRef{CRC32: 7, Size: 2 << 20}
	sess.Inject(&gadu.MessageEvent{Sender: contactUIN, Plain: " ", Time: time.Now(), Images: []gadu.ImageRef{big}})
	deadline := time.Now().Add(5 * time.Second)
	for net.ImageRequestCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a 2 MB image was not requested")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

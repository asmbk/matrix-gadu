package connector

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
	"github.com/asmbk/matrix-gadu/pkg/gadu/fake"
)

func TestHasUnreadMessages(t *testing.T) {
	const user, ghost, bot = id.UserID("@me:x"), id.UserID("@gg_42:x"), id.UserID("@ggbot:x")
	empty := ""
	state := func(evtID string, typ event.Type) *event.Event {
		return &event.Event{ID: id.EventID(evtID), Type: typ, Sender: bot, StateKey: &empty}
	}
	msg := func(evtID string, sender id.UserID) *event.Event {
		return &event.Event{ID: id.EventID(evtID), Type: event.EventMessage, Sender: sender}
	}
	statusChange := []*event.Event{state("$bridge", event.StateHalfShotBridge), state("$topic", event.StateTopic), state("$name", event.StateRoomName)}
	for _, tc := range []struct {
		name   string
		events []*event.Event
		marker id.EventID
		want   bool
	}{
		{"read up to the contact message", append(statusChange, msg("$m1", ghost)), "$m1", false},
		{"contact message after the marker", append(statusChange, msg("$m2", ghost), msg("$m1", ghost)), "$m1", true},
		{"own message last", append(statusChange, msg("$m2", user), msg("$m1", ghost)), "", false},
		{"no marker, contact message last", append(statusChange, msg("$m1", ghost)), "", true},
		{"marker on an older status change", append(statusChange, state("$old", event.StateRoomName), msg("$m1", ghost)), "$old", false},
		{"only state events", statusChange, "", false},
	} {
		if got := hasUnreadMessages(tc.events, tc.marker, user); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStatusChangesReadOption(t *testing.T) {
	for _, on := range []bool{false, true} {
		net := fake.NewNetwork()
		client, rec := newTestClient(t, net)
		client.main.Config.StatusInName = true
		client.main.Config.StatusChangesRead = on
		client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
		client.Connect(context.Background())
		sess := waitSession(t, net)
		sess.Inject(&gadu.StatusEvent{UIN: 42, Status: gadu.StatusAvailable})
		rec.waitFor(t, "status change", func() bool { return len(rec.statusChanges()) >= 1 })
		rec.mu.Lock()
		var withPost int
		for _, e := range rec.events {
			if c, ok := e.(*simplevent.ChatInfoChange); ok && c.ChatInfoChange.ChatInfo != nil && c.ChatInfoChange.ChatInfo.Name != nil && c.PostHandleFunc != nil {
				withPost++
			}
		}
		rec.mu.Unlock()
		if (withPost > 0) != on {
			t.Errorf("status_changes_read=%v: %d status changes with a post handler", on, withPost)
		}
		client.Disconnect()
	}
}

func TestStatusChangesReadOnlyRealChanges(t *testing.T) {
	net := fake.NewNetwork()
	client, rec := newTestClient(t, net)
	client.main.Config.StatusInName = true
	client.main.Config.StatusChangesRead = true
	client.hasRoom = func(context.Context, networkid.PortalKey) bool { return true }
	marked := 0
	client.markStatusRead = func(context.Context, *bridgev2.Portal) { marked++ }
	client.Connect(context.Background())
	sess := waitSession(t, net)
	sess.Inject(&gadu.StatusEvent{UIN: 42, Status: gadu.StatusAvailable})
	rec.waitFor(t, "status change", func() bool { return len(rec.statusChanges()) >= 1 })
	rec.mu.Lock()
	var change *simplevent.ChatInfoChange
	for _, e := range rec.events {
		if c, ok := e.(*simplevent.ChatInfoChange); ok && c.PostHandleFunc != nil {
			change = c
		}
	}
	rec.mu.Unlock()
	if change == nil {
		t.Fatal("no status change with a post handler")
	}
	name := *change.ChatInfoChange.ChatInfo.Name
	same := &bridgev2.Portal{Portal: &database.Portal{Name: name}}
	change.PreHandleFunc(context.Background(), same)
	change.PostHandleFunc(context.Background(), same)
	if marked != 0 {
		t.Errorf("marked an unchanged room name as read")
	}
	other := &bridgev2.Portal{Portal: &database.Portal{Name: "⚫ old"}}
	change.PreHandleFunc(context.Background(), other)
	change.PostHandleFunc(context.Background(), other)
	if marked != 1 {
		t.Errorf("marked = %d, want 1 for a changed name", marked)
	}
}

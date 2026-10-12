package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// presenceRefreshInterval is shorter than the idle timeout of homeservers
// (Tuwunel: 5 minutes), so that online contacts do not turn "away".
const presenceRefreshInterval = 4 * time.Minute

type contactStatus struct {
	Status      gadu.Status
	Description string
}

var statusLabels = map[gadu.Status]string{
	gadu.StatusAvailable:   "🟢 Available",
	gadu.StatusFreeForChat: "🟢 Free for chat",
	gadu.StatusBusy:        "🟡 Busy",
	gadu.StatusDND:         "🔴 Do not disturb",
	gadu.StatusInvisible:   "⚫ Offline",
	gadu.StatusOffline:     "⚫ Offline",
	gadu.StatusBlocked:     "⚫ Offline",
}

// formatStatus returns the status line used in room topics and in the online command.
// statusEmoji returns the first part of the status label, for room names.
func statusEmoji(s gadu.Status) string {
	label, ok := statusLabels[s]
	if !ok {
		label = statusLabels[gadu.StatusOffline]
	}
	emoji, _, _ := strings.Cut(label, " ")
	return emoji
}

// statusRoomName returns the room name with the status emoji, for example "🟢 Kuba".
func (gc *GaduClient) statusRoomName(uin gadu.UIN, s contactStatus) string {
	return statusEmoji(s.Status) + " " + *gc.userInfo(uin).Name
}

func formatStatus(s contactStatus) string {
	label, ok := statusLabels[s.Status]
	if !ok {
		label = statusLabels[gadu.StatusOffline]
	}
	if s.Description != "" {
		return label + " – " + s.Description
	}
	return label
}

func isOnline(s gadu.Status) bool {
	switch s {
	case gadu.StatusAvailable, gadu.StatusFreeForChat, gadu.StatusBusy, gadu.StatusDND:
		return true
	}
	return false
}

func matrixPresence(s gadu.Status) event.Presence {
	switch s {
	case gadu.StatusAvailable, gadu.StatusFreeForChat:
		return event.PresenceOnline
	case gadu.StatusBusy, gadu.StatusDND:
		return event.PresenceUnavailable
	}
	return event.PresenceOffline
}

// setGhostPresence sets the Matrix presence of an existing ghost. It does
// not create ghosts, so contacts without a room cause no Matrix traffic.
func (gc *GaduClient) setGhostPresence(ctx context.Context, uin gadu.UIN, s contactStatus) error {
	ghost, err := gc.main.br.GetExistingGhostByID(ctx, makeUserID(uin))
	if err != nil || ghost == nil {
		return err
	}
	intent, ok := ghost.Intent.(*matrix.ASIntent)
	if !ok {
		return nil
	}
	return intent.Matrix.SetPresence(ctx, mautrix.ReqPresence{
		Presence:  matrixPresence(s.Status),
		StatusMsg: s.Description,
	})
}

// subscribe asks GG for status updates of the numbers that the current
// session does not follow yet. It skips the own number.
func (gc *GaduClient) subscribe(ctx context.Context, uins ...gadu.UIN) {
	gc.mu.Lock()
	sess := gc.sess
	var add []gadu.UIN
	for _, uin := range uins {
		if _, done := gc.notified[uin]; done || uin == gc.uin || sess == nil {
			continue
		}
		gc.notified[uin] = struct{}{}
		add = append(add, uin)
	}
	gc.mu.Unlock()
	if len(add) == 0 {
		return
	}
	if err := sess.Notify(ctx, add); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Int("count", len(add)).Msg("Failed to subscribe to contact status")
		gc.mu.Lock()
		for _, uin := range add {
			delete(gc.notified, uin)
		}
		gc.mu.Unlock()
	}
}

// listRoomPeers returns the numbers that have a room for this login.
func (gc *GaduClient) listRoomPeers(ctx context.Context) []gadu.UIN {
	portals, err := gc.main.br.GetAllPortalsWithMXID(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to list rooms")
		return nil
	}
	var uins []gadu.UIN
	for _, p := range portals {
		if p.Receiver != gc.login.ID {
			continue
		}
		if uin, err := parsePortalID(p.ID); err == nil {
			uins = append(uins, uin)
		}
	}
	return uins
}

func (gc *GaduClient) status(uin gadu.UIN) (contactStatus, bool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	s, ok := gc.statuses[uin]
	return s, ok
}

func (gc *GaduClient) handleStatus(ctx context.Context, e *gadu.StatusEvent) {
	if e.UIN == gc.uin {
		return
	}
	s := contactStatus{Status: e.Status, Description: e.Description}
	gc.mu.Lock()
	prev, had := gc.statuses[e.UIN]
	gc.statuses[e.UIN] = s
	gc.mu.Unlock()
	if had && prev == s {
		return
	}
	if err := gc.setPresence(ctx, e.UIN, s); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Stringer("uin", e.UIN).Msg("Failed to set ghost presence")
	}
	gc.updateStatusMeta(ctx, e.UIN, s)
	gc.updateLowPriority(ctx, e.UIN, s)
}

// statusChatInfo returns the room topic and name that show the status, as
// enabled by status_in_topic and status_in_name. It returns nil if both are off.
func (gc *GaduClient) statusChatInfo(uin gadu.UIN, s contactStatus) *bridgev2.ChatInfo {
	cfg := gc.main.Config
	if !cfg.StatusInTopic && !cfg.StatusInName {
		return nil
	}
	info := &bridgev2.ChatInfo{ExcludeChangesFromTimeline: true}
	if cfg.StatusInTopic {
		info.Topic = ptr.Ptr(formatStatus(s))
	}
	gc.mu.Lock()
	loaded := gc.contactsLoaded
	gc.mu.Unlock()
	// GG sends statuses before the contact list. Without the list, the name
	// would be the number, and the room would get a false rename at each login.
	// The bridge sets the names when the list arrives (applyAllStatusMeta).
	if cfg.StatusInName && loaded {
		// An explicit name marks the room name as custom in bridgev2,
		// so the plain contact name does not overwrite it.
		info.Name = ptr.Ptr(gc.statusRoomName(uin, s))
	}
	if info.Name == nil && info.Topic == nil {
		return nil
	}
	return info
}

// updateStatusMeta writes the status into the room topic and name, if that is
// enabled and the room exists. bridgev2 skips values that did not change.
func (gc *GaduClient) updateStatusMeta(ctx context.Context, uin gadu.UIN, s contactStatus) {
	info := gc.statusChatInfo(uin, s)
	if info == nil {
		return
	}
	key := gc.makePortalKey(uin)
	if !gc.hasRoom(ctx, key) {
		return
	}
	meta := simplevent.EventMeta{
		Type:      bridgev2.RemoteEventChatInfoChange,
		PortalKey: key,
		Timestamp: time.Now(),
	}
	if gc.main.Config.StatusChangesRead {
		// Mark as read only real changes. After a login, most statuses repeat
		// the current room name and topic, and bridgev2 sends nothing for them.
		var changed bool
		meta.PreHandleFunc = func(_ context.Context, portal *bridgev2.Portal) {
			changed = (info.Name != nil && *info.Name != portal.Name) || (info.Topic != nil && *info.Topic != portal.Topic)
		}
		meta.PostHandleFunc = func(ctx context.Context, portal *bridgev2.Portal) {
			if changed {
				gc.markStatusRead(ctx, portal)
			}
		}
	}
	gc.queueEvent(&simplevent.ChatInfoChange{
		EventMeta:      meta,
		ChatInfoChange: &bridgev2.ChatInfoChange{ChatInfo: info},
	})
}

// applyAllStatusMeta re-sends the topic, name and low priority tag for every known status. It runs
// after new contact rooms exist, because statuses usually arrive before the
// rooms, and after the contact list reloads, because contact names can change.
func (gc *GaduClient) applyAllStatusMeta(ctx context.Context) {
	gc.mu.Lock()
	statuses := make(map[gadu.UIN]contactStatus, len(gc.statuses))
	for uin, s := range gc.statuses {
		statuses[uin] = s
	}
	gc.mu.Unlock()
	for uin, s := range statuses {
		gc.updateStatusMeta(ctx, uin, s)
		gc.updateLowPriority(ctx, uin, s)
	}
}

// refreshPresence repeats the presence of online contacts, so that the
// homeserver does not mark them idle.
func (gc *GaduClient) refreshPresence(ctx context.Context) {
	gc.mu.Lock()
	online := make(map[gadu.UIN]contactStatus)
	for uin, s := range gc.statuses {
		if isOnline(s.Status) {
			online[uin] = s
		}
	}
	gc.mu.Unlock()
	for uin, s := range online {
		if err := gc.setPresence(ctx, uin, s); err != nil {
			zerolog.Ctx(ctx).Debug().Err(err).Stringer("uin", uin).Msg("Failed to refresh ghost presence")
		}
	}
}

// clearStatuses forgets all statuses after the GG connection ends and marks
// online contacts offline in Matrix. Room topics stay, to avoid noise.
func (gc *GaduClient) clearStatuses(ctx context.Context) {
	gc.mu.Lock()
	statuses := gc.statuses
	gc.statuses = make(map[gadu.UIN]contactStatus)
	gc.mu.Unlock()
	for uin, s := range statuses {
		if isOnline(s.Status) {
			_ = gc.setPresence(ctx, uin, contactStatus{Status: gadu.StatusOffline})
		}
	}
}

// statusLines returns the lines of the online command. Without a query, it
// lists online contacts. With a query, it lists matching contacts, offline ones too.
func (gc *GaduClient) statusLines(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	type entry struct {
		name   string
		uin    gadu.UIN
		status contactStatus
	}
	var entries []entry
	gc.mu.Lock()
	// Numbers outside the contact list (for example after start-chat) can have a status too.
	uins := make(map[gadu.UIN]struct{}, len(gc.contacts))
	for uin := range gc.contacts {
		uins[uin] = struct{}{}
	}
	for uin := range gc.statuses {
		uins[uin] = struct{}{}
	}
	for uin := range uins {
		c, ok := gc.contacts[uin]
		if !ok {
			c = gadu.Contact{UIN: uin}
		}
		s := gc.statuses[uin]
		if query == "" {
			if !isOnline(s.Status) {
				continue
			}
		} else if !contactMatches(c, query) {
			continue
		}
		entries = append(entries, entry{name: gc.main.Config.FormatDisplayname(c), uin: uin, status: s})
	}
	gc.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool {
		oi, oj := isOnline(entries[i].status.Status), isOnline(entries[j].status.Status)
		if oi != oj {
			return oi
		}
		return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
	})
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", markdownEscaper.Replace(e.name), e.uin, markdownEscaper.Replace(formatStatus(e.status))))
	}
	return lines
}

// markdownEscaper escapes the characters that the bot's Markdown renderer
// would read as formatting in contact names and status texts.
var markdownEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", "&lt;", ">", "&gt;")

func contactMatches(c gadu.Contact, query string) bool {
	for _, f := range []string{c.UIN.String(), c.ShowName, c.Nickname, c.FullName()} {
		if f != "" && strings.Contains(strings.ToLower(f), query) {
			return true
		}
	}
	return false
}

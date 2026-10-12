package connector

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// defaultAvatarFetchInterval keeps the avatar server from answering HTTP 429.
const defaultAvatarFetchInterval = 500 * time.Millisecond

var avatarHTTPClient = gadu.NewSafeHTTPClient(15 * time.Second)

// cachedAvatar is the last known GG avatar of a number. A nil data means "no avatar".
type cachedAvatar struct {
	version string
	data    []byte
}

// contactRoomPowerLevels lets the user change the picture of contact rooms.
// The room name stays with the bridge, because it can carry the status emoji.
var contactRoomPowerLevels = &bridgev2.PowerLevelOverrides{
	Events: map[event.Type]int{event.StateRoomAvatar: 0},
}

func fetchGGAvatar(ctx context.Context, uin gadu.UIN) (*gadu.Avatar, error) {
	return gadu.FetchAvatar(ctx, avatarHTTPClient, uin)
}

// avatarFor returns the avatar for bridgev2 from the cache. It returns nil if
// the avatar is not known yet, so that the current avatar stays.
func (gc *GaduClient) avatarFor(uin gadu.UIN) *bridgev2.Avatar {
	gc.mu.Lock()
	cached, ok := gc.avatars[uin]
	gc.mu.Unlock()
	if !ok {
		return nil
	}
	if cached.data == nil {
		return &bridgev2.Avatar{Remove: true}
	}
	data := cached.data
	return &bridgev2.Avatar{
		ID:  networkid.AvatarID("gg:" + cached.version),
		Get: func(context.Context) ([]byte, error) { return data, nil },
	}
}

// syncAvatars downloads the GG avatars of the given numbers and applies the
// ones that changed. It runs in the background after each contact list load.
func (gc *GaduClient) syncAvatars(ctx context.Context, uins []gadu.UIN) {
	log := zerolog.Ctx(ctx)
	seen := make(map[gadu.UIN]bool, len(uins))
	changed := 0
	for _, uin := range uins {
		if seen[uin] || uin == gc.uin {
			continue
		}
		seen[uin] = true
		av, err := gc.fetchAvatar(ctx, uin)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Debug().Err(err).Stringer("uin", uin).Msg("Failed to fetch GG avatar")
			continue
		}
		next := cachedAvatar{}
		if av != nil {
			next = cachedAvatar{version: av.Version, data: av.Data}
		}
		gc.mu.Lock()
		prev, known := gc.avatars[uin]
		gc.avatars[uin] = next
		gc.mu.Unlock()
		if !known || prev.version != next.version {
			gc.applyAvatar(ctx, uin)
			changed++
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(gc.avatarFetchInterval):
		}
	}
	log.Debug().Int("checked", len(seen)).Int("applied", changed).Msg("Synced GG avatars")
}

// applyAvatar sets the cached avatar on the ghost and on the room, unless the
// user set a custom room picture.
func (gc *GaduClient) applyAvatar(ctx context.Context, uin gadu.UIN) {
	av := gc.avatarFor(uin)
	if av == nil {
		return
	}
	if ghost, err := gc.existingGhost(ctx, makeUserID(uin)); err == nil && ghost != nil {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{Avatar: av})
	}
	key := gc.makePortalKey(uin)
	if !gc.hasRoom(ctx, key) {
		return
	}
	info := &bridgev2.ChatInfo{Avatar: av, ExcludeChangesFromTimeline: true}
	gc.queueEvent(&simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: key,
			Timestamp: time.Now(),
			PreHandleFunc: func(_ context.Context, portal *bridgev2.Portal) {
				if hasCustomAvatar(portal) {
					info.Avatar = nil
				}
			},
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{ChatInfo: info},
	})
}

// applyRoomPermissions makes sure that existing contact rooms let the user
// change the room picture. bridgev2 sends power levels only if they change.
func (gc *GaduClient) applyRoomPermissions(ctx context.Context, uins []gadu.UIN) {
	for _, uin := range uins {
		key := gc.makePortalKey(uin)
		if !gc.hasRoom(ctx, key) {
			continue
		}
		gc.queueEvent(&simplevent.ChatInfoChange{
			EventMeta: simplevent.EventMeta{
				Type:      bridgev2.RemoteEventChatInfoChange,
				PortalKey: key,
				Timestamp: time.Now(),
			},
			ChatInfoChange: &bridgev2.ChatInfoChange{ChatInfo: &bridgev2.ChatInfo{
				Members:                    &bridgev2.ChatMemberList{PowerLevels: contactRoomPowerLevels},
				ExcludeChangesFromTimeline: true,
			}},
		})
	}
}

func hasCustomAvatar(portal *bridgev2.Portal) bool {
	meta, ok := portal.Metadata.(*PortalMetadata)
	return ok && meta.CustomAvatar
}

// HandleMatrixRoomAvatar accepts a room picture that the user sets in a
// contact room. GG has no such feature, so the picture stays in Matrix, and
// the bridge stops replacing it with the GG avatar. Removing the picture
// brings the GG avatar back.
func (gc *GaduClient) HandleMatrixRoomAvatar(ctx context.Context, msg *bridgev2.MatrixRoomAvatar) (bool, error) {
	portal := msg.Portal
	meta, ok := portal.Metadata.(*PortalMetadata)
	if !ok {
		meta = &PortalMetadata{}
		portal.Metadata = meta
	}
	url := msg.Content.URL
	meta.CustomAvatar = url != ""
	portal.AvatarMXC = url
	portal.AvatarHash = [32]byte{}
	if url == "" {
		portal.AvatarID = ""
		if uin, err := parsePortalID(portal.ID); err == nil {
			go gc.applyAvatar(context.WithoutCancel(ctx), uin)
		}
	} else {
		portal.AvatarID = networkid.AvatarID("custom:" + string(url))
	}
	return true, nil
}

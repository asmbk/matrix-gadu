package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// lowPriorityMarker marks the low priority tags that the bridge set. Some
// homeservers (Tuwunel) drop it, so the bridge also records its tags in
// PortalMetadata.LowPriority and trusts only that record.
const lowPriorityMarker = "matrix-gadu:offline"

// defaultOfflineSweepDelay is the time after login when the bridge treats
// room peers without a status as offline. GG sends statuses only for online
// contacts, and they arrive in the first seconds of a session.
const defaultOfflineSweepDelay = 30 * time.Second

// markSilentPeersOffline tags the rooms of peers that sent no status since
// login, because GG sends nothing for offline contacts.
func (gc *GaduClient) markSilentPeersOffline(ctx context.Context) {
	if !gc.main.Config.OfflineLowPriority {
		return
	}
	peers := gc.roomPeers(ctx)
	zerolog.Ctx(ctx).Debug().Int("room_peers", len(peers)).Msg("Marking room peers without a status as offline")
	for _, uin := range peers {
		if _, ok := gc.status(uin); ok || uin == gc.uin {
			continue
		}
		gc.updateLowPriority(ctx, uin, contactStatus{Status: gadu.StatusOffline})
	}
}

// updateLowPriority moves the room of an offline contact to the low priority
// section of the room list, and back when the contact comes online. It does
// nothing if offline_low_priority is off, the room does not exist, or the
// state did not change since the last call.
func (gc *GaduClient) updateLowPriority(ctx context.Context, uin gadu.UIN, s contactStatus) {
	if !gc.main.Config.OfflineLowPriority {
		return
	}
	offline := !isOnline(s.Status)
	gc.mu.Lock()
	prev, known := gc.lowPriority[uin]
	gc.mu.Unlock()
	if known && prev == offline {
		return
	}
	if !gc.hasRoom(ctx, gc.makePortalKey(uin)) {
		return
	}
	if err := gc.setLowPriority(ctx, uin, offline); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Stringer("uin", uin).Bool("offline", offline).Msg("Failed to update the low priority tag")
		return
	}
	gc.mu.Lock()
	gc.lowPriority[uin] = offline
	gc.mu.Unlock()
}

// setRoomLowPriority adds or removes the low priority tag with the double
// puppet of the user. Tags are account data, so they cause no room events.
// If the user tagged the room as favourite or low priority, the bridge
// leaves the room alone. It removes only the tags that it recorded itself.
func (gc *GaduClient) setRoomLowPriority(ctx context.Context, uin gadu.UIN, offline bool) error {
	portal, err := gc.main.br.GetExistingPortalByKey(ctx, gc.makePortalKey(uin))
	if err != nil || portal == nil || portal.MXID == "" {
		return err
	}
	dp, ok := gc.login.User.DoublePuppet(ctx).(*matrix.ASIntent)
	if !ok || dp == nil {
		// Without double puppeting, the bridge cannot change the tags of the user.
		zerolog.Ctx(ctx).Debug().Msg("No double puppet, not changing the low priority tag")
		return nil
	}
	tags, err := dp.Matrix.GetTags(ctx, portal.MXID)
	if err != nil {
		return fmt.Errorf("get room tags: %w", err)
	}
	meta, ok := portal.Metadata.(*PortalMetadata)
	if !ok {
		meta = &PortalMetadata{}
		portal.Metadata = meta
	}
	_, hasLow := tags.Tags[event.RoomTagLowPriority]
	if offline {
		if _, fav := tags.Tags[event.RoomTagFavourite]; fav || hasLow {
			return nil
		}
		zerolog.Ctx(ctx).Debug().Stringer("uin", uin).Msg("Moving the room of an offline contact to low priority")
		err = dp.Matrix.AddTagWithCustomData(ctx, portal.MXID, event.RoomTagLowPriority, &event.TagMetadata{
			MauDoublePuppetSource: lowPriorityMarker,
		})
		if err != nil {
			return err
		}
		meta.LowPriority = true
		return portal.Save(ctx)
	}
	if !meta.LowPriority {
		return nil
	}
	if hasLow {
		zerolog.Ctx(ctx).Debug().Stringer("uin", uin).Msg("Moving the room of an online contact out of low priority")
		if err = dp.Matrix.RemoveTag(ctx, portal.MXID, event.RoomTagLowPriority); err != nil {
			return err
		}
	}
	meta.LowPriority = false
	return portal.Save(ctx)
}

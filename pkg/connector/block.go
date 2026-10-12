package connector

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// applyBlocks sends the blocks of the contact list to the server. GG keeps
// blocks only for one session, so the bridge sends them after each list load.
// It also lifts the blocks of contacts that left the "Ignorowani" group.
func (gc *GaduClient) applyBlocks(ctx context.Context, contacts []gadu.Contact) {
	want := make(map[gadu.UIN]bool)
	for _, c := range contacts {
		if c.Blocked {
			want[c.UIN] = true
		}
	}
	gc.mu.Lock()
	sess := gc.sess
	var block, unblock []gadu.UIN
	for uin := range want {
		if !gc.blocked[uin] {
			block = append(block, uin)
		}
	}
	for uin := range gc.blocked {
		if !want[uin] {
			unblock = append(unblock, uin)
		}
	}
	gc.mu.Unlock()
	if sess == nil {
		return
	}
	set := func(uin gadu.UIN, blocked bool) {
		if err := sess.SetBlocked(ctx, uin, blocked); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Stringer("uin", uin).Bool("blocked", blocked).Msg("Failed to change the block on the GG server")
			return
		}
		gc.mu.Lock()
		if blocked {
			gc.blocked[uin] = true
		} else {
			delete(gc.blocked, uin)
		}
		gc.mu.Unlock()
	}
	for _, uin := range block {
		set(uin, true)
	}
	for _, uin := range unblock {
		set(uin, false)
	}
	if len(block)+len(unblock) > 0 {
		zerolog.Ctx(ctx).Debug().Int("blocked", len(block)).Int("unblocked", len(unblock)).Msg("Sent contact blocks to GG")
	}
}

// isBlocked tells if the contact list blocks the number.
func (gc *GaduClient) isBlocked(uin gadu.UIN) bool {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	return gc.contacts[uin].Blocked
}

// setContactBlocked blocks or unblocks a number in the contact list. A number
// outside the list is added first, because GG keeps blocks in the list.
func (gc *GaduClient) setContactBlocked(ctx context.Context, uin gadu.UIN, blocked bool) error {
	return gc.editContactList(ctx, func(b *gadu.ContactBook) error {
		if _, ok := b.Contact(uin); !ok {
			if !blocked {
				return gadu.ErrContactNotFound
			}
			if err := b.Add(uin, uin.String()); err != nil {
				return err
			}
		}
		return b.SetBlocked(uin, blocked)
	})
}

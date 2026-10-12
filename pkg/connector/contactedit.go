package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

// contactPutTimeout limits the wait for the server answer to a contact list edit.
const contactPutTimeout = 20 * time.Second

var (
	errNotConnected      = errors.New("not connected to GG")
	errListNotLoaded     = errors.New("the contact list is not loaded yet, try again in a moment")
	errListChanged       = errors.New("the contact list changed in another GG client, the bridge loads it again, try again in a moment")
	errContactAmbiguous  = errors.New("more than one contact matches, use the GG number")
	errContactNoMatch    = errors.New("no contact matches")
	errContactListFormat = errors.New("the contact list is not in the GG 10 format, so the bridge cannot edit it")
)

// handleContactListPut passes the server answer to the waiting edit.
func (gc *GaduClient) handleContactListPut(e *gadu.ContactListPutEvent) {
	select {
	case gc.putReply <- e:
	default:
	}
}

// handleContactListVersion loads the list again when another client changed it.
func (gc *GaduClient) handleContactListVersion(ctx context.Context, e *gadu.ContactListVersionEvent) {
	gc.mu.Lock()
	known := gc.contactVersion
	sess := gc.sess
	gc.mu.Unlock()
	if e.Version == known || sess == nil {
		return
	}
	zerolog.Ctx(ctx).Debug().Uint32("version", e.Version).Uint32("known", known).Msg("The contact list changed on the server")
	if err := sess.RequestContactList(ctx); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to request the contact list")
	}
}

// editContactList applies an edit to the server-side contact list. It starts
// from the last list that the server sent, so that all other entries stay as
// they are. After the server accepts the edit, the bridge loads the list again,
// which updates the contact names and creates rooms for new contacts.
func (gc *GaduClient) editContactList(ctx context.Context, edit func(*gadu.ContactBook) error) error {
	gc.editMu.Lock()
	defer gc.editMu.Unlock()
	gc.mu.Lock()
	sess, raw, version := gc.sess, gc.contactRaw, gc.contactVersion
	gc.mu.Unlock()
	if sess == nil {
		return errNotConnected
	}
	if raw == "" {
		return errListNotLoaded
	}
	if !strings.HasPrefix(strings.TrimSpace(raw), "<") {
		return errContactListFormat
	}
	book, err := gadu.ParseContactBook(raw)
	if err != nil {
		return err
	}
	if err := edit(book); err != nil {
		return err
	}
	data := book.String()
	// Drop a late answer to an earlier edit.
	select {
	case <-gc.putReply:
	default:
	}
	if err := sess.PutContactList(ctx, version, data); err != nil {
		return fmt.Errorf("send the contact list: %w", err)
	}
	var reply *gadu.ContactListPutEvent
	select {
	case reply = <-gc.putReply:
	case <-time.After(gc.contactPutTimeout):
		return errors.New("the GG server did not answer, check the list with sync-contacts")
	case <-ctx.Done():
		return ctx.Err()
	}
	if !reply.Accepted {
		_ = sess.RequestContactList(ctx)
		return errListChanged
	}
	gc.mu.Lock()
	gc.contactRaw, gc.contactVersion = data, reply.Version
	gc.mu.Unlock()
	if err := sess.RequestContactList(ctx); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to request the contact list after an edit")
	}
	return nil
}

// findContact returns the number of the contact that a GG number or a name
// part points to. A number does not need to be in the contact list.
func (gc *GaduClient) findContact(query string) (gadu.UIN, error) {
	if uin, err := gadu.ParseUIN(query); err == nil {
		return uin, nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	gc.mu.Lock()
	defer gc.mu.Unlock()
	var found []gadu.UIN
	for uin, c := range gc.contacts {
		if strings.EqualFold(gc.main.Config.FormatDisplayname(c), query) {
			// An exact name wins over partial matches.
			return uin, nil
		}
		if contactMatches(c, query) {
			found = append(found, uin)
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("%w %q", errContactNoMatch, query)
	case 1:
		return found[0], nil
	}
	return 0, errContactAmbiguous
}

func (gc *GaduClient) contactName(uin gadu.UIN) string {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	if c, ok := gc.contacts[uin]; ok {
		return gc.main.Config.FormatDisplayname(c)
	}
	return uin.String()
}

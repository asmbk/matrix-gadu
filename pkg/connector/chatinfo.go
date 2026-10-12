package connector

import (
	"context"
	"sort"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

func (gc *GaduClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	peer, err := parsePortalID(portal.ID)
	if err != nil {
		return nil, err
	}
	return gc.dmChatInfo(peer, !hasCustomAvatar(portal)), nil
}

// dmChatInfo returns the info of a contact room. withAvatar adds the GG avatar
// as the room picture. It must be false for a room with a custom picture.
func (gc *GaduClient) dmChatInfo(peer gadu.UIN, withAvatar bool) *bridgev2.ChatInfo {
	members := bridgev2.ChatMemberMap{}
	members.Set(bridgev2.ChatMember{
		EventSender: gc.makeEventSender(gc.uin),
		Membership:  event.MembershipJoin,
	})
	members.Set(bridgev2.ChatMember{
		EventSender: gc.makeEventSender(peer),
		Membership:  event.MembershipJoin,
		UserInfo:    gc.userInfo(peer),
	})
	info := &bridgev2.ChatInfo{
		Type: ptr.Ptr(database.RoomTypeDM),
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: makeUserID(peer),
			MemberMap:   members,
			PowerLevels: contactRoomPowerLevels,
		},
	}
	if withAvatar {
		info.Avatar = gc.avatarFor(peer)
	}
	if s, ok := gc.status(peer); ok {
		if status := gc.statusChatInfo(peer, s); status != nil {
			info.Topic, info.Name = status.Topic, status.Name
		}
	}
	return info
}

func (gc *GaduClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	uin, err := parseUserID(ghost.ID)
	if err != nil {
		return nil, err
	}
	return gc.userInfo(uin), nil
}

// userInfo returns the ghost info from the contact list. A number that is
// not on the list gets the number as its name.
func (gc *GaduClient) userInfo(uin gadu.UIN) *bridgev2.UserInfo {
	contact, ok := gc.contact(uin)
	if !ok {
		contact = gadu.Contact{UIN: uin}
	}
	return &bridgev2.UserInfo{
		Identifiers: []string{"gg:" + uin.String()},
		Name:        ptr.Ptr(gc.main.Config.FormatDisplayname(contact)),
		Avatar:      gc.avatarFor(uin),
		IsBot:       ptr.Ptr(false),
	}
}

func (gc *GaduClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	uin, err := gadu.ParseUIN(identifier)
	if err != nil {
		return nil, err
	}
	ghost, err := gc.main.br.GetGhostByID(ctx, makeUserID(uin))
	if err != nil {
		return nil, err
	}
	resp := &bridgev2.ResolveIdentifierResponse{
		Ghost:    ghost,
		UserID:   makeUserID(uin),
		UserInfo: gc.userInfo(uin),
	}
	if createChat {
		gc.subscribe(ctx, uin)
		resp.Chat = &bridgev2.CreateChatResponse{
			PortalKey:  gc.makePortalKey(uin),
			PortalInfo: gc.dmChatInfo(uin, !gc.hasRoom(ctx, gc.makePortalKey(uin))),
		}
	}
	return resp, nil
}

func (gc *GaduClient) CreateChatWithGhost(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.CreateChatResponse, error) {
	uin, err := parseUserID(ghost.ID)
	if err != nil {
		return nil, err
	}
	gc.subscribe(ctx, uin)
	return &bridgev2.CreateChatResponse{
		PortalKey:  gc.makePortalKey(uin),
		PortalInfo: gc.dmChatInfo(uin, !gc.hasRoom(ctx, gc.makePortalKey(uin))),
	}, nil
}

// SearchUsers finds contacts whose name or GG number contains the query.
func (gc *GaduClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	gc.mu.Lock()
	var matches []gadu.Contact
	for _, c := range gc.contacts {
		if contactMatches(c, query) {
			matches = append(matches, c)
		}
	}
	gc.mu.Unlock()
	sort.Slice(matches, func(i, j int) bool { return matches[i].UIN < matches[j].UIN })
	resp := make([]*bridgev2.ResolveIdentifierResponse, 0, len(matches))
	for _, c := range matches {
		resp = append(resp, &bridgev2.ResolveIdentifierResponse{
			UserID:   makeUserID(c.UIN),
			UserInfo: gc.userInfo(c.UIN),
		})
	}
	return resp, nil
}

func (gc *GaduClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	gc.mu.Lock()
	uins := make([]gadu.UIN, 0, len(gc.contacts))
	for uin := range gc.contacts {
		uins = append(uins, uin)
	}
	gc.mu.Unlock()
	resp := make([]*bridgev2.ResolveIdentifierResponse, 0, len(uins))
	for _, uin := range uins {
		resp = append(resp, &bridgev2.ResolveIdentifierResponse{
			UserID:   makeUserID(uin),
			UserInfo: gc.userInfo(uin),
		})
	}
	return resp, nil
}

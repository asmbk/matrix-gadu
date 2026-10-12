package connector

import (
	"errors"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

var cmdReconnect = &commands.FullHandler{
	Func: fnReconnect,
	Name: "reconnect",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionAuth,
		Description: "Close the GG connection and open it again",
	},
	RequiresLogin: true,
}

var cmdSyncContacts = &commands.FullHandler{
	Func: fnSyncContacts,
	Name: "sync-contacts",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Load the GG contact list again and update the contact names",
	},
	RequiresLogin: true,
}

var cmdOnline = &commands.FullHandler{
	Func: fnOnline,
	Name: "online",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "List online contacts. With a name or number, show the status of matching contacts",
		Args:        "[_name or number_]",
	},
	RequiresLogin: true,
}

var cmdStatus = &commands.FullHandler{
	Func: fnStatus,
	Name: "status",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionMisc,
		Description: "Set the GG status text. Without text, clear it",
		Args:        "[_text_]",
	},
	RequiresLogin: true,
}

func getClient(ce *commands.Event) *GaduClient {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in")
		return nil
	}
	client, ok := login.Client.(*GaduClient)
	if !ok {
		ce.Reply("This login is not a GG login")
		return nil
	}
	return client
}

func fnReconnect(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	client.Disconnect()
	client.Connect(ce.Ctx)
	ce.Reply("Reconnecting to GG")
}

func fnSyncContacts(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	sess := client.session()
	if sess == nil {
		ce.Reply("Not connected to GG")
		return
	}
	if err := sess.RequestContactList(ce.Ctx); err != nil {
		ce.Reply("Failed to request the contact list: %v", err)
		return
	}
	ce.Reply("Requested the contact list. Names update when it arrives")
}

func fnOnline(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	if client.session() == nil {
		ce.Reply("Not connected to GG, so the statuses are unknown")
		return
	}
	query := strings.TrimSpace(ce.RawArgs)
	lines := client.statusLines(query)
	switch {
	case len(lines) == 0 && query == "":
		ce.Reply("No contacts are online")
	case len(lines) == 0:
		ce.Reply("No contact matches %q", query)
	default:
		ce.Reply("%s", strings.Join(lines, "\n"))
	}
}

func fnStatus(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	text := strings.TrimSpace(ce.RawArgs)
	meta := client.metadata()
	meta.StatusText = text
	if err := client.login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to save the status text: %v", err)
		return
	}
	if sess := client.session(); sess != nil {
		if err := sess.SetStatus(ce.Ctx, client.main.Config.initialStatus, text); err != nil {
			ce.Reply("Failed to set the status: %v", err)
			return
		}
	}
	if text == "" {
		ce.Reply("Cleared the status text")
	} else {
		ce.Reply("Set the status text")
	}
}

var cmdContactAdd = &commands.FullHandler{
	Func: fnContactAdd,
	Name: "contact-add",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Add a GG number to the GG contact list",
		Args:        "<_number_> [_name_]",
	},
	RequiresLogin: true,
}

var cmdContactRemove = &commands.FullHandler{
	Func: fnContactRemove,
	Name: "contact-remove",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Remove a contact from the GG contact list. The Matrix room stays",
		Args:        "<_number or name_>",
	},
	RequiresLogin: true,
}

var cmdContactRename = &commands.FullHandler{
	Func: fnContactRename,
	Name: "contact-rename",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Change the name of a contact in the GG contact list",
		Args:        "<_number_> <_new name_>",
	},
	RequiresLogin: true,
}

func fnContactAdd(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("Usage: `contact-add <number> [name]`")
		return
	}
	uin, err := gadu.ParseUIN(ce.Args[0])
	if err != nil {
		ce.Reply("%v", err)
		return
	}
	name := strings.TrimSpace(strings.Join(ce.Args[1:], " "))
	if name == "" {
		name = uin.String()
	}
	err = client.editContactList(ce.Ctx, func(b *gadu.ContactBook) error { return b.Add(uin, name) })
	if errors.Is(err, gadu.ErrContactExists) {
		ce.Reply("%s is already in the contact list", uin)
		return
	} else if err != nil {
		ce.Reply("Failed to add the contact: %v", err)
		return
	}
	ce.Reply("Added %s (%s) to the GG contact list", markdownEscaper.Replace(name), uin)
}

func fnContactRemove(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	query := strings.TrimSpace(ce.RawArgs)
	if query == "" {
		ce.Reply("Usage: `contact-remove <number or name>`")
		return
	}
	uin, err := client.findContact(query)
	if err != nil {
		ce.Reply("%v", err)
		return
	}
	name := client.contactName(uin)
	err = client.editContactList(ce.Ctx, func(b *gadu.ContactBook) error { return b.Remove(uin) })
	if errors.Is(err, gadu.ErrContactNotFound) {
		ce.Reply("%s is not in the contact list", uin)
		return
	} else if err != nil {
		ce.Reply("Failed to remove the contact: %v", err)
		return
	}
	ce.Reply("Removed %s (%s) from the GG contact list. The Matrix room stays, leave it if you do not need it", markdownEscaper.Replace(name), uin)
}

func fnContactRename(ce *commands.Event) {
	client := getClient(ce)
	if client == nil {
		return
	}
	if len(ce.Args) < 2 {
		ce.Reply("Usage: `contact-rename <number> <new name>`")
		return
	}
	uin, err := gadu.ParseUIN(ce.Args[0])
	if err != nil {
		ce.Reply("%v", err)
		return
	}
	name := strings.TrimSpace(strings.Join(ce.Args[1:], " "))
	err = client.editContactList(ce.Ctx, func(b *gadu.ContactBook) error { return b.Rename(uin, name) })
	if errors.Is(err, gadu.ErrContactNotFound) {
		ce.Reply("%s is not in the contact list. Add it with `contact-add`", uin)
		return
	} else if err != nil {
		ce.Reply("Failed to rename the contact: %v", err)
		return
	}
	ce.Reply("Renamed %s to %s", uin, markdownEscaper.Replace(name))
}

var cmdBlock = &commands.FullHandler{
	Func: fnBlock,
	Name: "block",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Block a GG contact, so that GG drops their messages",
		Args:        "<_number or name_>",
	},
	RequiresLogin: true,
}

var cmdUnblock = &commands.FullHandler{
	Func: fnUnblock,
	Name: "unblock",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Unblock a GG contact",
		Args:        "<_number or name_>",
	},
	RequiresLogin: true,
}

func fnBlock(ce *commands.Event) {
	setBlocked(ce, true)
}

func fnUnblock(ce *commands.Event) {
	setBlocked(ce, false)
}

func setBlocked(ce *commands.Event, blocked bool) {
	client := getClient(ce)
	if client == nil {
		return
	}
	query := strings.TrimSpace(ce.RawArgs)
	if query == "" {
		ce.Reply("Usage: `%s <number or name>`", ce.Command)
		return
	}
	uin, err := client.findContact(query)
	if err != nil {
		ce.Reply("%v", err)
		return
	}
	name := markdownEscaper.Replace(client.contactName(uin))
	if client.isBlocked(uin) == blocked {
		if blocked {
			ce.Reply("%s (%s) is already blocked", name, uin)
		} else {
			ce.Reply("%s (%s) is not blocked", name, uin)
		}
		return
	}
	err = client.setContactBlocked(ce.Ctx, uin, blocked)
	switch {
	case errors.Is(err, gadu.ErrContactNotFound):
		ce.Reply("%s is not in the contact list", uin)
	case err != nil:
		ce.Reply("Failed to change the block: %v", err)
	case blocked:
		ce.Reply("Blocked %s (%s). GG drops their messages", name, uin)
	default:
		ce.Reply("Unblocked %s (%s)", name, uin)
	}
}

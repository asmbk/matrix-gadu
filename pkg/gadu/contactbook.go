package gadu

import (
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DefaultGroupID is the group of new contacts in the GG 10 contact list.
const DefaultGroupID = "00000000-0000-0000-0000-000000000000"

// IgnoredGroupID is the "Ignorowani" system group. GG clients put blocked contacts in it.
const IgnoredGroupID = "00000000-0000-0000-0000-000000000001"

// ErrContactExists means that the number is already in the contact list.
var ErrContactExists = errors.New("gadu: the number is already in the contact list")

// ErrContactNotFound means that the number is not in the contact list.
var ErrContactNotFound = errors.New("gadu: the number is not in the contact list")

// ContactBook is an editable GG 10 (XML) contact list. It keeps every element
// of the list, also the ones that the bridge does not read (groups, phone
// numbers, phone-only contacts), so that an edit changes only one contact.
type ContactBook struct {
	root *xmlNode
}

// xmlNode is an element with its text and child elements. GG contact lists
// have no attributes, comments or mixed content.
type xmlNode struct {
	Name     string
	Text     string
	Children []*xmlNode
}

func (n *xmlNode) child(name string) *xmlNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (n *xmlNode) childText(name string) string {
	if c := n.child(name); c != nil {
		return c.Text
	}
	return ""
}

// ParseContactBook parses a contact list in the GG 10 format for editing.
func ParseContactBook(data string) (*ContactBook, error) {
	dec := xml.NewDecoder(strings.NewReader(data))
	var stack []*xmlNode
	var root *xmlNode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse GG 10 contact list: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(t.Attr) > 0 {
				return nil, fmt.Errorf("parse GG 10 contact list: unexpected attributes in <%s>", t.Name.Local)
			}
			n := &xmlNode{Name: t.Name.Local}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
	if root == nil || root.Name != "ContactBook" || root.child("Contacts") == nil {
		return nil, errors.New("parse GG 10 contact list: no <ContactBook><Contacts>")
	}
	return &ContactBook{root: root}, nil
}

// String returns the list in the GG 10 format, without white space between elements.
func (b *ContactBook) String() string {
	var sb strings.Builder
	writeNode(&sb, b.root)
	return sb.String()
}

func writeNode(sb *strings.Builder, n *xmlNode) {
	sb.WriteString("<" + n.Name + ">")
	if len(n.Children) == 0 {
		_ = xml.EscapeText(sb, []byte(n.Text))
	}
	for _, c := range n.Children {
		writeNode(sb, c)
	}
	sb.WriteString("</" + n.Name + ">")
}

func (b *ContactBook) contacts() *xmlNode {
	return b.root.child("Contacts")
}

func (b *ContactBook) find(uin UIN) (int, *xmlNode) {
	for i, c := range b.contacts().Children {
		if c.Name != "Contact" {
			continue
		}
		if n, err := ParseUIN(c.childText("GGNumber")); err == nil && n == uin {
			return i, c
		}
	}
	return -1, nil
}

// Contact returns the contact with the given number.
func (b *ContactBook) Contact(uin UIN) (Contact, bool) {
	_, c := b.find(uin)
	if c == nil {
		return Contact{}, false
	}
	return Contact{
		UIN:       uin,
		ShowName:  strings.TrimSpace(c.childText("ShowName")),
		Nickname:  strings.TrimSpace(c.childText("NickName")),
		FirstName: strings.TrimSpace(c.childText("FirstName")),
		LastName:  strings.TrimSpace(c.childText("LastName")),
		Blocked:   c.child("Groups") != nil && hasGroup(c.child("Groups"), IgnoredGroupID),
	}, true
}

func hasGroup(groups *xmlNode, id string) bool {
	for _, g := range groups.Children {
		if g.Name == "GroupId" && strings.EqualFold(strings.TrimSpace(g.Text), id) {
			return true
		}
	}
	return false
}

// Add appends a contact in the default group, the same way GG clients do.
func (b *ContactBook) Add(uin UIN, showName string) error {
	if _, c := b.find(uin); c != nil {
		return ErrContactExists
	}
	guid, err := newGUID()
	if err != nil {
		return err
	}
	text := func(name, value string) *xmlNode { return &xmlNode{Name: name, Text: value} }
	contact := &xmlNode{Name: "Contact", Children: []*xmlNode{
		text("Guid", guid),
		text("GGNumber", uin.String()),
		text("ShowName", showName),
		{Name: "Groups", Children: []*xmlNode{text("GroupId", DefaultGroupID)}},
		{Name: "Avatars"},
		text("FlagBuddy", "true"),
		text("FlagNormal", "true"),
		text("FlagFriend", "true"),
	}}
	list := b.contacts()
	list.Children = append(list.Children, contact)
	return nil
}

// Remove deletes a contact.
func (b *ContactBook) Remove(uin UIN) error {
	i, c := b.find(uin)
	if c == nil {
		return ErrContactNotFound
	}
	list := b.contacts()
	list.Children = append(list.Children[:i], list.Children[i+1:]...)
	return nil
}

// Rename sets the name that GG clients show for a contact.
func (b *ContactBook) Rename(uin UIN, showName string) error {
	_, c := b.find(uin)
	if c == nil {
		return ErrContactNotFound
	}
	if n := c.child("ShowName"); n != nil {
		n.Text = showName
		return nil
	}
	// ShowName follows GGNumber in the lists that GG clients write.
	name := &xmlNode{Name: "ShowName", Text: showName}
	for i, ch := range c.Children {
		if ch.Name == "GGNumber" {
			c.Children = append(c.Children[:i+1], append([]*xmlNode{name}, c.Children[i+1:]...)...)
			return nil
		}
	}
	c.Children = append(c.Children, name)
	return nil
}

// SetBlocked adds the contact to the "Ignorowani" group, or removes it from
// that group. Other groups stay. A contact without any group goes back to the
// default group, because GG clients expect at least one group.
func (b *ContactBook) SetBlocked(uin UIN, blocked bool) error {
	_, c := b.find(uin)
	if c == nil {
		return ErrContactNotFound
	}
	groups := c.child("Groups")
	if groups == nil {
		groups = &xmlNode{Name: "Groups"}
		c.Children = append(c.Children, groups)
	}
	var kept []*xmlNode
	for _, g := range groups.Children {
		if g.Name == "GroupId" && strings.EqualFold(strings.TrimSpace(g.Text), IgnoredGroupID) {
			continue
		}
		kept = append(kept, g)
	}
	if blocked {
		kept = append(kept, &xmlNode{Name: "GroupId", Text: IgnoredGroupID})
	} else if len(kept) == 0 {
		kept = append(kept, &xmlNode{Name: "GroupId", Text: DefaultGroupID})
	}
	groups.Children = kept
	return nil
}

// newGUID returns a random (version 4) GUID in lower case, like the GG clients.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

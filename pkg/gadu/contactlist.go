package gadu

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// Contact is one entry of the GG contact list.
type Contact struct {
	UIN       UIN
	ShowName  string
	Nickname  string
	FirstName string
	LastName  string
	// Blocked is true for contacts in the "Ignorowani" group of GG 10 lists.
	Blocked bool
}

// FullName returns "first last", without extra spaces.
func (c Contact) FullName() string {
	return strings.TrimSpace(c.FirstName + " " + c.LastName)
}

// ParseContactList parses a contact list in the GG 10 (XML) or the GG 7 (text) format.
// It skips entries without a valid GG number.
func ParseContactList(data string) ([]Contact, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "<") {
		return parseContactListXML(trimmed)
	}
	return parseContactList70(trimmed), nil
}

type xmlContactBook struct {
	Contacts []struct {
		GGNumber  string   `xml:"GGNumber"`
		ShowName  string   `xml:"ShowName"`
		NickName  string   `xml:"NickName"`
		FirstName string   `xml:"FirstName"`
		LastName  string   `xml:"LastName"`
		Groups    []string `xml:"Groups>GroupId"`
	} `xml:"Contacts>Contact"`
}

func parseContactListXML(data string) ([]Contact, error) {
	var book xmlContactBook
	if err := xml.Unmarshal([]byte(data), &book); err != nil {
		return nil, fmt.Errorf("parse GG 10 contact list: %w", err)
	}
	contacts := make([]Contact, 0, len(book.Contacts))
	for _, c := range book.Contacts {
		uin, err := ParseUIN(c.GGNumber)
		if err != nil {
			continue
		}
		contacts = append(contacts, Contact{
			UIN:       uin,
			ShowName:  strings.TrimSpace(c.ShowName),
			Nickname:  strings.TrimSpace(c.NickName),
			FirstName: strings.TrimSpace(c.FirstName),
			LastName:  strings.TrimSpace(c.LastName),
			Blocked:   inGroup(c.Groups, IgnoredGroupID),
		})
	}
	return contacts, nil
}

func inGroup(groups []string, id string) bool {
	for _, g := range groups {
		if strings.EqualFold(strings.TrimSpace(g), id) {
			return true
		}
	}
	return false
}

// parseContactList70 parses the GG 7 format. Each line is
// "first;last;nickname;display;phone;group;uin;email;...".
func parseContactList70(data string) []Contact {
	var contacts []Contact
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), ";")
		if len(fields) < 7 {
			continue
		}
		uin, err := ParseUIN(fields[6])
		if err != nil {
			continue
		}
		contacts = append(contacts, Contact{
			UIN:       uin,
			FirstName: fields[0],
			LastName:  fields[1],
			Nickname:  fields[2],
			ShowName:  fields[3],
		})
	}
	return contacts
}

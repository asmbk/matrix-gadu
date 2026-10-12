package gadu

import (
	"errors"
	"strings"
	"testing"
)

const sampleBook = `<ContactBook><Groups><Group><Id>00000000-0000-0000-0000-000000000000</Id><Name>[default]</Name><IsExpanded>true</IsExpanded><IsRemovable>false</IsRemovable></Group></Groups>` +
	`<Contacts><Contact><Guid>11111111-1111-4111-8111-111111111111</Guid><GGNumber>123</GGNumber><ShowName>Kuba &amp; co</ShowName><MobilePhone>500</MobilePhone><Groups><GroupId>00000000-0000-0000-0000-000000000000</GroupId></Groups><Avatars><URL>avatar</URL></Avatars><FlagBuddy>true</FlagBuddy><FlagNormal>true</FlagNormal><FlagFriend>true</FlagFriend></Contact>` +
	`<Contact><Guid>22222222-2222-4222-8222-222222222222</Guid><GGNumber></GGNumber><ShowName>Phone only</ShowName><MobilePhone>600</MobilePhone><Groups><GroupId>sync</GroupId></Groups><Avatars /><FlagBuddy>true</FlagBuddy><FlagNormal>true</FlagNormal><FlagFriend>true</FlagFriend></Contact></Contacts></ContactBook>`

func TestContactBookRoundTrip(t *testing.T) {
	// The sample has a self-closing element, which GG clients do not write.
	want := strings.Replace(sampleBook, "<Avatars />", "<Avatars></Avatars>", 1)
	b, err := ParseContactBook(sampleBook)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != want {
		t.Errorf("round trip changed the list:\n%s", got)
	}
	if c, ok := b.Contact(123); !ok || c.ShowName != "Kuba & co" {
		t.Errorf("Contact(123) = %+v, %v", c, ok)
	}
}

func TestContactBookEdits(t *testing.T) {
	b, err := ParseContactBook(sampleBook)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Add(123, "x"); !errors.Is(err, ErrContactExists) {
		t.Errorf("Add existing = %v", err)
	}
	if err := b.Add(456, "Ola <3"); err != nil {
		t.Fatal(err)
	}
	if err := b.Rename(123, "Jakub"); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(789); !errors.Is(err, ErrContactNotFound) {
		t.Errorf("Remove missing = %v", err)
	}
	out := b.String()
	contacts, err := ParseContactList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 2 || contacts[0].ShowName != "Jakub" || contacts[1].UIN != 456 || contacts[1].ShowName != "Ola <3" {
		t.Errorf("contacts after edit = %+v", contacts)
	}
	for _, keep := range []string{"<MobilePhone>500</MobilePhone>", "<URL>avatar</URL>", "<ShowName>Phone only</ShowName>", "<Name>[default]</Name>", "Ola &lt;3"} {
		if !strings.Contains(out, keep) {
			t.Errorf("edited list lost %s", keep)
		}
	}
	b2, _ := ParseContactBook(out)
	if err := b2.Remove(123); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b2.String(), "<GGNumber>123</GGNumber>") || !strings.Contains(b2.String(), "Phone only") {
		t.Error("Remove deleted the wrong entries")
	}
}

func TestNewGUID(t *testing.T) {
	g, err := newGUID()
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 36 || g[14] != '4' || strings.ToLower(g) != g {
		t.Errorf("guid = %q", g)
	}
}

func TestContactBookSetBlocked(t *testing.T) {
	b, err := ParseContactBook(sampleBook)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetBlocked(123, true); err != nil {
		t.Fatal(err)
	}
	contacts, _ := ParseContactList(b.String())
	if len(contacts) != 1 || !contacts[0].Blocked {
		t.Fatalf("after block: %+v", contacts)
	}
	if c, _ := b.Contact(123); !c.Blocked {
		t.Error("Contact(123) not blocked")
	}
	if !strings.Contains(b.String(), "<GroupId>"+DefaultGroupID+"</GroupId><GroupId>"+IgnoredGroupID+"</GroupId>") {
		t.Errorf("block must keep the other groups: %s", b.String())
	}
	if err := b.SetBlocked(123, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), IgnoredGroupID) {
		t.Error("unblock kept the Ignorowani group")
	}
	if err := b.SetBlocked(999, true); !errors.Is(err, ErrContactNotFound) {
		t.Errorf("block missing = %v", err)
	}
}

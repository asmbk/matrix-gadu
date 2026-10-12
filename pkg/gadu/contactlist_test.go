package gadu

import (
	"reflect"
	"testing"
)

func TestParseContactListXML(t *testing.T) {
	data := `<ContactBook><Groups><Group><Id>00000000-0000-0000-0000-000000000000</Id><Name>Pozostali</Name></Group></Groups>
<Contacts>
<Contact><Guid>a</Guid><GGNumber>1234567</GGNumber><ShowName>Kuba</ShowName><NickName>kf</NickName><FirstName>Jakub</FirstName><LastName>Kowalski</LastName></Contact>
<Contact><Guid>b</Guid><GGNumber></GGNumber><ShowName>Phone only</ShowName></Contact>
<Contact><Guid>c</Guid><GGNumber>42</GGNumber></Contact>
</Contacts></ContactBook>`
	got, err := ParseContactList(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Contact{
		{UIN: 1234567, ShowName: "Kuba", Nickname: "kf", FirstName: "Jakub", LastName: "Kowalski"},
		{UIN: 42},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseContactList70(t *testing.T) {
	data := "Jakub;Kowalski;kf;Kuba;;Znajomi;1234567;;0;;0;;0;\r\nbroken line\n;;;;;;notanumber;\n"
	got, err := ParseContactList(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Contact{{UIN: 1234567, ShowName: "Kuba", Nickname: "kf", FirstName: "Jakub", LastName: "Kowalski"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseUIN(t *testing.T) {
	for in, want := range map[string]UIN{"7654321": 7654321, " gg:123 ": 123} {
		got, err := ParseUIN(in)
		if err != nil || got != want {
			t.Errorf("ParseUIN(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0", "abc", "-1", "99999999999"} {
		if _, err := ParseUIN(in); err == nil {
			t.Errorf("ParseUIN(%q) did not fail", in)
		}
	}
}

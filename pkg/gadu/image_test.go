package gadu

import (
	"reflect"
	"testing"
)

func TestParseRichTextImages(t *testing.T) {
	formats := []byte{
		0, 0, 0x01, // bold at 0
		2, 0, 0x08, 0xff, 0, 0, // colour at 2
		5, 0, 0x80 | 0x08, 1, 2, 3, // colour and image at 5
		0x09, 0x01, 0x10, 0x27, 0, 0, 0xef, 0xbe, 0xad, 0xde,
		7, 0, 0x80, 0x09, 0x01, // truncated image
	}
	want := []ImageRef{{CRC32: 0xdeadbeef, Size: 10000}}
	if got := ParseRichTextImages(formats); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseHTMLImages(t *testing.T) {
	html := `<span>a</span><img name="deadbeef00002710"><img src="x" name="0000000100000002"/>`
	want := []ImageRef{{CRC32: 0xdeadbeef, Size: 10000}, {CRC32: 1, Size: 2}}
	if got := ParseHTMLImages(html); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := MergeImageRefs(want[:1], want); len(got) != 2 {
		t.Errorf("merge = %+v", got)
	}
	if got := StripHTMLImages(html); got != "<span>a</span>" {
		t.Errorf("strip = %q", got)
	}
}

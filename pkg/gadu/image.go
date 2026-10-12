package gadu

import (
	"encoding/binary"
	"regexp"
	"strconv"
)

// Older GG clients (Kadu, Pidgin, the GG 7-10 desktop clients) send images
// with the GG image protocol. The message carries only a reference (CRC32
// and size), and the receiver asks the sender's client for the image.

// ImageRef points to an image in a GG message.
type ImageRef struct {
	CRC32 uint32
	Size  uint32
}

// MaxImageSizeKiB is the largest image that the session accepts. The login
// packet has one byte for it, so 255 KiB is the protocol maximum.
const MaxImageSizeKiB = 255

const (
	fontColor = 0x08
	fontImage = 0x80
)

// ParseRichTextImages returns the image references in the formatting data of
// a GG message: a list of {position uint16, font uint8}, then 3 colour bytes
// if font has the colour flag, then {unknown uint16, size uint32, crc32
// uint32} if font has the image flag. All numbers are little-endian.
func ParseRichTextImages(formats []byte) []ImageRef {
	var refs []ImageRef
	for len(formats) >= 3 {
		font := formats[2]
		formats = formats[3:]
		if font&fontColor != 0 {
			if len(formats) < 3 {
				break
			}
			formats = formats[3:]
		}
		if font&fontImage != 0 {
			if len(formats) < 10 {
				break
			}
			refs = append(refs, ImageRef{
				Size:  binary.LittleEndian.Uint32(formats[2:6]),
				CRC32: binary.LittleEndian.Uint32(formats[6:10]),
			})
			formats = formats[10:]
		}
	}
	return refs
}

// htmlImageRe matches the images that libgadu writes into the XHTML form of a
// message: <img name="CCCCCCCCSSSSSSSS">, with the CRC32 and the size in hex.
var htmlImageRe = regexp.MustCompile(`<img[^>]*\bname="([0-9a-fA-F]{8})([0-9a-fA-F]{8})"`)

// ParseHTMLImages returns the image references in the XHTML form of a GG message.
func ParseHTMLImages(html string) []ImageRef {
	var refs []ImageRef
	for _, m := range htmlImageRe.FindAllStringSubmatch(html, -1) {
		crc, err1 := strconv.ParseUint(m[1], 16, 32)
		size, err2 := strconv.ParseUint(m[2], 16, 32)
		if err1 == nil && err2 == nil {
			refs = append(refs, ImageRef{CRC32: uint32(crc), Size: uint32(size)})
		}
	}
	return refs
}

// MergeImageRefs joins reference lists in order and drops duplicates.
func MergeImageRefs(lists ...[]ImageRef) []ImageRef {
	seen := make(map[ImageRef]bool)
	var out []ImageRef
	for _, list := range lists {
		for _, r := range list {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// htmlImageTagRe matches whole image tags, so that they can be removed from the text.
var htmlImageTagRe = regexp.MustCompile(`<img[^>]*>`)

// StripHTMLImages removes the image tags from the XHTML form of a message.
func StripHTMLImages(html string) string {
	return htmlImageTagRe.ReplaceAllString(html, "")
}

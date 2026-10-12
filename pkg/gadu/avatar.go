package gadu

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// AvatarURLTemplate is the public GG avatar service. %d is the GG number.
var AvatarURLTemplate = "https://avatars.gg.pl/user,%d/s,480x480"

// maxAvatarSize limits the download. GG serves small JPEG files (about 10 KB).
const maxAvatarSize = 2 << 20

// Avatar is a GG avatar image.
type Avatar struct {
	Data []byte
	// Version changes when the contact changes the avatar (the HTTP ETag).
	Version string
}

// FetchAvatar downloads the public avatar of a GG number. It returns nil
// without an error if the number has no avatar.
func FetchAvatar(ctx context.Context, client *http.Client, uin UIN) (*Avatar, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(AvatarURLTemplate, uin), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch GG avatar: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotFound:
		return nil, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("fetch GG avatar: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarSize+1))
	if err != nil {
		return nil, fmt.Errorf("read GG avatar: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > maxAvatarSize {
		return nil, fmt.Errorf("GG avatar is larger than %d bytes", maxAvatarSize)
	}
	version := resp.Header.Get("ETag")
	if version == "" {
		version = fmt.Sprintf("len-%d", len(data))
	}
	return &Avatar{Data: data, Version: version}, nil
}

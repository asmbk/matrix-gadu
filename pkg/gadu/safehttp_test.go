package gadu

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "93.184.216.34": true, "2a00:1450::1": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.17.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false, "224.0.0.1": false,
		"64:ff9b::a00:1": false,
	} {
		if got := IsPublicIP(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsPublicIP(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestSafeClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()
	client := NewSafeHTTPClient(5 * time.Second)
	_, err := FetchMedia(context.Background(), client, MediaLink{Kind: MediaWeb, URL: srv.URL + "/x.png"})
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error = %v, want ErrBlockedAddress", err)
	}
	// A DNS name that resolves to loopback is blocked too.
	_, err = FetchMedia(context.Background(), client, MediaLink{Kind: MediaWeb, URL: "http://localhost:" + srv.URL[len("http://127.0.0.1:"):] + "/x.png"})
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("localhost error = %v, want ErrBlockedAddress", err)
	}
}

package libgadu

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

func TestVersion(t *testing.T) {
	if v := Version(); !strings.HasPrefix(v, "1.") {
		t.Errorf("unexpected libgadu version %q", v)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	for _, st := range []gadu.Status{gadu.StatusAvailable, gadu.StatusFreeForChat, gadu.StatusBusy, gadu.StatusDND, gadu.StatusInvisible, gadu.StatusOffline} {
		for _, descr := range []bool{false, true} {
			if got := fromGGStatus(int(toGGStatus(st, descr))); got != st {
				t.Errorf("status %v (descr %v) came back as %v", st, descr, got)
			}
		}
	}
	// The server adds mask bits to the status of contacts.
	if got := fromGGStatus(int(toGGStatus(gadu.StatusBusy, true)) | 0x4000 | 0x8000); got != gadu.StatusBusy {
		t.Errorf("masked busy status came back as %v", got)
	}
}

func TestCloseBeforeConnect(t *testing.T) {
	s := New(gadu.Config{UIN: 1, Password: "x"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-s.Events(); ok {
		t.Error("events channel is open after Close")
	}
	if err := s.Connect(context.Background()); !errors.Is(err, gadu.ErrClosed) && err == nil {
		t.Errorf("Connect after Close returned %v", err)
	}
	if _, err := s.SendMessage(context.Background(), 2, "x"); !errors.Is(err, gadu.ErrClosed) {
		t.Errorf("SendMessage after Close returned %v", err)
	}
}

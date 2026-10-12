package gadu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchAvatar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user,777/s,480x480":
			w.Header().Set("ETag", "1547048083")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
		case "/user,42/s,480x480":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	old := AvatarURLTemplate
	AvatarURLTemplate = srv.URL + "/user,%d/s,480x480"
	defer func() { AvatarURLTemplate = old }()

	av, err := FetchAvatar(context.Background(), srv.Client(), 777)
	if err != nil || av == nil || av.Version != "1547048083" || len(av.Data) != 3 {
		t.Fatalf("777: %+v, %v", av, err)
	}
	if av, err := FetchAvatar(context.Background(), srv.Client(), 42); av != nil || err != nil {
		t.Errorf("42 (no avatar): %+v, %v", av, err)
	}
	if _, err := FetchAvatar(context.Background(), srv.Client(), 1); err == nil {
		t.Error("HTTP 500 did not fail")
	}
}

package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// happyServer mimics the subset of the Discord REST API the client uses.
func happyServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bot test-token")
		}
		p := r.URL.Path
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/channels"):
			_, _ = w.Write([]byte(`{"id":"chan-1"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/webhooks"):
			_, _ = w.Write([]byte(`{"id":"wh-1","token":"tok-1"}`))
		case r.Method == http.MethodGet && strings.Contains(p, "/guilds/") && strings.HasSuffix(p, "/channels"):
			_, _ = w.Write([]byte(`[{"id":"c-existing","name":"existing-chan"}]`))
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/webhooks"):
			_, _ = w.Write([]byte(`[{"id":"wh-existing","name":"existing-hook","token":"t"}]`))
		case r.Method == http.MethodDelete:
			if strings.Contains(p, "missing") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}
	return httptest.NewServer(http.HandlerFunc(h))
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	t.Setenv("DISCORD_API_BASE", baseURL)
	return NewClient("test-token")
}

func TestCreateChannel(t *testing.T) {
	srv := happyServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	id, err := c.CreateChannel(context.Background(), "guild-1", "orders")
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if id != "chan-1" {
		t.Errorf("channel id = %q, want chan-1", id)
	}
}

func TestCreateWebhook(t *testing.T) {
	srv := happyServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	id, url, err := c.CreateWebhook(context.Background(), "chan-1", "orders")
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if id != "wh-1" {
		t.Errorf("webhook id = %q, want wh-1", id)
	}
	want := srv.URL + "/webhooks/wh-1/tok-1"
	if url != want {
		t.Errorf("webhook url = %q, want %q", url, want)
	}
}

func TestFindChannelByName(t *testing.T) {
	srv := happyServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	id, found, err := c.FindChannelByName(context.Background(), "guild-1", "existing-chan")
	if err != nil {
		t.Fatalf("FindChannelByName: %v", err)
	}
	if !found || id != "c-existing" {
		t.Errorf("got (%q,%v), want (c-existing,true)", id, found)
	}

	_, found, err = c.FindChannelByName(context.Background(), "guild-1", "nope")
	if err != nil {
		t.Fatalf("FindChannelByName(nope): %v", err)
	}
	if found {
		t.Error("found unexpected channel")
	}
}

func TestFindWebhook(t *testing.T) {
	srv := happyServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	id, url, found, err := c.FindWebhook(context.Background(), "chan-1", "existing-hook")
	if err != nil {
		t.Fatalf("FindWebhook: %v", err)
	}
	if !found || id != "wh-existing" {
		t.Errorf("got (%q,%v), want (wh-existing,true)", id, found)
	}
	want := srv.URL + "/webhooks/wh-existing/t"
	if url != want {
		t.Errorf("webhook url = %q, want %q", url, want)
	}
}

func TestDeleteChannel(t *testing.T) {
	srv := happyServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if err := c.DeleteChannel(context.Background(), "chan-1"); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	// 404 must be treated as success.
	if err := c.DeleteChannel(context.Background(), "missing-1"); err != nil {
		t.Errorf("DeleteChannel(404) returned error: %v", err)
	}
}

func TestCreateChannelServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if _, err := c.CreateChannel(context.Background(), "g", "n"); err == nil {
		t.Error("expected error on 500 response")
	}
}

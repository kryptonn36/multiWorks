package websocket

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOrigin(t *testing.T) {
	t.Setenv("FRONTEND_ORIGINS", "")
	for origin, want := range map[string]bool{"http://localhost:5173": true, "https://127.0.0.1:5173": true, "http://localhost:5173.evil.example": false, "http://localhost:5173@evil.example": false, "https://evil.example": false} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", origin)
		if allowedOrigin(req) != want {
			t.Errorf("origin %q", origin)
		}
	}
	t.Setenv("FRONTEND_ORIGINS", "https://app.example")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://app.example")
	if !allowedOrigin(req) {
		t.Fatal("configured origin rejected")
	}
	req.Header.Set("Origin", "http://localhost:5173")
	if allowedOrigin(req) {
		t.Fatal("unconfigured local origin accepted")
	}
}

func TestHubConcurrentDisconnectAndProducers(t *testing.T) {
	h := NewHub()
	id := uuid.New()
	c := &Client{ID: uuid.New(), UserID: uuid.New(), ProjectID: id, Send: make(chan ServerMessage, 1)}
	h.Register(c)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				h.Broadcast(id, ServerMessage{Type: "presence"})
				h.Touch(c)
				h.SetScene(c, "scene")
				h.Presence(id)
				select {
				case <-c.Done:
				case c.Send <- ServerMessage{Type: "replay"}:
				default:
				}
				h.Unregister(c)
			}
		}()
	}
	wg.Wait()
	select {
	case <-c.Done:
	default:
		t.Fatal("client was not stopped")
	}
}

func TestPresenceUsesLatestTabAndExcludesStale(t *testing.T) {
	h := NewHub()
	id, user := uuid.New(), uuid.New()
	first := &Client{ProjectID: id, UserID: user}
	second := &Client{ProjectID: id, UserID: user}
	h.Register(first)
	h.Register(second)
	h.mu.Lock()
	first.LastSeen = time.Now().Add(-time.Second)
	second.SceneID = "latest"
	h.mu.Unlock()
	users := h.Presence(id)
	if len(users) != 1 || users[0].SceneID != "latest" {
		t.Fatal("incorrect multi-tab presence")
	}
	h.mu.Lock()
	first.LastSeen = time.Now().Add(-PresenceTTL - time.Second)
	second.LastSeen = first.LastSeen
	h.mu.Unlock()
	if len(h.Presence(id)) != 0 {
		t.Fatal("stale presence returned")
	}
}

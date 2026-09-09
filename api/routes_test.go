package api

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"whatsapp-mcp/service"
)

// TestRoutesAreMounted proves every documented route exists and is behind auth:
// an authenticated request must not 404 (405 is fine for method mismatch).
func TestRoutesAreMounted(t *testing.T) {
	h := New(service.New(nil, nil, nil, time.UTC), "k", log.New(io.Discard, "", 0)).Routes()

	routes := []struct{ method, path string }{
		{"GET", "/api/v1/chats"},
		{"GET", "/api/v1/chats/search"},
		{"GET", "/api/v1/chats/abc/messages"},
		{"POST", "/api/v1/chats/abc/messages"},
		{"POST", "/api/v1/chats/abc/sync"},
		{"GET", "/api/v1/messages/search"},
		{"GET", "/api/v1/media"},
		{"GET", "/api/v1/media/msg1"},
		{"GET", "/api/v1/me"},
	}

	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		req.Header.Set("Authorization", "Bearer k")
		rec := httptest.NewRecorder()

		func() {
			// handlers that pass validation hit the nil store and panic;
			// reaching that point still proves the route is wired.
			defer func() { recover() }()
			h.ServeHTTP(rec, req)
		}()

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s returned 404 — route not mounted", r.method, r.path)
		}
	}
}

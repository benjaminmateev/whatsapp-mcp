package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"whatsapp-mcp/service"
)

// newTestAPI builds an API with nil service dependencies. Requests that pass
// validation would panic, so tests only cover auth and validation paths.
func newTestAPI() http.Handler {
	svc := service.New(nil, nil, nil, time.UTC)
	return New(svc, "secret", log.New(io.Discard, "", 0)).Routes()
}

func do(t *testing.T, h http.Handler, method, path, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRequired(t *testing.T) {
	h := newTestAPI()

	cases := []struct {
		name, auth string
		want       int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong key", "Bearer wrong", http.StatusUnauthorized},
		{"raw key without Bearer", "secret", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := do(t, h, "GET", "/api/v1/chats/search?q=", c.auth).Code; got != c.want {
				t.Errorf("status = %d, want %d", got, c.want)
			}
		})
	}
}

func TestValidationErrorsAre400(t *testing.T) {
	h := newTestAPI()

	// these reach the service and fail validation before touching the nil store
	cases := []string{
		"/api/v1/chats/search?q=",              // missing search term
		"/api/v1/messages/search?q=&from=",     // neither query nor sender
		"/api/v1/chats/x/messages?before=nope", // unparseable timestamp
		"/api/v1/media?type=bogus",             // invalid media type
	}
	for _, path := range cases {
		rec := do(t, h, "GET", path, "Bearer secret")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", path, rec.Code)
		}

		var body errorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("GET %s returned non-JSON body: %v", path, err)
		}
		if body.Error == "" {
			t.Errorf("GET %s returned empty error message", path)
		}
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	if got := do(t, newTestAPI(), "GET", "/api/v1/nope", "Bearer secret").Code; got != http.StatusNotFound {
		t.Errorf("status = %d, want 404", got)
	}
}

func TestIntParam(t *testing.T) {
	cases := []struct {
		query string
		want  int
	}{
		{"", 50},           // absent -> default
		{"?limit=10", 10},  // valid
		{"?limit=abc", 50}, // unparseable -> default
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/x"+c.query, nil)
		if got := intParam(req, "limit", 50); got != c.want {
			t.Errorf("intParam(%q) = %d, want %d", c.query, got, c.want)
		}
	}
}

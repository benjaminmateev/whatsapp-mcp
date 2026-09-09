package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"whatsapp-mcp/service"
)

// API serves the REST endpoints over a service.Service.
type API struct {
	svc    *service.Service
	apiKey string
	log    *log.Logger
}

// New creates a REST API handler set.
func New(svc *service.Service, apiKey string, logger *log.Logger) *API {
	return &API{svc: svc, apiKey: apiKey, log: logger}
}

// Routes returns the REST routes, mounted under /api/v1.
// Auth is applied to every route.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/chats", a.listChats)
	mux.HandleFunc("GET /api/v1/chats/search", a.findChat)
	mux.HandleFunc("GET /api/v1/chats/{jid}/messages", a.getChatMessages)
	mux.HandleFunc("POST /api/v1/chats/{jid}/messages", a.sendMessage)
	mux.HandleFunc("POST /api/v1/chats/{jid}/sync", a.loadMoreMessages)
	mux.HandleFunc("GET /api/v1/messages/search", a.searchMessages)
	mux.HandleFunc("GET /api/v1/media", a.listMedia)
	mux.HandleFunc("GET /api/v1/media/{message_id}", a.getMedia)
	mux.HandleFunc("GET /api/v1/me", a.getMyInfo)

	return a.authenticate(mux)
}

// authenticate requires "Authorization: Bearer <key>" on every REST request.
// Unlike the MCP endpoint there is no path-based key: REST clients can always
// set a header, and keys in URLs leak into logs.
func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+a.apiKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// errorResponse is the JSON body returned for any non-2xx response.
type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

// writeJSON renders v as JSON with a 200.
func (a *API) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.log.Printf("failed to encode response: %v", err)
	}
}

// fail maps a service error to the right status code. Validation errors are
// 400, missing resources 404, everything else 500 (logged, not echoed, so
// internal details stay out of the response).
func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		a.log.Printf("request failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// intParam reads an integer query parameter, returning def when absent or unparseable.
func intParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (a *API) listChats(w http.ResponseWriter, r *http.Request) {
	chats, err := a.svc.ListChats(intParam(r, "limit", 50))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"chats": newChats(chats)})
}

func (a *API) findChat(w http.ResponseWriter, r *http.Request) {
	chats, _, err := a.svc.FindChat(r.URL.Query().Get("q"))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"chats": newChats(chats)})
}

func (a *API) getChatMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := service.GetChatMessagesParams{
		ChatJID: r.PathValue("jid"),
		Limit:   intParam(r, "limit", 50),
		From:    q.Get("from"),
		Offset:  intParam(r, "offset", 0),
	}

	parseTime := func(name string) (*time.Time, bool) {
		v := q.Get(name)
		if v == "" {
			return nil, true
		}
		t, err := a.svc.ParseTimestamp(v)
		if err != nil {
			a.fail(w, err)
			return nil, false
		}
		return &t, true
	}

	var ok bool
	if params.Before, ok = parseTime("before"); !ok {
		return
	}
	if params.After, ok = parseTime("after"); !ok {
		return
	}

	messages, err := a.svc.GetChatMessages(params)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"messages": newMessages(messages)})
}

// sendMessageRequest is the body of POST /api/v1/chats/{jid}/messages.
type sendMessageRequest struct {
	Text string `json:"text"`
}

func (a *API) sendMessage(w http.ResponseWriter, r *http.Request) {
	var body sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	chatJID := r.PathValue("jid")
	if err := a.svc.SendMessage(r.Context(), chatJID, body.Text); err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"sent": true, "chat_jid": chatJID})
}

// loadMoreRequest is the body of POST /api/v1/chats/{jid}/sync.
type loadMoreRequest struct {
	Count       int   `json:"count"`
	WaitForSync *bool `json:"wait_for_sync"`
}

func (a *API) loadMoreMessages(w http.ResponseWriter, r *http.Request) {
	body := loadMoreRequest{Count: 50}
	// an empty body is valid: it means "sync with defaults"
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	waitForSync := true
	if body.WaitForSync != nil {
		waitForSync = *body.WaitForSync
	}

	messages, err := a.svc.LoadMoreMessages(r.Context(), r.PathValue("jid"), body.Count, waitForSync)
	if err != nil {
		a.fail(w, err)
		return
	}

	if !waitForSync {
		w.WriteHeader(http.StatusAccepted)
		a.writeJSON(w, map[string]any{"syncing": true, "messages": []Message{}})
		return
	}
	a.writeJSON(w, map[string]any{"syncing": false, "messages": newMessages(messages)})
}

func (a *API) searchMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	messages, _, err := a.svc.SearchMessages(q.Get("q"), q.Get("from"), intParam(r, "limit", 50))
	if err != nil {
		a.fail(w, err)
		return
	}
	// search results are ranked newest-first; keep that order
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		out = append(out, newMessage(m))
	}
	a.writeJSON(w, map[string]any{"messages": out})
}

func (a *API) listMedia(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	media, err := a.svc.ListMedia(q.Get("chat_jid"), q.Get("type"), intParam(r, "limit", 50))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"media": newMediaList(media)})
}

// getMedia streams the raw file bytes, so ordinary HTTP clients can consume it
// directly. MCP gets base64 instead because its transport is JSON.
func (a *API) getMedia(w http.ResponseWriter, r *http.Request) {
	media, err := a.svc.GetMedia(r.Context(), r.PathValue("message_id"))
	if err != nil {
		a.fail(w, err)
		return
	}

	w.Header().Set("Content-Type", media.Meta.MimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(media.Data)))
	// quote-escape the filename to keep the header well-formed
	filename := strings.ReplaceAll(media.Meta.FileName, `"`, "")
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	w.Write(media.Data)
}

func (a *API) getMyInfo(w http.ResponseWriter, r *http.Request) {
	info, err := a.svc.GetMyInfo(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, newProfile(info))
}

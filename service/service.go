// Package service holds the transport-independent business logic for WhatsApp
// operations. MCP tools and the REST API are both thin adapters over it: they
// parse their own request formats, call a Service method, and render the
// returned structs in their own way (LLM-readable prose for MCP, JSON for REST).
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"whatsapp-mcp/paths"
	"whatsapp-mcp/storage"
	"whatsapp-mcp/whatsapp"
)

// ErrInvalidInput marks a caller error (bad parameters). Adapters map it to a
// 400 in REST and a tool error in MCP. Anything else is a 500.
var ErrInvalidInput = errors.New("invalid input")

// invalidf builds an ErrInvalidInput with a formatted message.
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

// ErrNotFound marks a missing resource. Adapters map it to a 404 in REST.
var ErrNotFound = errors.New("not found")

// Service exposes WhatsApp operations over the storage and client layers.
type Service struct {
	wa         *whatsapp.Client
	store      *storage.MessageStore
	mediaStore *storage.MediaStore
	timezone   *time.Location
}

// New creates a Service.
func New(wa *whatsapp.Client, store *storage.MessageStore, mediaStore *storage.MediaStore, timezone *time.Location) *Service {
	return &Service{wa: wa, store: store, mediaStore: mediaStore, timezone: timezone}
}

// Timezone returns the configured display timezone.
func (s *Service) Timezone() *time.Location { return s.timezone }

// clamp bounds n to [1, max], substituting def when n is zero or negative.
func clamp(n, def, max int) int {
	if n <= 0 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

// detectPatternType reports whether a query should use GLOB matching.
func detectPatternType(query string) bool {
	return strings.ContainsAny(query, "*?[")
}

// ParseTimestamp converts an ISO 8601 string to a time in the service timezone.
// Supported: "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02".
func (s *Service) ParseTimestamp(v string) (time.Time, error) {
	for _, format := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(format, v, s.timezone); err == nil {
			return t, nil
		}
	}
	return time.Time{}, invalidf("invalid timestamp format: %s (expected ISO 8601 like '2006-01-02T15:04:05' or '2006-01-02')", v)
}

// ListChats returns chats ordered by most recent activity.
func (s *Service) ListChats(limit int) ([]storage.Chat, error) {
	return s.store.ListChats(clamp(limit, 50, 100))
}

// FindChat searches chats by name or JID, using GLOB when the search contains
// wildcards. The bool reports whether pattern matching was used.
func (s *Service) FindChat(search string) ([]storage.Chat, bool, error) {
	if search == "" {
		return nil, false, invalidf("search parameter is required")
	}
	useGlob := detectPatternType(search)
	chats, err := s.store.SearchChatsFiltered(search, useGlob, 100)
	return chats, useGlob, err
}

// GetChatMessagesParams holds the filters for GetChatMessages.
type GetChatMessagesParams struct {
	ChatJID string
	Limit   int
	Before  *time.Time
	After   *time.Time
	From    string // sender JID filter
	Offset  int    // only used when no timestamp/sender filter is set
}

// GetChatMessages retrieves message history for a chat, newest first.
func (s *Service) GetChatMessages(p GetChatMessagesParams) ([]storage.MessageWithNames, error) {
	if p.ChatJID == "" {
		return nil, invalidf("chat_jid parameter is required")
	}
	limit := clamp(p.Limit, 50, 200)

	if p.Before != nil || p.After != nil || p.From != "" {
		return s.store.GetChatMessagesWithNamesFiltered(p.ChatJID, limit, p.Before, p.After, p.From)
	}
	return s.store.GetChatMessagesWithNames(p.ChatJID, limit, p.Offset)
}

// SearchMessages searches messages across all chats by text and/or sender.
// The bool reports whether pattern matching was used.
func (s *Service) SearchMessages(query, from string, limit int) ([]storage.MessageWithNames, bool, error) {
	if query == "" && from == "" {
		return nil, false, invalidf("must provide either 'query' (text to search) or 'from' (sender JID) or both")
	}
	useGlob := detectPatternType(query)
	msgs, err := s.store.SearchMessagesWithNamesFiltered(query, useGlob, from, clamp(limit, 50, 200))
	return msgs, useGlob, err
}

// SendMessage sends a text message to a chat.
func (s *Service) SendMessage(ctx context.Context, chatJID, text string) error {
	if chatJID == "" {
		return invalidf("chat_jid parameter is required")
	}
	if text == "" {
		return invalidf("text parameter is required")
	}
	if !s.wa.IsLoggedIn() {
		return errors.New("WhatsApp is not connected")
	}
	return s.wa.SendTextMessage(ctx, chatJID, text)
}

// LoadMoreMessages fetches older history from WhatsApp servers. When
// waitForSync is false it returns immediately and messages arrive in the
// background, so the returned slice is empty.
func (s *Service) LoadMoreMessages(ctx context.Context, chatJID string, count int, waitForSync bool) ([]storage.MessageWithNames, error) {
	if chatJID == "" {
		return nil, invalidf("chat_jid parameter is required")
	}
	if !s.wa.IsLoggedIn() {
		return nil, errors.New("WhatsApp is not connected")
	}
	return s.wa.RequestHistorySync(ctx, chatJID, clamp(count, 50, 200), waitForSync)
}

// GetMyInfo returns the logged-in user's profile.
func (s *Service) GetMyInfo(ctx context.Context) (*whatsapp.MyInfo, error) {
	if !s.wa.IsLoggedIn() {
		return nil, errors.New("WhatsApp is not connected")
	}
	return s.wa.GetMyInfo(ctx)
}

// mimePrefixForType maps a friendly media type to the MIME prefix used in storage.
func mimePrefixForType(mediaType string) (string, error) {
	switch mediaType {
	case "image":
		return "image/", nil
	case "video":
		return "video/", nil
	case "audio":
		return "audio/", nil
	case "document":
		return "application/", nil
	case "sticker":
		return "image/webp", nil
	case "":
		return "", nil
	default:
		return "", invalidf("invalid media_type '%s': must be one of: image, video, audio, document, sticker", mediaType)
	}
}

// ListMedia lists media metadata, optionally filtered by chat and/or type.
func (s *Service) ListMedia(chatJID, mediaType string, limit int) ([]storage.MediaMetadata, error) {
	mimePrefix, err := mimePrefixForType(mediaType)
	if err != nil {
		return nil, err
	}
	return s.mediaStore.ListMedia(chatJID, mimePrefix, clamp(limit, 50, 200))
}

// Media is a media file's content plus its metadata.
type Media struct {
	Meta *storage.MediaMetadata
	Data []byte
}

// GetMedia returns a media file's bytes, downloading on demand if needed.
func (s *Service) GetMedia(ctx context.Context, messageID string) (*Media, error) {
	if messageID == "" {
		return nil, invalidf("message_id parameter is required")
	}

	meta, err := s.mediaStore.GetMediaMetadata(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get media metadata: %w", err)
	}
	if meta == nil {
		return nil, fmt.Errorf("%w: no media for message ID %s", ErrNotFound, messageID)
	}

	if meta.DownloadStatus != "downloaded" {
		if meta.DownloadStatus == "expired" {
			return nil, invalidf("media expired and can no longer be downloaded. File: %s (%s, %s)",
				meta.FileName, meta.MimeType, FormatFileSize(meta.FileSize))
		}
		relPath, err := s.wa.DownloadMediaFromMetadata(ctx, meta)
		if err != nil {
			return nil, fmt.Errorf("failed to download media: %w", err)
		}
		meta.FilePath = relPath
		meta.DownloadStatus = "downloaded"
	}

	absPath, err := resolveMediaPath(meta.FilePath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read media file: %w", err)
	}

	return &Media{Meta: meta, Data: data}, nil
}

// resolveMediaPath validates that a stored relative path stays inside the media
// directory and returns its absolute form. This guards against path traversal
// via a crafted stored path.
func resolveMediaPath(relPath string) (string, error) {
	cleanPath := filepath.Clean(relPath)
	if strings.Contains(cleanPath, "..") {
		return "", invalidf("invalid file path")
	}

	mediaDir, err := filepath.Abs(paths.DataMediaDir)
	if err != nil {
		return "", errors.New("failed to resolve media directory")
	}
	absPath, err := filepath.Abs(paths.GetMediaPath(cleanPath))
	if err != nil {
		return "", errors.New("failed to resolve file path")
	}
	if !strings.HasPrefix(absPath, mediaDir+string(filepath.Separator)) && absPath != mediaDir {
		return "", invalidf("invalid file path: outside media directory")
	}
	return absPath, nil
}

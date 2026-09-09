// Package api exposes the WhatsApp service over a plain JSON REST interface,
// for callers that do not speak MCP (automation tools, scripts, mobile apps).
// It is a thin adapter: all logic lives in the service package.
package api

import (
	"time"

	"whatsapp-mcp/service"
	"whatsapp-mcp/storage"
	"whatsapp-mcp/whatsapp"
)

// Chat is the JSON representation of a conversation.
type Chat struct {
	JID             string    `json:"jid"`
	Name            string    `json:"name"`
	PushName        string    `json:"push_name,omitempty"`
	ContactName     string    `json:"contact_name,omitempty"`
	IsGroup         bool      `json:"is_group"`
	LastMessageTime time.Time `json:"last_message_time"`
	UnreadCount     int       `json:"unread_count"`
}

func newChat(c storage.Chat) Chat {
	return Chat{
		JID:             c.JID,
		Name:            service.DisplayName(c),
		PushName:        c.PushName,
		ContactName:     c.ContactName,
		IsGroup:         c.IsGroup,
		LastMessageTime: c.LastMessageTime,
		UnreadCount:     c.UnreadCount,
	}
}

func newChats(in []storage.Chat) []Chat {
	out := make([]Chat, 0, len(in))
	for _, c := range in {
		out = append(out, newChat(c))
	}
	return out
}

// Media is the JSON representation of a media attachment's metadata.
type Media struct {
	MessageID      string `json:"message_id"`
	FileName       string `json:"file_name"`
	MimeType       string `json:"mime_type"`
	FileSize       int64  `json:"file_size"`
	FileSizeHuman  string `json:"file_size_human"`
	Width          *int   `json:"width,omitempty"`
	Height         *int   `json:"height,omitempty"`
	Duration       *int   `json:"duration_seconds,omitempty"`
	DownloadStatus string `json:"download_status"`
	DownloadError  string `json:"download_error,omitempty"`
}

func newMedia(m storage.MediaMetadata) Media {
	return Media{
		MessageID:      m.MessageID,
		FileName:       m.FileName,
		MimeType:       m.MimeType,
		FileSize:       m.FileSize,
		FileSizeHuman:  service.FormatFileSize(m.FileSize),
		Width:          m.Width,
		Height:         m.Height,
		Duration:       m.Duration,
		DownloadStatus: m.DownloadStatus,
		DownloadError:  m.DownloadError,
	}
}

func newMediaList(in []storage.MediaMetadata) []Media {
	out := make([]Media, 0, len(in))
	for _, m := range in {
		out = append(out, newMedia(m))
	}
	return out
}

// Message is the JSON representation of a chat message.
type Message struct {
	ID          string    `json:"id"`
	ChatJID     string    `json:"chat_jid"`
	ChatName    string    `json:"chat_name,omitempty"`
	SenderJID   string    `json:"sender_jid"`
	SenderName  string    `json:"sender_name"`
	Text        string    `json:"text"`
	Timestamp   time.Time `json:"timestamp"`
	IsFromMe    bool      `json:"is_from_me"`
	MessageType string    `json:"message_type,omitempty"`
	ReplyToID   string    `json:"reply_to_id,omitempty"`
	Media       *Media    `json:"media,omitempty"`
}

func newMessage(m storage.MessageWithNames) Message {
	msg := Message{
		ID:          m.ID,
		ChatJID:     m.ChatJID,
		ChatName:    m.ChatName,
		SenderJID:   m.SenderJID,
		SenderName:  service.SenderDisplayName(m),
		Text:        m.Text,
		Timestamp:   m.Timestamp,
		IsFromMe:    m.IsFromMe,
		MessageType: m.MessageType,
		ReplyToID:   m.ReplyToID,
	}
	if m.MediaMetadata != nil {
		media := newMedia(*m.MediaMetadata)
		msg.Media = &media
	}
	return msg
}

// newMessages converts to JSON messages in oldest-first order, matching how
// conversations read. The store returns newest-first.
func newMessages(in []storage.MessageWithNames) []Message {
	out := make([]Message, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, newMessage(in[i]))
	}
	return out
}

// Profile is the JSON representation of the logged-in user.
type Profile struct {
	JID          string `json:"jid"`
	PushName     string `json:"push_name,omitempty"`
	Status       string `json:"status,omitempty"`
	BusinessName string `json:"business_name,omitempty"`
	PictureID    string `json:"picture_id,omitempty"`
	PictureURL   string `json:"picture_url,omitempty"`
}

func newProfile(i *whatsapp.MyInfo) Profile {
	return Profile{
		JID:          i.JID,
		PushName:     i.PushName,
		Status:       i.Status,
		BusinessName: i.BusinessName,
		PictureID:    i.PictureID,
		PictureURL:   i.PictureURL,
	}
}

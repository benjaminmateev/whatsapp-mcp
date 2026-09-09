package service

import (
	"fmt"
	"time"

	"whatsapp-mcp/storage"
)

// DisplayName returns the best available name for a chat.
// Priority: ContactName > PushName > JID.
func DisplayName(chat storage.Chat) string {
	if chat.ContactName != "" {
		return chat.ContactName
	}
	if chat.PushName != "" {
		return chat.PushName
	}
	return chat.JID
}

// SenderDisplayName returns the best available name for a message sender.
// Priority: ContactName > PushName > JID.
func SenderDisplayName(msg storage.MessageWithNames) string {
	if msg.SenderContactName != "" {
		return msg.SenderContactName
	}
	if msg.SenderPushName != "" {
		return msg.SenderPushName
	}
	return msg.SenderJID
}

// ToLocalTime converts a timestamp to the configured timezone.
func (s *Service) ToLocalTime(t time.Time) time.Time { return t.In(s.timezone) }

// FormatDateTime formats a timestamp in the configured timezone.
func (s *Service) FormatDateTime(t time.Time) string {
	return s.ToLocalTime(t).Format("2006-01-02 15:04:05")
}

// FormatTime formats a timestamp in the configured timezone, time only.
func (s *Service) FormatTime(t time.Time) string {
	return s.ToLocalTime(t).Format("15:04:05")
}

// FormatFileSize converts bytes to a human-readable size string.
func FormatFileSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// FormatDimensions returns a "WxH" string, or "" when either is unset.
func FormatDimensions(width, height *int) string {
	if width != nil && height != nil {
		return fmt.Sprintf("%dx%d", *width, *height)
	}
	return ""
}

// FormatDuration converts seconds to MM:SS, or "" when unset.
func FormatDuration(seconds *int) string {
	if seconds == nil {
		return ""
	}
	s := *seconds
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

package service

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClamp(t *testing.T) {
	cases := []struct{ n, def, max, want int }{
		{0, 50, 200, 50},    // unset -> default
		{-5, 50, 200, 50},   // negative -> default
		{10, 50, 200, 10},   // in range -> unchanged
		{500, 50, 200, 200}, // over max -> capped
	}
	for _, c := range cases {
		if got := clamp(c.n, c.def, c.max); got != c.want {
			t.Errorf("clamp(%d,%d,%d) = %d, want %d", c.n, c.def, c.max, got, c.want)
		}
	}
}

func TestDetectPatternType(t *testing.T) {
	for _, q := range []string{"hel*o", "h?llo", "[abc]"} {
		if !detectPatternType(q) {
			t.Errorf("detectPatternType(%q) = false, want true", q)
		}
	}
	if detectPatternType("plain text") {
		t.Error("detectPatternType(\"plain text\") = true, want false")
	}
}

func TestMimePrefixForType(t *testing.T) {
	cases := map[string]string{
		"image": "image/", "video": "video/", "audio": "audio/",
		"document": "application/", "sticker": "image/webp", "": "",
	}
	for in, want := range cases {
		got, err := mimePrefixForType(in)
		if err != nil {
			t.Errorf("mimePrefixForType(%q) unexpected error: %v", in, err)
		}
		if got != want {
			t.Errorf("mimePrefixForType(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := mimePrefixForType("bogus"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("mimePrefixForType(\"bogus\") error = %v, want ErrInvalidInput", err)
	}
}

func TestResolveMediaPathRejectsTraversal(t *testing.T) {
	// a stored path escaping the media dir must be refused, not read
	for _, p := range []string{"../../etc/passwd", "sub/../../../etc/passwd"} {
		if _, err := resolveMediaPath(p); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("resolveMediaPath(%q) error = %v, want ErrInvalidInput", p, err)
		}
	}

	got, err := resolveMediaPath("chat/photo.jpg")
	if err != nil {
		t.Fatalf("resolveMediaPath on a valid path failed: %v", err)
	}
	if !strings.HasSuffix(got, "chat/photo.jpg") {
		t.Errorf("resolveMediaPath returned %q, want it to end in chat/photo.jpg", got)
	}
}

func TestParseTimestamp(t *testing.T) {
	s := &Service{timezone: time.UTC}

	for _, in := range []string{"2026-01-02T15:04:05", "2026-01-02 15:04:05", "2026-01-02"} {
		if _, err := s.ParseTimestamp(in); err != nil {
			t.Errorf("ParseTimestamp(%q) unexpected error: %v", in, err)
		}
	}

	if _, err := s.ParseTimestamp("not-a-date"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ParseTimestamp(\"not-a-date\") error = %v, want ErrInvalidInput", err)
	}

	// the parsed time must land in the service timezone, not UTC by accident
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	got, err := (&Service{timezone: berlin}).ParseTimestamp("2026-01-02T15:04:05")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Hour() != 15 || got.Location().String() != "Europe/Berlin" {
		t.Errorf("ParseTimestamp = %v, want 15:04:05 in Europe/Berlin", got)
	}
}

// Validation must reject bad input before any store or WhatsApp call, so these
// run safely against a Service with nil dependencies.
func TestValidationRejectsBadInput(t *testing.T) {
	s := &Service{timezone: time.UTC}

	if _, _, err := s.FindChat(""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("FindChat(\"\") error = %v, want ErrInvalidInput", err)
	}
	if _, err := s.GetChatMessages(GetChatMessagesParams{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("GetChatMessages with no JID error = %v, want ErrInvalidInput", err)
	}
	if _, _, err := s.SearchMessages("", "", 10); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SearchMessages with no query or sender error = %v, want ErrInvalidInput", err)
	}
	if _, err := s.GetMedia(t.Context(), ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("GetMedia(\"\") error = %v, want ErrInvalidInput", err)
	}
	if err := s.SendMessage(t.Context(), "", "hi"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SendMessage with no JID error = %v, want ErrInvalidInput", err)
	}
	if err := s.SendMessage(t.Context(), "x@s.whatsapp.net", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SendMessage with no text error = %v, want ErrInvalidInput", err)
	}
	if _, err := s.LoadMoreMessages(t.Context(), "", 10, true); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("LoadMoreMessages with no JID error = %v, want ErrInvalidInput", err)
	}
}

func TestFormatFileSize(t *testing.T) {
	cases := map[int64]string{
		512: "512 B", 2048: "2.00 KB", 5 * 1024 * 1024: "5.00 MB", 3 * 1024 * 1024 * 1024: "3.00 GB",
	}
	for in, want := range cases {
		if got := FormatFileSize(in); got != want {
			t.Errorf("FormatFileSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDimensionsAndDuration(t *testing.T) {
	w, h := 1920, 1080
	if got := FormatDimensions(&w, &h); got != "1920x1080" {
		t.Errorf("FormatDimensions = %q, want 1920x1080", got)
	}
	if got := FormatDimensions(nil, &h); got != "" {
		t.Errorf("FormatDimensions with nil width = %q, want empty", got)
	}

	d := 125
	if got := FormatDuration(&d); got != "2:05" {
		t.Errorf("FormatDuration(125) = %q, want 2:05", got)
	}
	if got := FormatDuration(nil); got != "" {
		t.Errorf("FormatDuration(nil) = %q, want empty", got)
	}
}

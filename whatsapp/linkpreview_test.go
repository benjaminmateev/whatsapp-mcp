package whatsapp

import (
	"context"
	"testing"
)

func TestOGTags(t *testing.T) {
	html := `<meta property="og:title" content="Ben on Instagram: &quot;Happy Tunes&quot;" />
	         <meta name="description" content="ignored, not og" />
	         <meta property='og:image' content='https://x/y.jpg' />`
	og := ogTags(html)
	if og["og:title"] != `Ben on Instagram: "Happy Tunes"` {
		t.Fatalf("title unescape failed: %q", og["og:title"])
	}
	if og["og:image"] != "https://x/y.jpg" {
		t.Fatalf("single-quoted attr failed: %q", og["og:image"])
	}
	if _, ok := og["description"]; ok {
		t.Fatal("non-og tag leaked in")
	}
}

func TestURLExtraction(t *testing.T) {
	got := urlRe.FindString("see https://example.com/p/AbC_1/ and more")
	if got != "https://example.com/p/AbC_1/" {
		t.Fatalf("got %q", got)
	}
}

// hits the network; skipped with -short
func TestInstagramPreview(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	body, err := fetch(context.Background(), "https://www.instagram.com/p/DdO_MqzMyXT/", previewMaxHTML)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	og := ogTags(string(body))
	if og["og:title"] == "" || og["og:image"] == "" {
		t.Fatalf("missing og tags: %+v", og)
	}
	raw, err := fetch(context.Background(), og["og:image"], previewMaxImage)
	if err != nil {
		t.Fatalf("image fetch: %v", err)
	}
	thumb, w, h, err := thumbnail(raw)
	if err != nil {
		t.Fatalf("thumbnail: %v", err)
	}
	t.Logf("title=%.60s...", og["og:title"])
	t.Logf("thumb %dx%d, %d bytes", w, h, len(thumb))
	if len(thumb) > 100<<10 {
		t.Errorf("thumbnail too big for inline metadata: %d bytes", len(thumb))
	}
}

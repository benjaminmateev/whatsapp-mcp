package whatsapp

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"whatsapp-mcp/storage"
)

// WhatsApp generates link previews in the sending client, so a plain text
// message with a URL arrives without one. This builds the same metadata
// (title, description, thumbnail) and attaches it to an ExtendedTextMessage.

const (
	previewUserAgent = "Mozilla/5.0 (compatible; WhatsApp/2.23)"
	previewMaxHTML   = 512 << 10 // og tags live in <head>; no need to read further
	previewMaxImage  = 8 << 20
	previewThumbMax  = 320 // px on the long edge, matching WhatsApp's own thumbnails
)

var (
	urlRe  = regexp.MustCompile(`https?://[^\s<>"']+`)
	metaRe = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
	attrRe = regexp.MustCompile(`(?is)(property|name|content)\s*=\s*"([^"]*)"|(property|name|content)\s*=\s*'([^']*)'`)
)

// ogTags pulls the OpenGraph fields we care about out of a page's HTML.
func ogTags(body string) map[string]string {
	out := map[string]string{}
	for _, tag := range metaRe.FindAllString(body, -1) {
		var key, val string
		for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
			name, value := m[1], m[2]
			if name == "" {
				name, value = m[3], m[4]
			}
			switch strings.ToLower(name) {
			case "property", "name":
				key = strings.ToLower(value)
			case "content":
				val = value
			}
		}
		if strings.HasPrefix(key, "og:") && val != "" {
			if _, seen := out[key]; !seen {
				out[key] = html.UnescapeString(val)
			}
		}
	}
	return out
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", previewUserAgent)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// thumbnail downscales to a JPEG small enough to ride along in the message.
func thumbnail(raw []byte) ([]byte, uint32, uint32, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > previewThumbMax || h > previewThumbMax {
		if w >= h {
			h = h * previewThumbMax / w
			w = previewThumbMax
		} else {
			w = w * previewThumbMax / h
			h = previewThumbMax
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), uint32(w), uint32(h), nil
}

// SendLinkMessage sends text whose first URL carries a rendered link preview.
// A page that can't be fetched still sends: the text goes out without a card.
func (c *Client) SendLinkMessage(ctx context.Context, chatJID, text string) error {
	targetJID, err := types.ParseJID(chatJID)
	if err != nil {
		return err
	}

	url := urlRe.FindString(text)
	if url == "" {
		return fmt.Errorf("no URL found in text")
	}

	ext := &waE2E.ExtendedTextMessage{
		Text:        proto.String(text),
		MatchedText: proto.String(url),
	}

	if body, err := fetch(ctx, url, previewMaxHTML); err == nil {
		og := ogTags(string(body))
		if t := og["og:title"]; t != "" {
			ext.Title = proto.String(t)
		}
		if d := og["og:description"]; d != "" {
			ext.Description = proto.String(d)
		}
		if img := og["og:image"]; img != "" {
			if raw, err := fetch(ctx, img, previewMaxImage); err == nil {
				if thumb, w, h, err := thumbnail(raw); err == nil {
					ext.JPEGThumbnail = thumb
					ext.ThumbnailWidth = proto.Uint32(w)
					ext.ThumbnailHeight = proto.Uint32(h)
					ext.PreviewType = waE2E.ExtendedTextMessage_IMAGE.Enum()
				}
			}
		}
	}

	resp, err := c.wa.SendMessage(ctx, targetJID, &waE2E.Message{ExtendedTextMessage: ext})
	if err != nil {
		return err
	}

	c.store.SaveMessage(storage.Message{
		ID:          resp.ID,
		ChatJID:     chatJID,
		SenderJID:   resp.Sender.String(),
		Text:        text,
		Timestamp:   resp.Timestamp,
		IsFromMe:    true,
		MessageType: "text",
	})

	return nil
}

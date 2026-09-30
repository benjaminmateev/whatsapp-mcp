package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"
	"whatsapp-mcp/storage"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// videoMeta is what the chat bubble needs before the video is downloaded:
// dimensions for the frame, duration for the badge, a JPEG for the poster.
// Every field is optional; WhatsApp plays the video without them, it just
// shows a grey box with no length until the recipient taps it.
type videoMeta struct {
	Width     uint32
	Height    uint32
	Seconds   uint32
	Thumbnail []byte
}

// probeVideo reads dimensions, duration and a poster frame with ffprobe and
// ffmpeg. Missing tools or a failed probe are not errors: the send goes
// ahead with whatever was found.
func probeVideo(ctx context.Context, path string) videoMeta {
	var meta videoMeta
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if ffprobe, err := exec.LookPath("ffprobe"); err == nil {
		out, err := exec.CommandContext(ctx, ffprobe,
			"-v", "error",
			"-select_streams", "v:0",
			"-show_entries", "stream=width,height:format=duration",
			"-of", "json", path).Output()
		if err == nil {
			var parsed struct {
				Streams []struct {
					Width  uint32 `json:"width"`
					Height uint32 `json:"height"`
				} `json:"streams"`
				Format struct {
					Duration string `json:"duration"`
				} `json:"format"`
			}
			if json.Unmarshal(out, &parsed) == nil {
				if len(parsed.Streams) > 0 {
					meta.Width = parsed.Streams[0].Width
					meta.Height = parsed.Streams[0].Height
				}
				if d, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil && d > 0 {
					meta.Seconds = uint32(math.Ceil(d))
				}
			}
		}
	}

	if ffmpeg, err := exec.LookPath("ffmpeg"); err == nil {
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, ffmpeg,
			"-v", "error",
			"-i", path,
			"-frames:v", "1",
			"-vf", "scale=200:-2",
			"-f", "image2", "-c:v", "mjpeg", "-q:v", "5",
			"pipe:1")
		cmd.Stdout = &buf
		if cmd.Run() == nil && buf.Len() > 0 {
			meta.Thumbnail = buf.Bytes()
		}
	}

	return meta
}

// SendVideoMessage uploads an MP4 and sends it to a chat with an optional caption.
func (c *Client) SendVideoMessage(ctx context.Context, chatJID string, videoPath string, caption string) error {
	targetJID, err := types.ParseJID(chatJID)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(videoPath)
	if err != nil {
		return fmt.Errorf("failed to read video: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("video file is empty")
	}

	// WhatsApp clients only play MP4 inline. A .mov or .webm would upload
	// fine and then fail on the recipient's phone, so refuse it here.
	mimeType := http.DetectContentType(data)
	if mimeType != "video/mp4" {
		return fmt.Errorf("file is not an MP4 video (detected %s); convert it first, e.g. ffmpeg -i in.mov -c:v libx264 -c:a aac out.mp4", mimeType)
	}

	meta := probeVideo(ctx, videoPath)

	uploaded, err := c.wa.Upload(ctx, data, whatsmeow.MediaVideo)
	if err != nil {
		return fmt.Errorf("failed to upload video: %w", err)
	}

	vid := &waE2E.VideoMessage{
		Mimetype:      proto.String(mimeType),
		URL:           proto.String(uploaded.URL),
		DirectPath:    proto.String(uploaded.DirectPath),
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
	}
	if caption != "" {
		vid.Caption = proto.String(caption)
	}
	if meta.Width > 0 && meta.Height > 0 {
		vid.Width = proto.Uint32(meta.Width)
		vid.Height = proto.Uint32(meta.Height)
	}
	if meta.Seconds > 0 {
		vid.Seconds = proto.Uint32(meta.Seconds)
	}
	if len(meta.Thumbnail) > 0 {
		vid.JPEGThumbnail = meta.Thumbnail
	}

	resp, err := c.wa.SendMessage(ctx, targetJID, &waE2E.Message{VideoMessage: vid})
	if err != nil {
		return err
	}

	c.store.SaveMessage(storage.Message{
		ID:          resp.ID,
		ChatJID:     chatJID,
		SenderJID:   resp.Sender.String(),
		Text:        caption,
		Timestamp:   resp.Timestamp,
		IsFromMe:    true,
		MessageType: "video",
	})

	return nil
}

package whatsapp

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"whatsapp-mcp/storage"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// maxDocumentBytes caps what we read into memory for one send. WhatsApp itself
// accepts documents up to 2 GB, but the upload is built from a single in-memory
// buffer here, so a sane ceiling keeps one call from swallowing the process.
const maxDocumentBytes = 100 << 20 // 100 MB

// SendDocumentMessage uploads any file (PDF, spreadsheet, archive, ...) and sends
// it as a WhatsApp document, which the recipient sees with its file name and a
// download button rather than inline.
//
// fileName is what the recipient sees; empty means the file's own base name.
// The MIME type comes from the extension first, because content sniffing reports
// .docx/.xlsx/.pptx as application/zip and the recipient's phone would then offer
// the wrong app to open them.
func (c *Client) SendDocumentMessage(ctx context.Context, chatJID, filePath, caption, fileName string) error {
	targetJID, err := types.ParseJID(chatJID)
	if err != nil {
		return err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to read document: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", filePath)
	}
	if info.Size() == 0 {
		return fmt.Errorf("document is empty")
	}
	if info.Size() > maxDocumentBytes {
		return fmt.Errorf("document is %d MB, over the %d MB limit for this tool",
			info.Size()>>20, maxDocumentBytes>>20)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read document: %w", err)
	}

	if fileName == "" {
		fileName = filepath.Base(filePath)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	// Strip parameters ("text/plain; charset=utf-8"): WhatsApp expects the bare type.
	if i := strings.Index(mimeType, ";"); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}

	uploaded, err := c.wa.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("failed to upload document: %w", err)
	}

	title := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	doc := &waE2E.DocumentMessage{
		Mimetype:      proto.String(mimeType),
		Title:         proto.String(title),
		FileName:      proto.String(fileName),
		URL:           proto.String(uploaded.URL),
		DirectPath:    proto.String(uploaded.DirectPath),
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
	}
	if caption != "" {
		doc.Caption = proto.String(caption)
	}

	resp, err := c.wa.SendMessage(ctx, targetJID, &waE2E.Message{DocumentMessage: doc})
	if err != nil {
		return err
	}

	text := caption
	if text == "" {
		text = fileName
	}
	c.store.SaveMessage(storage.Message{
		ID:          resp.ID,
		ChatJID:     chatJID,
		SenderJID:   resp.Sender.String(),
		Text:        text,
		Timestamp:   resp.Timestamp,
		IsFromMe:    true,
		MessageType: "document",
	})

	return nil
}

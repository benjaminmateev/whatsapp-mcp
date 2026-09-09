package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"whatsapp-mcp/service"
	"whatsapp-mcp/storage"

	"github.com/mark3labs/mcp-go/mcp"
)

// writeMediaLine appends the "📎 filename (type, size)" line plus download
// status for a message's attached media.
func writeMediaLine(result *strings.Builder, meta *storage.MediaMetadata, withResourceURI bool, messageID string) {
	fmt.Fprintf(result, "   📎 %s (%s, %s)",
		meta.FileName, meta.MimeType, service.FormatFileSize(meta.FileSize))

	if dims := service.FormatDimensions(meta.Width, meta.Height); dims != "" {
		fmt.Fprintf(result, ", %s", dims)
	}
	if dur := service.FormatDuration(meta.Duration); dur != "" {
		fmt.Fprintf(result, ", %s", dur)
	}

	switch meta.DownloadStatus {
	case "downloaded":
		result.WriteString(" [Downloaded]")
		if withResourceURI {
			fmt.Fprintf(result, "\n   Resource: whatsapp://media/%s", messageID)
		}
	case "pending":
		result.WriteString(" [Not downloaded]")
	case "failed":
		result.WriteString(" [Download failed]")
	case "expired":
		result.WriteString(" [Expired]")
	}
	result.WriteString("\n")
}

// writeChatLine appends a numbered chat entry with its JID and name details.
func writeChatLine(result *strings.Builder, i int, chat storage.Chat) {
	chatType := "DM"
	if chat.IsGroup {
		chatType = "Group"
	}

	fmt.Fprintf(result, "%d. [%s] %s\n", i+1, chatType, service.DisplayName(chat))
	fmt.Fprintf(result, "   JID: %s\n", chat.JID)
	if chat.ContactName != "" && chat.PushName != "" && chat.ContactName != chat.PushName {
		fmt.Fprintf(result, "   (Contact: %s, Push: %s)\n", chat.ContactName, chat.PushName)
	}
}

// writeConversation appends messages oldest-first in transcript form.
func (m *MCPServer) writeConversation(result *strings.Builder, messages []storage.MessageWithNames, withResourceURI bool) {
	for i := len(messages) - 1; i >= 0; i-- { // reverse to show oldest first
		msg := messages[i]
		sender := service.SenderDisplayName(msg)

		direction := "←"
		if msg.IsFromMe {
			direction = "→"
			sender = "You"
		}

		fmt.Fprintf(result, "[%s] %s %s: %s\n",
			m.svc.FormatTime(msg.Timestamp), direction, sender, msg.Text)

		if msg.MediaMetadata != nil {
			writeMediaLine(result, msg.MediaMetadata, withResourceURI, msg.ID)
		}
	}
}

// handleListChats handles the list_chats tool request.
func (m *MCPServer) handleListChats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chats, err := m.svc.ListChats(int(request.GetFloat("limit", 50.0)))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list chats: %v", err)), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d chats:\n\n", len(chats))

	for i, chat := range chats {
		writeChatLine(&result, i, chat)
		fmt.Fprintf(&result, "   Last message: %s\n", m.svc.FormatDateTime(chat.LastMessageTime))
		if chat.UnreadCount > 0 {
			fmt.Fprintf(&result, "   Unread: %d\n", chat.UnreadCount)
		}
		result.WriteString("\n")
	}

	return mcp.NewToolResultText(result.String()), nil
}

// handleGetChatMessages handles the get_chat_messages tool request.
func (m *MCPServer) handleGetChatMessages(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := service.GetChatMessagesParams{
		ChatJID: request.GetString("chat_jid", ""),
		Limit:   int(request.GetFloat("limit", 50.0)),
		From:    request.GetString("from", ""),
		Offset:  int(request.GetFloat("offset", 0.0)),
	}

	if v := request.GetString("before_timestamp", ""); v != "" {
		t, err := m.svc.ParseTimestamp(v)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid before_timestamp: %v", err)), nil
		}
		params.Before = &t
	}
	if v := request.GetString("after_timestamp", ""); v != "" {
		t, err := m.svc.ParseTimestamp(v)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid after_timestamp: %v", err)), nil
		}
		params.After = &t
	}

	messages, err := m.svc.GetChatMessages(params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get messages: %v", err)), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Retrieved %d messages from chat %s", len(messages), params.ChatJID)
	if params.From != "" {
		fmt.Fprintf(&result, " (filtered by sender: %s)", params.From)
	}
	if params.Before != nil {
		fmt.Fprintf(&result, " (before: %s)", m.svc.FormatDateTime(*params.Before))
	}
	if params.After != nil {
		fmt.Fprintf(&result, " (after: %s)", m.svc.FormatDateTime(*params.After))
	}
	result.WriteString(":\n\n")

	m.writeConversation(&result, messages, true)

	return mcp.NewToolResultText(result.String()), nil
}

// handleSearchMessages handles the search_messages tool request.
func (m *MCPServer) handleSearchMessages(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	senderJID := request.GetString("from", "")

	messages, useGlob, err := m.svc.SearchMessages(query, senderJID, int(request.GetFloat("limit", 50.0)))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d messages matching '%s'", len(messages), query)
	if senderJID != "" {
		fmt.Fprintf(&result, " from sender %s", senderJID)
	}
	if useGlob {
		result.WriteString(" (using pattern matching)")
	}
	result.WriteString(":\n\n")

	for i, msg := range messages {
		sender := service.SenderDisplayName(msg)
		if msg.IsFromMe {
			sender = "You"
		}

		fmt.Fprintf(&result, "%d. [%s] %s in chat %s:\n",
			i+1, m.svc.FormatDateTime(msg.Timestamp), sender, msg.ChatJID)
		fmt.Fprintf(&result, "   %s\n", msg.Text)

		if msg.MediaMetadata != nil {
			writeMediaLine(&result, msg.MediaMetadata, true, msg.ID)
		}
		result.WriteString("\n")
	}

	return mcp.NewToolResultText(result.String()), nil
}

// handleFindChat handles the find_chat tool request.
func (m *MCPServer) handleFindChat(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chats, useGlob, err := m.svc.FindChat(request.GetString("search", ""))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to search chats: %v", err)), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d matching chats", len(chats))
	if useGlob {
		result.WriteString(" (using pattern matching)")
	}
	result.WriteString(":\n\n")

	for i, chat := range chats {
		writeChatLine(&result, i, chat)
		result.WriteString("\n")
	}

	return mcp.NewToolResultText(result.String()), nil
}

// handleSendMessage handles the send_message tool request.
func (m *MCPServer) handleSendMessage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chatJID := request.GetString("chat_jid", "")

	if err := m.svc.SendMessage(ctx, chatJID, request.GetString("text", "")); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to send message: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Message sent successfully to %s", chatJID)), nil
}

// handleLoadMoreMessages handles the load_more_messages tool request.
func (m *MCPServer) handleLoadMoreMessages(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chatJID := request.GetString("chat_jid", "")
	count := int(request.GetFloat("count", 50.0))
	waitForSync := request.GetBool("wait_for_sync", true)

	messages, err := m.svc.LoadMoreMessages(ctx, chatJID, count, waitForSync)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to load messages: %v", err)), nil
	}

	var result strings.Builder
	if !waitForSync {
		fmt.Fprintf(&result, "History sync request sent for chat %s (%d messages). Messages will load in the background. Use get_chat_messages to see them once loaded.", chatJID, count)
		return mcp.NewToolResultText(result.String()), nil
	}

	fmt.Fprintf(&result, "Loaded %d additional messages from chat %s:\n\n", len(messages), chatJID)
	m.writeConversation(&result, messages, false)

	return mcp.NewToolResultText(result.String()), nil
}

// handleGetMyInfo handles the get_my_info tool request.
func (m *MCPServer) handleGetMyInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	myInfo, err := m.svc.GetMyInfo(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get user info: %v", err)), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Your WhatsApp Profile:\n\n")
	fmt.Fprintf(&result, "JID: %s\n", myInfo.JID)

	if myInfo.PushName != "" {
		fmt.Fprintf(&result, "Display Name: %s\n", myInfo.PushName)
	}
	if myInfo.Status != "" {
		fmt.Fprintf(&result, "Status/Bio: %s\n", myInfo.Status)
	} else {
		fmt.Fprintf(&result, "Status/Bio: (not set)\n")
	}
	if myInfo.BusinessName != "" {
		fmt.Fprintf(&result, "Business Name: %s\n", myInfo.BusinessName)
	}

	if myInfo.PictureURL != "" {
		fmt.Fprintf(&result, "\nProfile Picture:\n")
		fmt.Fprintf(&result, "  Picture ID: %s\n", myInfo.PictureID)
		fmt.Fprintf(&result, "  URL: %s\n", myInfo.PictureURL)
	} else {
		fmt.Fprintf(&result, "\nProfile Picture: (not set)\n")
	}

	return mcp.NewToolResultText(result.String()), nil
}

// handleListMedia handles the list_media tool request.
func (m *MCPServer) handleListMedia(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chatJID := request.GetString("chat_jid", "")
	mediaType := request.GetString("media_type", "")

	media, err := m.svc.ListMedia(chatJID, mediaType, int(request.GetFloat("limit", 50.0)))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list media: %v", err)), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d media files", len(media))
	if chatJID != "" {
		fmt.Fprintf(&result, " in chat %s", chatJID)
	}
	if mediaType != "" {
		fmt.Fprintf(&result, " (type: %s)", mediaType)
	}
	result.WriteString(":\n\n")

	for i, meta := range media {
		fmt.Fprintf(&result, "%d. %s\n", i+1, meta.FileName)
		fmt.Fprintf(&result, "   Message ID: %s\n", meta.MessageID)
		fmt.Fprintf(&result, "   Type: %s | Size: %s\n", meta.MimeType, service.FormatFileSize(meta.FileSize))

		if dims := service.FormatDimensions(meta.Width, meta.Height); dims != "" {
			fmt.Fprintf(&result, "   Dimensions: %s\n", dims)
		}
		if dur := service.FormatDuration(meta.Duration); dur != "" {
			fmt.Fprintf(&result, "   Duration: %s\n", dur)
		}

		switch meta.DownloadStatus {
		case "downloaded":
			result.WriteString("   Status: Downloaded\n")
		case "pending":
			result.WriteString("   Status: Not downloaded\n")
		case "failed":
			fmt.Fprintf(&result, "   Status: Download failed (%s)\n", meta.DownloadError)
		case "expired":
			result.WriteString("   Status: Expired\n")
		case "skipped":
			result.WriteString("   Status: Skipped (auto-download disabled for this type)\n")
		}
		result.WriteString("\n")
	}

	return mcp.NewToolResultText(result.String()), nil
}

// handleGetMedia handles the get_media tool request.
func (m *MCPServer) handleGetMedia(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	messageID := request.GetString("message_id", "")

	media, err := m.svc.GetMedia(ctx, messageID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	meta := media.Meta

	encodedData := base64.StdEncoding.EncodeToString(media.Data)

	desc := fmt.Sprintf("%s (%s, %s)", meta.FileName, meta.MimeType, service.FormatFileSize(meta.FileSize))
	if dims := service.FormatDimensions(meta.Width, meta.Height); dims != "" {
		desc += fmt.Sprintf(", %s", dims)
	}
	if dur := service.FormatDuration(meta.Duration); dur != "" {
		desc += fmt.Sprintf(", duration: %s", dur)
	}

	// images render inline for AI assistants; everything else is an embedded resource
	if strings.HasPrefix(meta.MimeType, "image/") {
		return mcp.NewToolResultImage(desc, encodedData, meta.MimeType), nil
	}

	return mcp.NewToolResultResource(desc, mcp.BlobResourceContents{
		URI:      fmt.Sprintf("whatsapp://media/%s", messageID),
		MIMEType: meta.MimeType,
		Blob:     encodedData,
	}), nil
}

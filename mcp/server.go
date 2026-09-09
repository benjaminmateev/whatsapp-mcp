package mcp

import (
	"log"
	"time"

	"whatsapp-mcp/service"
	"whatsapp-mcp/storage"
	"whatsapp-mcp/whatsapp"

	"github.com/mark3labs/mcp-go/server"
)

// MCPServer represents an MCP server instance for WhatsApp integration.
type MCPServer struct {
	server     *server.MCPServer
	svc        *service.Service
	wa         *whatsapp.Client
	store      *storage.MessageStore
	mediaStore *storage.MediaStore
	log        *log.Logger
	timezone   *time.Location
}

// NewMCPServer creates a new MCP server over the shared service layer.
func NewMCPServer(svc *service.Service, wa *whatsapp.Client, store *storage.MessageStore, mediaStore *storage.MediaStore, timezone *time.Location) *MCPServer {
	s := server.NewMCPServer(
		"WhatsApp MCP",
		"1.0.0",
		server.WithInstructions(`WhatsApp integration for messaging and media operations.

Key workflow: find_chat → get_chat_messages or send_message
Media workflow: list_media → get_media (to view images/files)
Always get chat_jid from find_chat before other operations.
JIDs are WhatsApp identifiers (e.g., 5511999999999@s.whatsapp.net).

Use prompts for common workflows or resources for detailed guides.`),
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithRecovery(),
	)

	m := &MCPServer{
		server:     s,
		svc:        svc,
		wa:         wa,
		store:      store,
		mediaStore: mediaStore,
		log:        log.Default(),
		timezone:   timezone,
	}

	// register all capabilities
	m.registerTools()
	m.registerPrompts()
	m.registerResources()

	return m
}

// GetServer returns the underlying MCP server instance.
func (m *MCPServer) GetServer() *server.MCPServer {
	return m.server
}

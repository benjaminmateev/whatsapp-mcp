package whatsapp

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/types"
)

// SendReaction reacts to an existing message with an emoji. An empty emoji
// removes a reaction sent earlier.
//
// senderJID is the AUTHOR of the message being reacted to, not the chat: in a
// group the reaction key has to name the participant who sent it, or WhatsApp
// attaches the reaction to nothing. For a message you sent yourself, pass your
// own JID.
func (c *Client) SendReaction(ctx context.Context, chatJID, senderJID, messageID, emoji string) error {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat_jid: %w", err)
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return fmt.Errorf("invalid sender_jid: %w", err)
	}
	if messageID == "" {
		return fmt.Errorf("message_id is required")
	}

	msg := c.wa.BuildReaction(chat, sender, types.MessageID(messageID), emoji)
	_, err = c.wa.SendMessage(ctx, chat, msg)
	return err
}

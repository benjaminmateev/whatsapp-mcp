package whatsapp

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-mcp/storage"
)

// contactDisplayName returns the address-book name for a contact:
// full name, else first name, else business name.
func contactDisplayName(info types.ContactInfo) string {
	switch {
	case info.FullName != "":
		return info.FullName
	case info.FirstName != "":
		return info.FirstName
	default:
		return info.BusinessName
	}
}

// mergeContactNames keys the contact store by canonical chat JID.
// A person can be stored under both their phone-number JID and their LID; both
// normalize to the same chat. A name stored under the canonical JID itself wins,
// and the other entry only fills in what is missing.
func mergeContactNames(contacts map[types.JID]types.ContactInfo, normalize func(types.JID) string) map[string]storage.ContactNames {
	merged := make(map[string]storage.ContactNames, len(contacts))
	direct := make(map[string]bool, len(contacts))
	for jid, info := range contacts {
		key := normalize(jid)
		if key == "" {
			continue
		}
		names := storage.ContactNames{ContactName: contactDisplayName(info), PushName: info.PushName}
		isDirect := jid.ToNonAD().String() == key

		prev, seen := merged[key]
		switch {
		case !seen:
			merged[key] = names
		case isDirect && !direct[key]:
			merged[key] = fillContactNames(names, prev)
		default:
			merged[key] = fillContactNames(prev, names)
		}
		if isDirect {
			direct[key] = true
		}
	}
	return merged
}

// fillContactNames returns primary with its empty fields taken from fallback.
func fillContactNames(primary, fallback storage.ContactNames) storage.ContactNames {
	if primary.ContactName == "" {
		primary.ContactName = fallback.ContactName
	}
	if primary.PushName == "" {
		primary.PushName = fallback.PushName
	}
	return primary
}

// ownerIdentity returns the account owner's canonical JIDs and known display names.
func (c *Client) ownerIdentity(ctx context.Context) (jids []string, names []string) {
	if c.wa.Store.ID == nil {
		return nil, nil
	}
	if c.wa.Store.PushName != "" {
		names = append(names, c.wa.Store.PushName)
	}
	own := []types.JID{c.wa.Store.ID.ToNonAD()}
	if !c.wa.Store.LID.IsEmpty() {
		own = append(own, c.wa.Store.LID.ToNonAD())
	}
	for _, jid := range own {
		jids = append(jids, c.normalizeJID(jid), jid.String())
		if c.wa.Store.Contacts == nil {
			continue
		}
		info, err := c.wa.Store.Contacts.GetContact(ctx, jid)
		if err != nil || !info.Found {
			continue
		}
		for _, name := range []string{info.PushName, info.FullName, info.FirstName} {
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return jids, names
}

// SyncChatNames copies names from the WhatsApp contact store onto one-to-one chats
// and repairs chats that carry the account owner's own display name. It runs at
// startup and after a full contact-list sync, and is safe to run any number of times.
func (c *Client) SyncChatNames(ctx context.Context) error {
	if c.wa.Store.Contacts == nil || c.wa.Store.ID == nil {
		return nil
	}
	contacts, err := c.wa.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return fmt.Errorf("failed to read contact store: %w", err)
	}
	ownerJIDs, ownerNames := c.ownerIdentity(ctx)

	result, err := c.store.SyncChatNames(storage.ChatNameSync{
		Contacts:   mergeContactNames(contacts, c.normalizeJID),
		OwnerNames: ownerNames,
		OwnerJIDs:  ownerJIDs,
	})
	if err != nil {
		return fmt.Errorf("failed to sync chat names: %w", err)
	}
	c.log.Infof("Chat names synced from %d contacts: %d contact names set, %d owner push names repaired, %d push names filled (owner names: %v)",
		len(contacts), result.ContactNamesSet, result.PushNamesRepaired, result.PushNamesFilled, result.OwnerNames)
	return nil
}

// handleContact processes contact info updates from app state sync.
// whatsmeow stores the names itself; the chat row is updated here so that
// find_chat and list_chats see the saved name without waiting for a new message.
func (c *Client) handleContact(evt *events.Contact) {
	c.log.Debugf("Contact info updated: %s (FullName: %s, FirstName: %s)",
		evt.JID, evt.Action.GetFullName(), evt.Action.GetFirstName())

	name := evt.Action.GetFullName()
	if name == "" {
		name = evt.Action.GetFirstName()
	}
	if _, err := c.store.UpdateChatContactName(c.normalizeJID(evt.JID), name); err != nil {
		c.log.Warnf("Failed to update contact name of chat %s: %v", evt.JID, err)
	}
}

// handlePushName processes push name updates from WhatsApp.
// whatsmeow stores the name itself; the matching one-to-one chat is updated here.
func (c *Client) handlePushName(evt *events.PushName) {
	c.log.Debugf("Push name updated: %s -> %s", evt.JID, evt.NewPushName)

	if _, err := c.store.UpdateChatPushName(c.normalizeJID(evt.JID), evt.NewPushName); err != nil {
		c.log.Warnf("Failed to update push name of chat %s: %v", evt.JID, err)
	}
}

// handleAppStateSyncComplete re-syncs chat names after a full contact-list sync.
// whatsmeow stores a full snapshot of the contact list without one Contact event
// per contact, so the chats would otherwise keep the names they had before.
func (c *Client) handleAppStateSyncComplete(evt *events.AppStateSyncComplete) {
	if evt.Name != appstate.WAPatchCriticalUnblockLow {
		return
	}
	if err := c.SyncChatNames(context.Background()); err != nil {
		c.log.Warnf("Chat name sync after contact sync failed: %v", err)
	}
}

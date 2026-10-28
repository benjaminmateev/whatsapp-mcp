package storage

import (
	"fmt"
	"sort"
	"strings"
)

// ownerNameMinChats is how many one-to-one chats must share a push name, with no
// contact confirming it, before that name is treated as one of the account owner's
// own display names. A DM chat's push name should belong to the other person, so a
// single name on many unrelated DMs is the owner's name stored by mistake (for
// example an older display name the owner no longer uses).
const ownerNameMinChats = 10

// ContactNames holds the names the WhatsApp contact store knows for one JID.
type ContactNames struct {
	ContactName string // address-book name: full name, else first name, else business name
	PushName    string // the contact's own WhatsApp display name
}

// ChatNameSync is the input for SyncChatNames.
type ChatNameSync struct {
	Contacts   map[string]ContactNames // contact store names, keyed by canonical chat JID
	OwnerNames []string                // the account owner's own display names
	OwnerJIDs  []string                // the owner's canonical JIDs; the self-chat is left alone
}

// ChatNameSyncResult reports what SyncChatNames changed.
type ChatNameSyncResult struct {
	ContactNamesSet   int      // chats whose contact_name was filled or updated
	PushNamesRepaired int      // chats whose push_name was the owner's name and was replaced
	PushNamesFilled   int      // chats with no push_name that got one from the contact store
	OwnerNames        []string // owner names that were looked for (known and detected)
}

// isDMJID reports whether a canonical JID is a one-to-one chat with a person.
func isDMJID(jid string) bool {
	return strings.HasSuffix(jid, "@s.whatsapp.net") || strings.HasSuffix(jid, "@lid")
}

// UpdateChatContactName sets the saved contact name of an existing one-to-one chat.
// An empty name is ignored. It reports whether a row changed.
func (s *MessageStore) UpdateChatContactName(jid, contactName string) (bool, error) {
	if jid == "" || contactName == "" || !isDMJID(jid) {
		return false, nil
	}
	res, err := s.db.Exec(
		`UPDATE chats SET contact_name = ? WHERE jid = ? AND is_group = 0 AND COALESCE(contact_name, '') <> ?`,
		contactName, jid, contactName,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpdateChatPushName sets the push name of an existing one-to-one chat.
// An empty name is ignored. It reports whether a row changed.
func (s *MessageStore) UpdateChatPushName(jid, pushName string) (bool, error) {
	if jid == "" || pushName == "" || !isDMJID(jid) {
		return false, nil
	}
	res, err := s.db.Exec(
		`UPDATE chats SET push_name = ? WHERE jid = ? AND is_group = 0 AND COALESCE(push_name, '') <> ?`,
		pushName, jid, pushName,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SyncChatNames brings the names of one-to-one chats in line with the contact store.
//
// It does three things, in one transaction:
//   - contact_name is set from the contact store wherever the store has a name.
//     Chats saved during the first history sync often miss it, because WhatsApp
//     sends the contact list after the messages.
//   - push_name is repaired where it holds one of the account owner's own display
//     names. It is replaced by the contact's push name, or cleared if none is known.
//   - an empty push_name is filled from the contact store.
//
// It is idempotent: a second run with the same input changes nothing.
func (s *MessageStore) SyncChatNames(in ChatNameSync) (ChatNameSyncResult, error) {
	var result ChatNameSyncResult

	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	type dmChat struct {
		jid, pushName, contactName string
	}

	ownerJIDs := make(map[string]bool, len(in.OwnerJIDs))
	for _, jid := range in.OwnerJIDs {
		ownerJIDs[jid] = true
	}

	rows, err := tx.Query(`SELECT jid, COALESCE(push_name, ''), COALESCE(contact_name, '') FROM chats WHERE is_group = 0`)
	if err != nil {
		return result, err
	}
	var chats []dmChat
	for rows.Next() {
		var c dmChat
		if err := rows.Scan(&c.jid, &c.pushName, &c.contactName); err != nil {
			rows.Close()
			return result, err
		}
		if isDMJID(c.jid) && !ownerJIDs[c.jid] {
			chats = append(chats, c)
		}
	}
	if err := rows.Close(); err != nil {
		return result, err
	}

	storedPushNames := make(map[string]string)
	pnRows, err := tx.Query(`SELECT jid, push_name FROM push_names`)
	if err != nil {
		return result, err
	}
	for pnRows.Next() {
		var jid, name string
		if err := pnRows.Scan(&jid, &name); err != nil {
			pnRows.Close()
			return result, err
		}
		storedPushNames[jid] = name
	}
	if err := pnRows.Close(); err != nil {
		return result, err
	}

	// knownPushName is the contact's own display name from a source that only
	// records names the other side sent: the contact store, then push_names.
	knownPushName := func(jid string) string {
		if name := in.Contacts[jid].PushName; name != "" {
			return name
		}
		return storedPushNames[jid]
	}

	owner := make(map[string]bool)
	for _, name := range in.OwnerNames {
		if name = strings.TrimSpace(name); name != "" {
			owner[name] = true
		}
	}

	// detect owner names that are no longer the current one: a push name on many
	// DM chats that no contact source confirms for any of them
	shared := make(map[string]int)
	confirmed := make(map[string]bool)
	for _, c := range chats {
		if c.pushName == "" {
			continue
		}
		shared[c.pushName]++
		if knownPushName(c.jid) == c.pushName {
			confirmed[c.pushName] = true
		}
	}
	for name, n := range shared {
		if n >= ownerNameMinChats && !confirmed[name] {
			owner[name] = true
		}
	}

	stmt, err := tx.Prepare(`UPDATE chats SET push_name = ?, contact_name = ? WHERE jid = ?`)
	if err != nil {
		return result, err
	}
	defer stmt.Close()

	for _, c := range chats {
		newContact := c.contactName
		if name := in.Contacts[c.jid].ContactName; name != "" && name != c.contactName {
			newContact = name
			result.ContactNamesSet++
		}

		newPush := c.pushName
		known := knownPushName(c.jid)
		if owner[known] {
			known = ""
		}
		switch {
		case owner[c.pushName] && knownPushName(c.jid) != c.pushName:
			newPush = known
			result.PushNamesRepaired++
		case c.pushName == "" && known != "":
			newPush = known
			result.PushNamesFilled++
		}

		if newContact == c.contactName && newPush == c.pushName {
			continue
		}
		if _, err := stmt.Exec(newPush, newContact, c.jid); err != nil {
			return result, fmt.Errorf("failed to update chat %s: %w", c.jid, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}

	for name := range owner {
		result.OwnerNames = append(result.OwnerNames, name)
	}
	sort.Strings(result.OwnerNames)
	return result, nil
}

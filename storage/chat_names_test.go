package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStore opens a migrated database in a temp directory.
func newTestStore(t *testing.T) (*MessageStore, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := NewMigrator(db).Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewMessageStore(db), db
}

func saveChat(t *testing.T, s *MessageStore, jid, pushName, contactName string, isGroup bool) {
	t.Helper()
	err := s.SaveChat(Chat{JID: jid, PushName: pushName, ContactName: contactName, LastMessageTime: time.Unix(1700000000, 0), IsGroup: isGroup})
	if err != nil {
		t.Fatalf("save chat %s: %v", jid, err)
	}
}

func chatNames(t *testing.T, s *MessageStore, jid string) (pushName, contactName string) {
	t.Helper()
	chat, err := s.GetChatByJID(jid)
	if err != nil || chat == nil {
		t.Fatalf("get chat %s: %v (nil=%v)", jid, err, chat == nil)
	}
	return chat.PushName, chat.ContactName
}

func TestSyncChatNamesMakesContactFindable(t *testing.T) {
	s, _ := newTestStore(t)
	const sam = "447979544515@s.whatsapp.net"
	saveChat(t, s, sam, "", "", false)

	found, _ := s.SearchChatsFiltered("Sam Cash", false, 10)
	if len(found) != 0 {
		t.Fatalf("expected no match before sync, got %d", len(found))
	}

	res, err := s.SyncChatNames(ChatNameSync{
		Contacts: map[string]ContactNames{sam: {ContactName: "Sam Cash", PushName: "Sam"}},
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.ContactNamesSet != 1 || res.PushNamesFilled != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}

	found, _ = s.SearchChatsFiltered("Sam Cash", false, 10)
	if len(found) != 1 || found[0].JID != sam {
		t.Fatalf("find_chat should match the contact name, got %+v", found)
	}
	if push, contact := chatNames(t, s, sam); push != "Sam" || contact != "Sam Cash" {
		t.Fatalf("got push=%q contact=%q", push, contact)
	}
}

func TestSyncChatNamesRepairsOwnerPushName(t *testing.T) {
	s, _ := newTestStore(t)
	const (
		alice = "491111111111@s.whatsapp.net"
		bob   = "492222222222@s.whatsapp.net"
		self  = "493333333333@s.whatsapp.net"
		group = "120363000000000000@g.us"
	)
	saveChat(t, s, alice, "Owner", "", false)
	saveChat(t, s, bob, "Owner", "Bob Saved", false)
	saveChat(t, s, self, "Owner", "", false)
	saveChat(t, s, group, "Owner", "", true)

	res, err := s.SyncChatNames(ChatNameSync{
		Contacts:   map[string]ContactNames{alice: {PushName: "Alice"}},
		OwnerNames: []string{"Owner"},
		OwnerJIDs:  []string{self},
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.PushNamesRepaired != 2 {
		t.Fatalf("expected 2 repaired, got %+v", res)
	}

	if push, _ := chatNames(t, s, alice); push != "Alice" {
		t.Fatalf("alice push=%q, want contact's own push name", push)
	}
	if push, contact := chatNames(t, s, bob); push != "" || contact != "Bob Saved" {
		t.Fatalf("bob push=%q contact=%q, want owner name cleared and contact kept", push, contact)
	}
	if push, _ := chatNames(t, s, self); push != "Owner" {
		t.Fatalf("self-chat must keep the owner's name, got %q", push)
	}
	if push, _ := chatNames(t, s, group); push != "Owner" {
		t.Fatalf("group names must not be touched, got %q", push)
	}

	found, _ := s.SearchChatsFiltered("Owner", false, 10)
	if len(found) != 2 {
		t.Fatalf("searching the owner's name should only find the self-chat and the group, got %d", len(found))
	}
}

func TestSyncChatNamesDetectsOldOwnerName(t *testing.T) {
	s, db := newTestStore(t)

	// an older owner display name stored on many DMs, confirmed by no contact
	for i := 0; i < ownerNameMinChats; i++ {
		saveChat(t, s, fmt.Sprintf("4910000000%02d@s.whatsapp.net", i), "Old Nick", "", false)
	}
	// a common first name on as many DMs, confirmed by one contact's push name
	for i := 0; i < ownerNameMinChats; i++ {
		saveChat(t, s, fmt.Sprintf("4920000000%02d@s.whatsapp.net", i), "Max", "", false)
	}
	if _, err := db.Exec(`INSERT INTO push_names (jid, push_name) VALUES ('492000000000@s.whatsapp.net', 'Max')`); err != nil {
		t.Fatalf("seed push_names: %v", err)
	}

	res, err := s.SyncChatNames(ChatNameSync{OwnerNames: []string{"Current Name"}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.PushNamesRepaired != ownerNameMinChats {
		t.Fatalf("expected %d repaired, got %+v", ownerNameMinChats, res)
	}
	if push, _ := chatNames(t, s, "491000000003@s.whatsapp.net"); push != "" {
		t.Fatalf("old owner name should be cleared, got %q", push)
	}
	if push, _ := chatNames(t, s, "492000000003@s.whatsapp.net"); push != "Max" {
		t.Fatalf("a confirmed contact name must be kept, got %q", push)
	}
}

func TestSyncChatNamesIsIdempotent(t *testing.T) {
	s, _ := newTestStore(t)
	const alice = "491111111111@s.whatsapp.net"
	saveChat(t, s, alice, "Owner", "", false)
	in := ChatNameSync{
		Contacts:   map[string]ContactNames{alice: {ContactName: "Alice A.", PushName: "Alice"}},
		OwnerNames: []string{"Owner"},
	}
	if _, err := s.SyncChatNames(in); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	res, err := s.SyncChatNames(in)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if res.ContactNamesSet != 0 || res.PushNamesRepaired != 0 || res.PushNamesFilled != 0 {
		t.Fatalf("second run changed rows: %+v", res)
	}
}

func TestUpdateChatContactAndPushName(t *testing.T) {
	s, _ := newTestStore(t)
	const (
		dm    = "491111111111@s.whatsapp.net"
		group = "120363000000000000@g.us"
	)
	saveChat(t, s, dm, "", "", false)
	saveChat(t, s, group, "Group", "", true)

	if changed, err := s.UpdateChatContactName(dm, "Alice A."); err != nil || !changed {
		t.Fatalf("contact name update: changed=%v err=%v", changed, err)
	}
	if changed, _ := s.UpdateChatContactName(dm, "Alice A."); changed {
		t.Fatal("same name should not count as a change")
	}
	if changed, _ := s.UpdateChatContactName(dm, ""); changed {
		t.Fatal("empty name must not wipe the contact name")
	}
	if changed, _ := s.UpdateChatContactName(group, "x"); changed {
		t.Fatal("groups must not get a contact name")
	}
	if changed, err := s.UpdateChatPushName(dm, "Alice"); err != nil || !changed {
		t.Fatalf("push name update: changed=%v err=%v", changed, err)
	}
	if changed, _ := s.UpdateChatPushName(group, "Renamed"); changed {
		t.Fatal("group names come from GroupInfo, not push names")
	}
	if push, contact := chatNames(t, s, dm); push != "Alice" || contact != "Alice A." {
		t.Fatalf("got push=%q contact=%q", push, contact)
	}
}

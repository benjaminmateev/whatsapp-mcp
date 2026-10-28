package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestContactDisplayName(t *testing.T) {
	cases := []struct {
		info types.ContactInfo
		want string
	}{
		{types.ContactInfo{FullName: "Sam Cash", FirstName: "Sam", BusinessName: "Cash Ltd", PushName: "S"}, "Sam Cash"},
		{types.ContactInfo{FirstName: "Sam", BusinessName: "Cash Ltd", PushName: "S"}, "Sam"},
		{types.ContactInfo{BusinessName: "Cash Ltd", PushName: "S"}, "Cash Ltd"},
		{types.ContactInfo{PushName: "S"}, ""}, // a push name is not an address-book name
	}
	for _, tc := range cases {
		if got := contactDisplayName(tc.info); got != tc.want {
			t.Fatalf("contactDisplayName(%+v) = %q, want %q", tc.info, got, tc.want)
		}
	}
}

func TestMergeContactNamesJoinsPhoneAndLID(t *testing.T) {
	pn := types.NewJID("447979544515", types.DefaultUserServer)
	lid := types.NewJID("97796438904880", types.HiddenUserServer)
	other := types.NewJID("491111111111", types.DefaultUserServer)
	normalize := func(j types.JID) string {
		if j == lid {
			return pn.String()
		}
		return j.ToNonAD().String()
	}

	contacts := map[types.JID]types.ContactInfo{
		pn:    {Found: true, FullName: "Sam Cash"},
		lid:   {Found: true, FullName: "Old Name", PushName: "Sammy"},
		other: {Found: true, FirstName: "Alice"},
	}

	// map iteration order is random; the result must not depend on it
	for i := 0; i < 50; i++ {
		got := mergeContactNames(contacts, normalize)
		if len(got) != 2 {
			t.Fatalf("expected 2 chats, got %d: %+v", len(got), got)
		}
		sam := got[pn.String()]
		if sam.ContactName != "Sam Cash" || sam.PushName != "Sammy" {
			t.Fatalf("phone-number entry must win and the LID fill gaps, got %+v", sam)
		}
		if got[other.String()].ContactName != "Alice" {
			t.Fatalf("first name fallback missing: %+v", got[other.String()])
		}
	}
}

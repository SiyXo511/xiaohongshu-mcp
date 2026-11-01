package xiaohongshu

import (
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestManagedNotesPreserveStableNoteIDs(t *testing.T) {
	raw := []managedNoteRaw{
		{
			Title:       "把想念寄出去",
			PublishedAt: "2026-11-01 23:23",
			Impression:  `{"noteTarget":{"value":{"noteId":"6ae75966000000001802ed19"}}}`,
		},
		{
			Title:       "把想念寄出去",
			PublishedAt: "2026-11-01 22:26",
			Impression:  `{"noteTarget":{"value":{"noteId":"6ae74c03000000000e00e176"}}}`,
		},
	}

	notes := managedNotesFromRaw(raw)
	if len(notes) != 2 {
		t.Fatalf("notes = %d", len(notes))
	}
	if notes[0].NoteID != "6ae75966000000001802ed19" ||
		notes[1].NoteID != "6ae74c03000000000e00e176" {
		t.Fatalf("unexpected IDs: %+v", notes)
	}
}

func TestManagedNotesDoNotGuessMissingIDs(t *testing.T) {
	notes := managedNotesFromRaw([]managedNoteRaw{
		{Title: "标题相同", Impression: ""},
		{Title: "标题相同", Impression: "not-json"},
		{Title: "标题相同", Impression: `{"noteTarget":{"value":{}}}`},
	})
	for _, note := range notes {
		if note.NoteID != "" {
			t.Fatalf("missing impression produced guessed ID: %+v", note)
		}
	}
}

func TestUniqueManagedNoteRejectsMissingAndDuplicateTargets(t *testing.T) {
	if _, err := uniqueManagedNote(nil, "wanted"); err == nil {
		t.Fatal("missing target accepted")
	}
	notes := []ManagedNote{
		{NoteID: "wanted", Title: "first"},
		{NoteID: "wanted", Title: "duplicate"},
	}
	if _, err := uniqueManagedNote(notes, "wanted"); err == nil {
		t.Fatal("duplicate target accepted")
	}
}

func TestSessionFingerprintIgnoresCookieOrderAndDetectsChanges(t *testing.T) {
	first := []*proto.NetworkCookie{
		{Name: "galaxy_creator_session_id", Value: "session-a", Domain: ".xiaohongshu.com", Path: "/"},
		{Name: "a1", Value: "web_session", Domain: ".xiaohongshu.com", Path: "/"},
	}
	reversed := []*proto.NetworkCookie{first[1], first[0]}
	if sessionFingerprintFromCookies(first) != sessionFingerprintFromCookies(reversed) {
		t.Fatal("cookie order changed fingerprint")
	}

	changed := []*proto.NetworkCookie{
		{Name: "galaxy_creator_session_id", Value: "session-b", Domain: ".xiaohongshu.com", Path: "/"},
		first[1],
	}
	if sessionFingerprintFromCookies(first) == sessionFingerprintFromCookies(changed) {
		t.Fatal("session change did not alter fingerprint")
	}
}

func TestXHSNoteMissingRequiresLoadedEvidence(t *testing.T) {
	if xhsNoteMissingConfirmed(false, 0, "") {
		t.Fatal("empty page reload was accepted as a confirmed deletion")
	}
	if xhsNoteMissingConfirmed(true, 3, "删除成功") {
		t.Fatal("visible target was accepted as deleted")
	}
	if !xhsNoteMissingConfirmed(false, 3, "") {
		t.Fatal("missing target in a loaded list was rejected")
	}
	if !xhsNoteMissingConfirmed(false, 0, "删除成功") {
		t.Fatal("explicit deletion success was rejected")
	}
}

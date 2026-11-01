package xiaohongshu

import "testing"

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

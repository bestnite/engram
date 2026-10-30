package main

import (
	"bytes"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestPrintMigrationReport covers the read-only pre-flight output: every issue is printed with its deck, note and
// field, and the return value is the number of blocking notes, because that is what decides the exit status.
func TestPrintMigrationReport(t *testing.T) {
	cases := []struct {
		name       string
		report     store.MigrationReport
		wantBlocks int
		wantLines  []string
	}{
		{
			name:       "clean",
			report:     store.MigrationReport{},
			wantBlocks: 0,
			wantLines:  []string{"no notes need attention"},
		},
		{
			name: "warnings only",
			report: store.MigrationReport{Warnings: []store.MigrationIssue{
				{DeckID: "deck-1", NoteID: "note-1", Field: "text", Reason: `cloze text contains "|"`},
			}},
			wantBlocks: 0,
			wantLines:  []string{"WARNING deck=deck-1 note=note-1 field=text"},
		},
		{
			name: "blocking",
			report: store.MigrationReport{
				Blocking: []store.MigrationIssue{
					{DeckID: "deck-1", NoteID: "note-2", Field: "items", Reason: "array item contains a newline"},
				},
				Warnings: []store.MigrationIssue{
					{DeckID: "deck-1", NoteID: "note-3", Field: "text", Reason: `cloze text contains "\\alt"`},
				},
			},
			wantBlocks: 1,
			wantLines: []string{
				"BLOCKING deck=deck-1 note=note-2 field=items: array item contains a newline",
				"WARNING deck=deck-1 note=note-3",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := printMigrationReport(&buf, tc.report); got != tc.wantBlocks {
				t.Errorf("printMigrationReport() = %d, want %d", got, tc.wantBlocks)
			}
			out := buf.String()
			for _, want := range tc.wantLines {
				if !strings.Contains(out, want) {
					t.Errorf("output %q does not contain %q", out, want)
				}
			}
		})
	}
}

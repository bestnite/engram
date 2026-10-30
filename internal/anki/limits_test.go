package anki_test

import (
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/store"
)

// The .apkg reader and the deck package reader guard against the same zip
// bombs. Keeping the two default caps equal means one limit policy, so this
// test fails when either side changes alone.
func TestDefaultLimitsMatchDeckPackageReader(t *testing.T) {
	got := anki.DefaultLimits()
	want := store.DefaultPackageLimits()
	if got.MaxEntries != want.MaxEntries {
		t.Errorf("MaxEntries = %d, want %d", got.MaxEntries, want.MaxEntries)
	}
	if got.MaxFileBytes != want.MaxFileBytes {
		t.Errorf("MaxFileBytes = %d, want %d", got.MaxFileBytes, want.MaxFileBytes)
	}
	if got.MaxTotalBytes != want.MaxTotalBytes {
		t.Errorf("MaxTotalBytes = %d, want %d", got.MaxTotalBytes, want.MaxTotalBytes)
	}
}

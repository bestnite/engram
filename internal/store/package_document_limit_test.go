package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

// The base64 archive entry point of MCP/CLI rejects oversized input before decoding, against "the expansion bound of the archive cap",
// and never decodes an oversized string wholesale.

// TestPackageReaderRejectsOversizedBase64BeforeDecoding asserts an oversized base64 string is rejected before decoding.
// It uses a string that is both oversized and contains an illegal base64 character ('!'): an implementation that decodes first must yield a decoding error;
// only "check the length first" yields package_too_large.
func TestPackageReaderRejectsOversizedBase64BeforeDecoding(t *testing.T) {
	// With MaxTotalBytes=30 the expansion bound is EncodedLen(30)=40 bytes; 64 > 40, so the length check must stop it.
	limits := PackageLimits{MaxEntries: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 30}
	oversize := strings.Repeat("!", 64)
	_, err := packageReader(oversize, limits)
	if err == nil {
		t.Fatal("oversized invalid base64 was accepted")
	}
	var pe *PackageError
	if !errors.As(err, &pe) || pe.Code != CodePackageTooLarge {
		t.Fatalf("error = %v, want PackageError code %s (length check must run before decode)", err, CodePackageTooLarge)
	}
}

// TestPackageReaderBase64Boundary asserts the expansion bound itself is not rejected by mistake: a string exactly at the bound is accepted,
// anything over the bound is rejected.
func TestPackageReaderBase64Boundary(t *testing.T) {
	limits := PackageLimits{MaxEntries: 8, MaxFileBytes: 1 << 20, MaxTotalBytes: 3}
	// EncodedLen(3)=4: 'YWJj' decodes to 3 bytes, exactly within the bound, so it passes.
	exact := base64.StdEncoding.EncodeToString([]byte("abc"))
	if len(exact) != 4 {
		t.Fatalf("test setup: encoded len = %d, want 4", len(exact))
	}
	r, err := packageReader(exact, limits)
	if err != nil {
		t.Fatalf("base64 at the expansion bound was rejected: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, []byte("abc")) {
		t.Fatalf("decoded = %q err %v, want %q", got, err, "abc")
	}
	// One character more than the bound: even if the content happens to be legal it must be rejected (no spill past the bound).
	if _, err := packageReader(strings.Repeat("A", 5), limits); err == nil {
		t.Fatal("base64 one byte over the expansion bound was accepted")
	}
}

// TestImportViaBase64PackageReader is the positive control: a legal base64 archive still imports
// completely through PackageReader (a real SQLite round trip, reusing the store layer's existing package builders).
func TestImportViaBase64PackageReader(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_b64_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			r, err := PackageReader(base64.StdEncoding.EncodeToString(raw))
			if err != nil {
				t.Fatalf("PackageReader: %v", err)
			}
			importer := seedUsers(t, dst, "pkg_b64_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, r, PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.NotesCreated != 4 {
				t.Fatalf("notes_created = %d, want 4", report.NotesCreated)
			}
		})
	}
}

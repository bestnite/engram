package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// manifest deck name/description bounds
//
// Bounds (decided by the user, 2026-10-06): deck name ≤ 200 characters, description ≤ 2000 characters, counted in Unicode characters
// (runes) rather than bytes; every C0 control character is rejected (U+0000–U+001F, including \n \t \r, no exceptions);
// invalid UTF-8 is rejected. Going over a bound or carrying a forbidden character rejects the whole package with an entry-level error.
// The same rule set for deck names now also constrains creation/rename in the store layer (see validateDeckName in deck.go),
// guaranteeing that any name the export side can produce the import side can take.

// jsonQuote encodes a string as a JSON string literal (control characters escape to \u00XX, which is still valid JSON).
func jsonQuote(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", s, err)
	}
	return string(raw)
}

// manifestZip builds a minimal but complete deck package zip; deck.name/description go into manifest.json verbatim.
func manifestZip(t *testing.T, name, description string) []byte {
	t.Helper()
	manifest := `{"format_version":1,"exported_at":"2026-10-06T00:00:00Z","app_version":"test",` +
		`"deck":{"name":` + jsonQuote(t, name) + `,"description":` + jsonQuote(t, description) + `},` +
		`"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":0,"cards":0}}`
	return buildZip(t, map[string]string{
		"manifest.json": manifest,
		"notes.json":    `[]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":false,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	})
}

// importPackageOK asserts the import succeeds and returns the report.
func importPackageOK(t *testing.T, db *gorm.DB, user uint64, raw []byte, opts PackageImportOptions) *PackageImportReport {
	t.Helper()
	report, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(raw), opts)
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}
	return report
}

// importPackageRejected asserts the import is rejected and returns the PackageError.
func importPackageRejected(t *testing.T, db *gorm.DB, user uint64, raw []byte, opts PackageImportOptions) *PackageError {
	t.Helper()
	_, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(raw), opts)
	var pe *PackageError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want *PackageError", err)
	}
	return pe
}

// deckNameOf reads a deck's name back, asserting an accepted package really created the deck as expected.
func deckNameOf(t *testing.T, db *gorm.DB, deckID uint64) string {
	t.Helper()
	var d Deck
	if err := db.First(&d, "id = ?", deckID).Error; err != nil {
		t.Fatalf("load deck %d: %v", deckID, err)
	}
	return d.Name
}

// TestImportManifestDeckNameLengthLimit asserts the deck name counts characters: 200 passes, 201 is rejected.
func TestImportManifestDeckNameLengthLimit(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_name")[0]

			t.Run("200 characters accepted", func(t *testing.T) {
				name := strings.Repeat("a", 200)
				report := importPackageOK(t, db, user, manifestZip(t, name, ""), PackageImportOptions{})
				if got := deckNameOf(t, db, report.DeckID); got != name {
					t.Fatalf("deck name = %q, want the 200-character name", got)
				}
			})

			t.Run("201 characters rejected", func(t *testing.T) {
				pe := importPackageRejected(t, db, user, manifestZip(t, strings.Repeat("a", 201), ""), PackageImportOptions{})
				if pe.Code != CodePackageDeckMetaInvalid {
					t.Fatalf("code = %s, want %s", pe.Code, CodePackageDeckMetaInvalid)
				}
				if len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "deck.name") {
					t.Fatalf("entries = %v, want one entry naming deck.name", pe.Entries)
				}
			})
		})
	}
}

// TestImportManifestDeckDescriptionLengthLimit asserts the description counts characters: 2000 passes, 2001 is rejected.
func TestImportManifestDeckDescriptionLengthLimit(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_desc")[0]

			t.Run("2000 characters accepted", func(t *testing.T) {
				desc := strings.Repeat("d", 2000)
				report := importPackageOK(t, db, user, manifestZip(t, "desc-ok", desc), PackageImportOptions{})
				var d Deck
				if err := db.First(&d, "id = ?", report.DeckID).Error; err != nil {
					t.Fatalf("load deck: %v", err)
				}
				if d.Description != desc {
					t.Fatalf("description length = %d, want 2000", len(d.Description))
				}
			})

			t.Run("2001 characters rejected", func(t *testing.T) {
				pe := importPackageRejected(t, db, user, manifestZip(t, "desc-too-long", strings.Repeat("d", 2001)), PackageImportOptions{})
				if pe.Code != CodePackageDeckMetaInvalid {
					t.Fatalf("code = %s, want %s", pe.Code, CodePackageDeckMetaInvalid)
				}
				if len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "deck.description") {
					t.Fatalf("entries = %v, want one entry naming deck.description", pe.Entries)
				}
			})
		})
	}
}

// TestImportManifestDeckUnicodeCountedByCharacter asserts the length counts Unicode characters, not bytes:
// 200 CJK characters (600 bytes) and 200 emoji pass, 201 CJK characters are rejected.
func TestImportManifestDeckUnicodeCountedByCharacter(t *testing.T) {
	cases := []struct {
		name     string
		deckName string
		wantErr  bool
	}{
		{"200 hanzi", strings.Repeat("汉", 200), false},
		{"200 emoji", strings.Repeat("😀", 200), false},
		{"201 hanzi", strings.Repeat("汉", 201), true},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_unicode")[0]
			for i, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw := manifestZip(t, tc.deckName, "")
					if tc.wantErr {
						pe := importPackageRejected(t, db, user, raw, PackageImportOptions{})
						if pe.Code != CodePackageDeckMetaInvalid {
							t.Fatalf("code = %s, want %s", pe.Code, CodePackageDeckMetaInvalid)
						}
						return
					}
					opts := PackageImportOptions{}
					if i > 0 {
						// Re-importing the same deck name makes uniqueDeckName add a suffix, so an independent name avoids ambiguity:
						// here only the length verdict matters, the name itself is not compared.
						opts.NewDeckName = tc.deckName
					}
					report := importPackageOK(t, db, user, raw, opts)
					if got := deckNameOf(t, db, report.DeckID); got != tc.deckName {
						t.Fatalf("deck name rune count = %d, want %d", len([]rune(got)), len([]rune(tc.deckName)))
					}
				})
			}
		})
	}
}

// TestImportManifestDeckControlCharacters asserts every C0 control character is rejected (newline and tab included).
func TestImportManifestDeckControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		raw  byte
	}{
		{"null", 0x00},
		{"bell", 0x07},
		{"newline", 0x0a},
		{"tab", 0x09},
		{"carriage return", 0x0d},
		{"escape", 0x1b},
		{"unit separator", 0x1f},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_ctrl")[0]
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					pe := importPackageRejected(t, db, user, manifestZip(t, "bad"+string(tc.raw)+"name", ""), PackageImportOptions{})
					if pe.Code != CodePackageDeckMetaInvalid {
						t.Fatalf("code = %s, want %s", pe.Code, CodePackageDeckMetaInvalid)
					}
					if len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "deck.name") {
						t.Fatalf("entries = %v, want one entry naming deck.name", pe.Entries)
					}
				})
			}
			// The description is subject to the same control-character rule.
			pe := importPackageRejected(t, db, user, manifestZip(t, "ok", "bad\x07desc"), PackageImportOptions{})
			if pe.Code != CodePackageDeckMetaInvalid || len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "deck.description") {
				t.Fatalf("description control char: err = %v, want one entry naming deck.description", pe)
			}
		})
	}
}

// TestImportManifestRejectsInvalidUTF8 asserts invalid UTF-8 bytes in manifest.json are rejected.
//
// Note: the invalid bytes have to be written raw, bypassing json.Marshal -- encoding/json turns invalid UTF-8
// into U+FFFD silently ("not an error"), and looking only at the decoded string never catches it, so one must first validate
// the raw bytes of manifest.json.
func TestImportManifestRejectsInvalidUTF8(t *testing.T) {
	manifest := "{\"format_version\":1,\"exported_at\":\"2026-10-06T00:00:00Z\"," +
		"\"deck\":{\"name\":\"bad\xffname\",\"description\":\"\"}," +
		"\"include_progress\":false,\"include_media\":false,\"include_reviews\":false,\"counts\":{\"notes\":0,\"cards\":0}}"
	raw := buildZip(t, map[string]string{
		"manifest.json": manifest,
		"notes.json":    `[]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":false,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	})
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_utf8")[0]
			pe := importPackageRejected(t, db, user, raw, PackageImportOptions{})
			if pe.Code != CodePackageBadFormat {
				t.Fatalf("code = %s, want %s", pe.Code, CodePackageBadFormat)
			}
			if len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "invalid UTF-8") {
				t.Fatalf("entries = %v, want one entry mentioning invalid UTF-8", pe.Entries)
			}
		})
	}
}

// TestImportManifestNewDeckNameOverrideAlsoLimited asserts the opts.NewDeckName override is bound by the
// deck name bounds as well (it is written into the same column; bypassing validation would leave a back door).
func TestImportManifestNewDeckNameOverrideAlsoLimited(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_override")[0]
			pe := importPackageRejected(t, db, user, manifestZip(t, "short", ""),
				PackageImportOptions{NewDeckName: strings.Repeat("n", 201)})
			if pe.Code != CodePackageDeckMetaInvalid {
				t.Fatalf("code = %s, want %s", pe.Code, CodePackageDeckMetaInvalid)
			}
			if len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "deck.name") {
				t.Fatalf("entries = %v, want one entry naming deck.name", pe.Entries)
			}
		})
	}
}

// TestImportManifestEmptyNameFallsBack asserts an empty deck name still falls back to Imported deck and passes.
func TestImportManifestEmptyNameFallsBack(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "pkg_lim_empty")[0]
			report := importPackageOK(t, db, user, manifestZip(t, "", ""), PackageImportOptions{})
			if got := deckNameOf(t, db, report.DeckID); got != "Imported deck" {
				t.Fatalf("deck name = %q, want %q", got, "Imported deck")
			}
		})
	}
}

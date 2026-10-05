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

// ---- F27：manifest 卡组名/描述界限 ----
//
// 界限（用户拍板，2026-10-06）：卡组名 ≤ 200 字符、描述 ≤ 2000 字符，按 Unicode 字符
// （rune）而非字节计数；拒一切 C0 控制字符（U+0000–U+001F，含 \n \t \r，不开口子）；
// 拒非法 UTF-8。超限或含法外字符一律拒绝整包，并报条目级错误。

// jsonQuote 把字符串编码成 JSON 字符串字面量（控制字符会转义成 \u00XX，仍是合法 JSON）。
func jsonQuote(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", s, err)
	}
	return string(raw)
}

// manifestZip 构造一个最小但完整的卡组包 zip；deck.name/description 原样进 manifest.json。
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

// importPackageOK 断言导入成功并返回报告。
func importPackageOK(t *testing.T, db *gorm.DB, user uint64, raw []byte, opts PackageImportOptions) *PackageImportReport {
	t.Helper()
	report, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(raw), opts)
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}
	return report
}

// importPackageRejected 断言导入被拒并返回 PackageError。
func importPackageRejected(t *testing.T, db *gorm.DB, user uint64, raw []byte, opts PackageImportOptions) *PackageError {
	t.Helper()
	_, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(raw), opts)
	var pe *PackageError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want *PackageError", err)
	}
	return pe
}

// deckNameOf 读回某卡组的名字，断言被接受的包确实按预期建了卡组。
func deckNameOf(t *testing.T, db *gorm.DB, deckID uint64) string {
	t.Helper()
	var d Deck
	if err := db.First(&d, "id = ?", deckID).Error; err != nil {
		t.Fatalf("load deck %d: %v", deckID, err)
	}
	return d.Name
}

// TestImportManifestDeckNameLengthLimit 断言卡组名按字符计：200 通过、201 拒绝。
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

// TestImportManifestDeckDescriptionLengthLimit 断言描述按字符计：2000 通过、2001 拒绝。
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

// TestImportManifestDeckUnicodeCountedByCharacter 断言长度按 Unicode 字符而非字节：
// 200 个汉字（600 字节）与 200 个 emoji 通过，201 个汉字被拒。
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
						// 同一卡组名重复导入时 uniqueDeckName 会加后缀，用独立名字避免歧义：
						// 这里只关心长度判定通过，名字本身不比对。
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

// TestImportManifestDeckControlCharacters 断言一切 C0 控制字符都被拒（含换行与制表符）。
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
			// 描述同样受控字符规则约束。
			pe := importPackageRejected(t, db, user, manifestZip(t, "ok", "bad\x07desc"), PackageImportOptions{})
			if pe.Code != CodePackageDeckMetaInvalid || len(pe.Entries) != 1 || !strings.Contains(pe.Entries[0], "deck.description") {
				t.Fatalf("description control char: err = %v, want one entry naming deck.description", pe)
			}
		})
	}
}

// TestImportManifestRejectsInvalidUTF8 断言 manifest.json 里的非法 UTF-8 字节被拒。
//
// 注意：非法字节必须绕过 json.Marshal 直写原始字节——encoding/json 会把非法 UTF-8
// 静默替换成 U+FFFD（“不是错误”），若只看解码后的字符串就永远抓不到它，只能先校验
// manifest.json 的原始字节。
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

// TestImportManifestNewDeckNameOverrideAlsoLimited 断言 opts.NewDeckName 覆盖同样受
// 卡组名界限约束（它写进的是同一列，绕过校验等于留后门）。
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

// TestImportManifestEmptyNameFallsBack 断言空卡组名仍回退为 Imported deck 并通过。
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

package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/anki"
)

// ankiFixtureWithMedia rebuilds a legacy fixture archive whose media index and numbered
// payloads are replaced. The collection is kept, so the result is a valid .apkg with a known
// media set.
func ankiFixtureWithMedia(t *testing.T, fixture string, dataByKey map[string][]byte, names map[string]string) []byte {
	t.Helper()
	raw, err := os.ReadFile(ankiFixturePath(fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	entries := make(map[string][]byte, len(zr.File)+len(dataByKey))
	for _, f := range zr.File {
		if f.Name == "media" {
			continue
		}
		if _, numeric := strconv.Atoi(f.Name); numeric == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = data
	}
	index := make(map[string]string, len(dataByKey))
	for key, data := range dataByKey {
		entries[key] = data
		index[key] = names[key]
	}
	rawIndex, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("encode media index: %v", err)
	}
	entries["media"] = rawIndex

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	namesList := make([]string, 0, len(entries))
	for name := range entries {
		namesList = append(namesList, name)
	}
	sort.Strings(namesList)
	for _, name := range namesList {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write(entries[name]); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return buf.Bytes()
}

// tinyAnkiMedia returns two small media entries, enough to make the archive hold more entries
// and bytes than a deliberately low cap allows.
func tinyAnkiMedia() (map[string][]byte, map[string]string) {
	return map[string][]byte{
		"0": []byte("first media payload"),
		"1": []byte("second media payload"),
	}, map[string]string{
		"0": "placeholder.png",
		"1": "second.txt",
	}
}

// packageRows counts every table an import could touch, so a rejection proves it wrote nothing.
func packageRows(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	out := make(map[string]int64)
	for name, model := range map[string]any{
		"decks":       &Deck{},
		"presets":     &Preset{},
		"note_types":  &NoteType{},
		"notes":       &Note{},
		"cards":       &Card{},
		"media":       &Media{},
		"media_names": &MediaName{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		out[name] = count
	}
	return out
}

// countFiles counts the regular files below root.
func countFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return count
}

// TestAnkiImportCapsRejectWithoutSideEffects covers the three deployment caps: each rejects
// with package_too_large and leaves no deck, media row or file behind.
func TestAnkiImportCapsRejectWithoutSideEffects(t *testing.T) {
	data, names := tinyAnkiMedia()
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg", data, names)
	cases := []struct {
		name   string
		limits PackageImportLimits
	}{
		{"package bytes", PackageImportLimits{MaxPackageBytes: 16}},
		{"entry count", PackageImportLimits{MaxEntries: 2}},
		{"unpacked bytes", PackageImportLimits{MaxUnpackedBytes: 16}},
	}
	for driver, db := range packageDatabases(t) {
		for _, tc := range cases {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				owner := seedUsers(t, db, "anki_cap_"+strings.ReplaceAll(tc.name, " ", "_"))[0]
				mediaRoot := t.TempDir()
				_, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
					Target: PackageTargetNewDeck, MediaRoot: mediaRoot, Limits: tc.limits,
				})
				if err == nil {
					t.Fatal("import succeeded under a low cap")
				}
				var pe *PackageError
				if !errors.As(err, &pe) || pe.Code != CodePackageTooLarge {
					t.Fatalf("err = %v, want package_too_large", err)
				}
				for name, count := range packageRows(t, db) {
					if count != 0 {
						t.Errorf("%s has %d rows after a rejected import", name, count)
					}
				}
				if files := countFiles(t, mediaRoot); files != 0 {
					t.Errorf("%d files under the media root after a rejected import", files)
				}
			})
		}
	}
}

// TestAnkiImportStreamsLargeMediaSet imports a generated package with 2500 media entries: more
// than the old 2000-entry cap, and more than the old reader could hold comfortably.
func TestAnkiImportStreamsLargeMediaSet(t *testing.T) {
	const mediaCount = 2500
	data := make(map[string][]byte, mediaCount)
	names := make(map[string]string, mediaCount)
	for i := 0; i < mediaCount; i++ {
		key := strconv.Itoa(i)
		data[key] = []byte(fmt.Sprintf("generated media payload %d", i))
		names[key] = fmt.Sprintf("file-%d.txt", i)
	}
	// Keep the fixture's own reference resolvable, so the report has no missing media noise.
	names["0"] = "placeholder.png"
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg", data, names)

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_many_media")[0]
			mediaRoot := t.TempDir()
			report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target: PackageTargetNewDeck, MediaRoot: mediaRoot,
			})
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if report.MediaNew != mediaCount {
				t.Fatalf("media_new = %d, want %d", report.MediaNew, mediaCount)
			}
			var mediaRows, nameRows int64
			if err := db.Model(&Media{}).Count(&mediaRows).Error; err != nil {
				t.Fatalf("count media: %v", err)
			}
			if err := db.Model(&MediaName{}).Where("deck_id = ?", report.DeckID).Count(&nameRows).Error; err != nil {
				t.Fatalf("count media names: %v", err)
			}
			if mediaRows != mediaCount || nameRows != mediaCount {
				t.Fatalf("media rows = %d, name rows = %d, want %d each", mediaRows, nameRows, mediaCount)
			}
			if files := countFiles(t, mediaRoot); files != mediaCount {
				t.Fatalf("files under the media root = %d, want %d", files, mediaCount)
			}
		})
	}
}

// TestAnkiImportRemovesTemporaryFiles checks the spooled package and the extracted collection
// are gone after a successful import and after a failed one.
func TestAnkiImportRemovesTemporaryFiles(t *testing.T) {
	data, names := tinyAnkiMedia()
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg", data, names)

	for _, tc := range []struct {
		name    string
		archive []byte
	}{
		{"success", raw},
		{"failure", []byte("this is not a zip archive")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			mediaRoot := t.TempDir()
			db := packageDatabases(t)["sqlite"]
			owner := seedUsers(t, db, "anki_tmp_"+tc.name)[0]
			_, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(tc.archive), PackageImportOptions{
				Target: PackageTargetNewDeck, MediaRoot: mediaRoot,
			})
			if tc.name == "success" && err != nil {
				t.Fatalf("import: %v", err)
			}
			if tc.name == "failure" && err == nil {
				t.Fatal("import of garbage succeeded")
			}
			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatalf("read TMPDIR: %v", err)
			}
			for _, entry := range entries {
				t.Errorf("temporary file left behind: %s", entry.Name())
			}
			// Media staging files live under the media root, not TMPDIR.
			if staged, err := os.ReadDir(filepath.Join(mediaRoot, "tmp")); err == nil {
				for _, entry := range staged {
					t.Errorf("media staging file left behind: %s", entry.Name())
				}
			}
		})
	}
}

// TestResolvePackageLimits covers the resolution order: environment variable, then settings
// table, then default, for all three package caps.
func TestResolvePackageLimits(t *testing.T) {
	db := packageDatabases(t)["sqlite"]
	ctx := context.Background()
	for _, env := range []string{
		EnvPackageMaxBytes, EnvPackageMaxEntries, EnvPackageMaxUnpackedBytes,
	} {
		t.Setenv(env, "")
	}
	if got := ResolvePackageLimits(ctx, db); got != DefaultPackageImportLimits() {
		t.Fatalf("defaults = %+v, want %+v", got, DefaultPackageImportLimits())
	}

	now := time.Now().UTC()
	for key, value := range map[string]string{
		SettingKeyPackageMaxBytes:         "11",
		SettingKeyPackageMaxEntries:       "12",
		SettingKeyPackageMaxUnpackedBytes: "13",
	} {
		if err := PutSetting(ctx, db, key, value, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", key, err)
		}
	}
	wantDB := PackageImportLimits{MaxPackageBytes: 11, MaxEntries: 12, MaxUnpackedBytes: 13}
	if got := ResolvePackageLimits(ctx, db); got != wantDB {
		t.Fatalf("settings = %+v, want %+v", got, wantDB)
	}

	t.Setenv(EnvPackageMaxBytes, "21")
	t.Setenv(EnvPackageMaxEntries, "22")
	t.Setenv(EnvPackageMaxUnpackedBytes, "23")
	wantEnv := PackageImportLimits{MaxPackageBytes: 21, MaxEntries: 22, MaxUnpackedBytes: 23}
	if got := ResolvePackageLimits(ctx, db); got != wantEnv {
		t.Fatalf("environment = %+v, want %+v", got, wantEnv)
	}
}

// TestHashAnkiMediaEnforcesStreamedCaps proves the decompressed total and the per-entry cap are
// charged while streaming, which is what bounds a modern zstd payload whose compressed size is
// below the archive cap.
func TestHashAnkiMediaEnforcesStreamedCaps(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1024)
	entry := testMediaEntry("a.txt", payload)
	pkg := &anki.Package{UnpackedBytes: 100, Media: []anki.MediaEntry{entry}}

	limits := anki.DefaultLimits()
	limits.MaxTotalBytes = 500
	if _, err := hashAnkiMedia(pkg, limits); !errors.Is(err, anki.ErrTooLarge) {
		t.Fatalf("total cap err = %v, want ErrTooLarge", err)
	}

	limits = anki.DefaultLimits()
	limits.MaxFileBytes = 16
	if _, err := hashAnkiMedia(pkg, limits); !errors.Is(err, anki.ErrTooLarge) {
		t.Fatalf("per-entry cap err = %v, want ErrTooLarge", err)
	}
}

// TestAnkiHashRejectsAdvertisedSha1Mismatch keeps the reader's old content check: a payload that
// does not match the digest the package advertises is corrupt.
func TestAnkiHashRejectsAdvertisedSha1Mismatch(t *testing.T) {
	data, names := tinyAnkiMedia()
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg", data, names)
	// Rebuild the modern fixture so the declared sha1 can disagree with the payload.
	pkg, err := anki.Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	pkg.Media[0].SHA1 = make([]byte, 20)
	if _, err := hashAnkiMedia(pkg, anki.DefaultLimits()); !errors.Is(err, anki.ErrCorruptMedia) {
		t.Fatalf("hashAnkiMedia err = %v, want ErrCorruptMedia", err)
	}
}

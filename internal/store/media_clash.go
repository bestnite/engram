// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/media/files.rs at tag 26.09.3 of ankitects/anki: only the file-name clash branch
// (add_data_to_folder_uniquely and add_hash_suffix_to_file_stem) is ported. Anki's filename sanitisation
// (problem characters, Windows device names, nonbreaking spaces, invisible-character removal) is NOT ported;
// this repository stores deck-scoped names as written and rejects nothing.

package store

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// maxMediaFilenameLength is Anki's 120-byte cap on a media file name. It matches the limit the import path enforces
// so a renamed file stays a valid media name.
const maxMediaFilenameLength = 120

// addHashSuffixToFileStem turns foo.jpg into foo-<sha1>.jpg, truncating the stem so the whole name stays within
// maxMediaFilenameLength bytes with the extension capped at 10 bytes.
//
// The suffix is SHA-1 of the file bytes because that is the hash Anki uses for this rename: the two implementations must
// choose the same name for the same clash, or re-importing an Anki-exported deck would keep producing new files.
func addHashSuffixToFileStem(fname, sha1hex string) string {
	// The suffix is 40 bytes plus the hyphen.
	maxLen := maxMediaFilenameLength - 40 - 1
	stem, ext := splitAndTruncateFilename(fname, maxLen)
	return fmt.Sprintf("%s-%s.%s", stem, sha1hex, ext)
}

// splitAndTruncateFilename splits at the last dot and trims both parts so stem + "." + ext stays within maxBytes.
// An extension longer than 10 bytes is capped, so the stem length can never go negative.
func splitAndTruncateFilename(fname string, maxBytes int) (stem, ext string) {
	i := strings.LastIndex(fname, ".")
	if i < 0 {
		stem, ext = fname, ""
	} else {
		stem, ext = fname[:i], fname[i+1:]
	}
	ext = truncatedToCharBoundary(ext, 10)
	// Allow for the dot and a trailing underscore.
	stemLen := maxBytes - len(ext) - 2
	stem = truncatedToCharBoundary(stem, stemLen)
	return stem, ext
}

// truncatedToCharBoundary returns at most max bytes of s without splitting a UTF-8 sequence.
func truncatedToCharBoundary(s string, max int) string {
	if max >= len(s) {
		return s
	}
	if max < 0 {
		max = 0
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// addDataToFolderUniquely returns the deck-scoped name under which the new bytes are registered.
//
// The rule is Anki's: use the NFC-normalised name when it is free or already holds the same bytes; otherwise try the
// lowercased name once, because Anki treats names case-insensitively when checking clashes; if the name is still taken
// by different bytes, append the SHA-1 suffix. existing maps a normalised name to the sha256 of its current content.
func addDataToFolderUniquely(existing map[string]string, desiredName, sha256hex, sha1hex string) string {
	normalized := normaliseMediaName(desiredName)
	if sha, ok := existing[normalized]; ok && sha == sha256hex {
		return normalized
	}
	if lower := strings.ToLower(normalized); lower != normalized {
		return addDataToFolderUniquely(existing, lower, sha256hex, sha1hex)
	}
	if _, ok := existing[normalized]; !ok {
		return normalized
	}
	return addHashSuffixToFileStem(normalized, sha1hex)
}

// renamePackageMedia implements Anki's clash behaviour for a deck package import. It renames every package media name
// whose content differs from the target deck's existing name, rewrites this package's references in note fields and
// note-type templates/CSS/LaTeX, and updates the package entries so the renamed names are what the import registers.
//
// A name that is absent, or already points at the same bytes, is left alone. The old name keeps its old content; only the
// new bytes move to the suffixed name.
func renamePackageMedia(ctx context.Context, tx *gorm.DB, deckID uint64, pkg *packageModel) error {
	rows, err := NewMediaNameStore(tx).ListByDeck(ctx, deckID)
	if err != nil {
		return fmt.Errorf("import package: list existing media names: %w", err)
	}
	existing := make(map[string]string, len(rows))
	for _, row := range rows {
		existing[row.Name] = row.Sha256
	}

	renames := map[string]string{}
	// Iterate the shas in a stable order: two entries can contend for the same name, and map order must not decide
	// which one keeps it.
	shas := make([]string, 0, len(pkg.Media))
	for sha := range pkg.Media {
		shas = append(shas, sha)
	}
	sort.Strings(shas)
	for _, sha := range shas {
		entry := pkg.Media[sha]
		raw, ok := pkg.MediaRaw[sha]
		if !ok {
			continue
		}
		names := entry.Names
		if len(names) == 0 {
			names = []string{path.Base(entry.Path)}
		}
		sum := sha1.Sum(raw)
		suffix := hex.EncodeToString(sum[:])
		sum256 := sha256.Sum256(raw)
		sha256hex := hex.EncodeToString(sum256[:])
		renamed := make([]string, 0, len(names))
		for _, name := range names {
			oldName := normaliseMediaName(name)
			newName := addDataToFolderUniquely(existing, oldName, sha256hex, suffix)
			if newName != oldName {
				renames[oldName] = newName
			}
			// Reserve the chosen name for later entries, mirroring Anki writing each file before examining the next.
			existing[newName] = sha256hex
			renamed = append(renamed, newName)
		}
		entry.Names = renamed
		pkg.Media[sha] = entry
	}
	if len(renames) == 0 {
		return nil
	}
	for i := range pkg.Notes {
		for key, value := range pkg.Notes[i].Fields {
			pkg.Notes[i].Fields[key] = rewriteMediaRefs(value, renames)
		}
	}
	for i := range pkg.NoteTypes {
		cfg := &pkg.NoteTypes[i].Config
		for j := range cfg.Templates {
			cfg.Templates[j].QFormat = rewriteMediaRefs(cfg.Templates[j].QFormat, renames)
			cfg.Templates[j].AFormat = rewriteMediaRefs(cfg.Templates[j].AFormat, renames)
		}
		cfg.CSS = rewriteMediaRefs(cfg.CSS, renames)
		cfg.LaTeXPre = rewriteMediaRefs(cfg.LaTeXPre, renames)
		cfg.LaTeXPost = rewriteMediaRefs(cfg.LaTeXPost, renames)
	}
	return nil
}

// rewriteMediaRefs replaces media names only inside the three reference positions, so ordinary text that happens to
// equal a file name is left alone.
func rewriteMediaRefs(value string, renames map[string]string) string {
	if len(renames) == 0 {
		return value
	}
	value = mediaSrcRE.ReplaceAllStringFunc(value, func(match string) string {
		sub := mediaSrcRE.FindStringSubmatch(match)
		return replaceMediaRefName(match, sub[1]+sub[2]+sub[3], renames)
	})
	value = mediaHrefRE.ReplaceAllStringFunc(value, func(match string) string {
		sub := mediaHrefRE.FindStringSubmatch(match)
		return replaceMediaRefName(match, sub[1]+sub[2]+sub[3], renames)
	})
	value = mediaSoundRE.ReplaceAllStringFunc(value, func(match string) string {
		sub := mediaSoundRE.FindStringSubmatch(match)
		return replaceMediaRefName(match, sub[1], renames)
	})
	return mediaCSSURLRE.ReplaceAllStringFunc(value, func(match string) string {
		sub := mediaCSSURLRE.FindStringSubmatch(match)
		return replaceMediaRefName(match, sub[1]+sub[2]+sub[3], renames)
	})
}

// replaceMediaRefName substitutes one captured reference name when it is in the rename set.
func replaceMediaRefName(match, name string, renames map[string]string) string {
	key := normaliseMediaName(strings.TrimPrefix(strings.TrimSpace(name), "./"))
	newName, ok := renames[key]
	if !ok {
		return match
	}
	return strings.Replace(match, name, newName, 1)
}

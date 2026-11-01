// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/media/files.rs at tag 26.09.3 of ankitects/anki: only the file-name clash branch
// (add_data_to_folder_uniquely and add_hash_suffix_to_file_stem) is ported. Anki's filename sanitisation
// (problem characters, Windows device names, nonbreaking spaces, invisible-character removal) is NOT ported;
// this repository stores deck-scoped names as written and rejects nothing.

package store

import (
	"fmt"
	"strings"
	"unicode/utf8"
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

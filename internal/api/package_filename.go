package api

import (
	"mime"
	"strings"
	"unicode"
)

// packageFilenameMaxRunes bounds the length of the download filename body: deck names are up to 200 characters, and concatenating them verbatim into a filename
// would exceed the 255-byte limit of some filesystems (one Chinese character takes 3 bytes).
const packageFilenameMaxRunes = 80

// DeckPackageFilename returns the download filename when exporting a deck package: deck title + .edeck.
//
// Characters in the title that are illegal or carry special meaning on common filesystems (path separators, Windows reserved characters, control characters)
// become underscores, runs of whitespace collapse to one space, and leading/trailing spaces and dots are stripped (Windows forbids trailing dots or spaces).
// When sanitizing leaves nothing it falls back to deck-<external id>, so a usable name always exists; the auto-increment primary key never appears in a filename.
func DeckPackageFilename(deckName, publicID string) string {
	var b strings.Builder
	space := false
	for _, r := range deckName {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r):
			r = '_'
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
	}
	name := strings.Trim(b.String(), " .")
	if runes := []rune(name); len(runes) > packageFilenameMaxRunes {
		name = strings.TrimRight(string(runes[:packageFilenameMaxRunes]), " .")
	}
	if name == "" {
		name = "deck-" + publicID
	}
	return name + ".edeck"
}

// AttachmentDisposition builds the Content-Disposition for downloads. When the filename contains non-ASCII characters (e.g. a Chinese deck name)
// it is encoded per RFC 2231/6266 as a filename* UTF-8 percent-escaped form, with the standard library doing the escaping and no hand-built quotes.
func AttachmentDisposition(filename string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

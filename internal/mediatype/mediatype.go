// Package mediatype does exactly one thing: decide the media type from file bytes (magic bytes) and compare it against the allowlist.
//
// It is a leaf package: it depends only on the standard library, importing neither internal/store nor the storage portion of internal/media.
// That way the ordinary upload path (internal/media) and the deck-package import path (internal/store) share one detector and one allowlist,
// avoiding both duplicate implementations and a store → media import cycle.
package mediatype

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// HeadBytes is the maximum number of bytes needed for detection; callers need only read this many (fewer if the file is shorter) to decide.
// It is also part of the external contract: both paths read the file header at this length so detection results agree.
const HeadBytes = 512

// DefaultAllowedMimes is the default value of the media allowlist.
func DefaultAllowedMimes() []string {
	return []string{
		"image/png", "image/jpeg", "image/webp", "image/gif",
		"audio/mpeg", "audio/ogg", "audio/mp4",
	}
}

// Detect determines the media type from magic bytes, returning the mime, the extension and whether detection succeeded.
// It only recognizes the types covered by the allowlist; the extension comes from Ext and is never taken from the filename.
func Detect(head []byte) (mime, ext string, ok bool) {
	mime = detectMime(head)
	if mime == "" {
		return "", "", false
	}
	return mime, Ext(mime), true
}

func detectMime(head []byte) string {
	switch {
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case len(head) >= 6 && (bytes.Equal(head[:6], []byte("GIF87a")) || bytes.Equal(head[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp"
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("OggS")):
		return "audio/ogg"
	case len(head) >= 3 && bytes.Equal(head[:3], []byte("ID3")):
		return "audio/mpeg"
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		// MPEG audio frame sync (mp3 without an ID3 tag).
		return "audio/mpeg"
	case isISOBaseMedia(head):
		// m4a belongs to ISO BMFF: offset 4 onward is "ftyp".
		return "audio/mp4"
	}
	return ""
}

// Ext maps a mime to the file extension used in the content-addressed path; unknown types fall back to bin.
// It is the only mime-to-extension table, so an upload and a package import of the same bytes land on the same path.
func Ext(mime string) string {
	switch Normalize(mime) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "audio/mpeg":
		return "mp3"
	case "audio/ogg":
		return "ogg"
	case "audio/mp4":
		return "m4a"
	default:
		return "bin"
	}
}

// isISOBaseMedia checks the ISO BMFF header (ftyp box); m4a/mp4 share it.
func isISOBaseMedia(head []byte) bool {
	if len(head) < 12 {
		return false
	}
	if !bytes.Equal(head[4:8], []byte("ftyp")) {
		return false
	}
	// The box size is a big-endian integer in the first 4 bytes and must cover at least the brand field, guarding against malformed input.
	size := binary.BigEndian.Uint32(head[:4])
	return size >= 8
}

// Normalize canonicalizes common equivalent spellings so they can be compared against detection results.
func Normalize(raw string) string {
	m := strings.ToLower(strings.TrimSpace(strings.SplitN(raw, ";", 2)[0]))
	switch m {
	case "image/jpg":
		return "image/jpeg"
	case "audio/m4a", "audio/x-m4a":
		return "audio/mp4"
	case "audio/mp3":
		return "audio/mpeg"
	}
	return m
}

// Allowed reports whether mime is in the allowed allowlist; both sides are normalized first, tolerating equivalents like image/jpg.
func Allowed(mime string, allowed []string) bool {
	m := Normalize(mime)
	for _, a := range allowed {
		if Normalize(a) == m {
			return true
		}
	}
	return false
}

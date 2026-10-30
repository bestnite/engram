// Package mediatype classifies media from its file bytes and, when the bytes are unrecognised, from its file name.
//
// It never rejects a file: every upload and every package import is accepted, and the classification decides only
// whether a stored medium may be served inline (image, audio, video) or must be downloaded as an attachment.
//
// It is a leaf package: it depends only on the standard library, importing neither internal/store nor the storage portion
// of internal/media. That way the ordinary upload path (internal/media) and the deck-package import path (internal/store)
// share one classifier instead of keeping two detection tables that could drift apart.
package mediatype

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// HeadBytes is the maximum number of bytes needed for detection; callers need only read this many (fewer if the file is shorter) to classify.
// It is also part of the external contract: both paths read the file header at this length so detection results agree.
const HeadBytes = 512

// Class is the kind of a medium, which decides how it is served.
type Class string

const (
	ClassImage  Class = "image"
	ClassAudio  Class = "audio"
	ClassVideo  Class = "video"
	ClassFont   Class = "font"
	ClassScript Class = "script"
	ClassStyle  Class = "style"
	ClassOther  Class = "other"
)

// IsInline reports whether the class may be served inline. Everything else is served as an attachment, because a browser
// must never render executable or otherwise untrusted content on the application's own origin.
func (c Class) IsInline() bool {
	return c == ClassImage || c == ClassAudio || c == ClassVideo
}

// Classify determines a medium's mime type and class. It never fails: bytes are recognised first, and when the bytes are
// unrecognised the file-name extension decides the text classes (.js/.mjs/.cjs, .css); anything else is
// application/octet-stream and ClassOther. The extension never overrides recognised bytes, so a renamed executable cannot
// claim an image type.
func Classify(head []byte, name string) (mime string, class Class) {
	if mime = detectMime(head); mime != "" {
		return mime, ClassOfMime(mime)
	}
	switch fileExt(name) {
	case "js", "mjs", "cjs":
		return "text/javascript", ClassScript
	case "css":
		return "text/css", ClassStyle
	}
	return "application/octet-stream", ClassOther
}

// ClassOfMime maps a stored mime type to its class. It is the read-side counterpart of Classify: a row written by an old
// import chain may carry a type Classify no longer produces, and the serving rule must still classify it consistently.
func ClassOfMime(mime string) Class {
	switch m := Normalize(mime); {
	case strings.HasPrefix(m, "image/"):
		return ClassImage
	case strings.HasPrefix(m, "audio/"):
		return ClassAudio
	case strings.HasPrefix(m, "video/"):
		return ClassVideo
	case strings.HasPrefix(m, "font/"):
		return ClassFont
	case m == "text/javascript":
		return ClassScript
	case m == "text/css":
		return ClassStyle
	default:
		return ClassOther
	}
}

// fileExt returns the lowercased extension without the dot, or "" when the name has none.
func fileExt(name string) string {
	i := strings.LastIndex(name, ".")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[i+1:])
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
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WAVE")):
		return "audio/wav"
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("fLaC")):
		return "audio/flac"
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("OggS")):
		if bytes.Contains(head, []byte("OpusHead")) {
			return "audio/opus"
		}
		return "audio/ogg"
	case len(head) >= 3 && bytes.Equal(head[:3], []byte("ID3")):
		return "audio/mpeg"
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		// MPEG audio frame sync (mp3 without an ID3 tag).
		return "audio/mpeg"
	case isWebM(head):
		return "video/webm"
	case isISOBaseMedia(head):
		switch string(head[8:12]) {
		case "avif", "avis":
			return "image/avif"
		case "M4A ", "M4B ":
			return "audio/mp4"
		default:
			return "video/mp4"
		}
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("wOFF")):
		return "font/woff"
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("wOF2")):
		return "font/woff2"
	case len(head) >= 4 && (bytes.Equal(head[:4], []byte{0x00, 0x01, 0x00, 0x00}) || bytes.Equal(head[:4], []byte("true"))):
		return "font/ttf"
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("OTTO")):
		return "font/otf"
	case isSVG(head):
		return "image/svg+xml"
	}
	return ""
}

// isSVG accepts an XML declaration or a root <svg element after an optional UTF-8 BOM and leading whitespace.
// The root tag is matched case-insensitively because XML tag names are case-sensitive in principle but exporters vary.
func isSVG(head []byte) bool {
	trimmed := bytes.TrimPrefix(head, []byte{0xEF, 0xBB, 0xBF})
	trimmed = bytes.TrimLeft(trimmed, " \t\r\n")
	lower := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lower, []byte("<svg")) {
		return true
	}
	return bytes.HasPrefix(lower, []byte("<?xml")) && bytes.Contains(lower, []byte("<svg"))
}

// isISOBaseMedia checks the ISO BMFF header (ftyp box); mp4, m4a and avif share it.
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

// isWebM checks the EBML header and the "webm" doctype. Matroska uses the same EBML signature with a different doctype,
// so the doctype must be present before the type is called video/webm.
func isWebM(head []byte) bool {
	if len(head) < 4 || !bytes.Equal(head[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		return false
	}
	return bytes.Contains(bytes.ToLower(head), []byte("webm"))
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
	case "image/svg+xml":
		return "svg"
	case "image/avif":
		return "avif"
	case "audio/mpeg":
		return "mp3"
	case "audio/ogg":
		return "ogg"
	case "audio/opus":
		return "opus"
	case "audio/mp4":
		return "m4a"
	case "audio/wav":
		return "wav"
	case "audio/flac":
		return "flac"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "font/woff":
		return "woff"
	case "font/woff2":
		return "woff2"
	case "font/ttf":
		return "ttf"
	case "font/otf":
		return "otf"
	case "text/javascript":
		return "js"
	case "text/css":
		return "css"
	default:
		return "bin"
	}
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
	case "audio/x-wav", "audio/wave":
		return "audio/wav"
	case "audio/x-flac":
		return "audio/flac"
	case "application/javascript", "application/x-javascript":
		return "text/javascript"
	}
	return m
}

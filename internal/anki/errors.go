package anki

import "errors"

// Stable error values. Each distinguishes one failure class so callers can
// answer with a specific message; the wrapping text names the offending entry
// or version.
var (
	// ErrBadArchive reports bytes that are not a zip archive.
	ErrBadArchive = errors.New("anki package: not a valid zip archive")
	// ErrUnsafeEntry reports an entry or media name that is not a single
	// normal path component.
	ErrUnsafeEntry = errors.New("anki package: unsafe entry path")
	// ErrNoCollection reports an archive without the collection file the
	// detected layout requires.
	ErrNoCollection = errors.New("anki package: no collection entry")
	// ErrCorruptMeta reports a meta entry that is not valid protobuf.
	ErrCorruptMeta = errors.New("anki package: cannot decode meta")
	// ErrUnsupportedVersion reports a meta version this reader does not know.
	ErrUnsupportedVersion = errors.New("anki package: unsupported package version")
	// ErrCorruptZstd reports a zstd payload that does not decode.
	ErrCorruptZstd = errors.New("anki package: corrupt zstd stream")
	// ErrCorruptCollection reports a collection that cannot be decoded.
	ErrCorruptCollection = errors.New("anki package: cannot decode collection")
	// ErrCorruptMedia reports a media table or payload that cannot be decoded.
	ErrCorruptMedia = errors.New("anki package: cannot decode media")
	// ErrTooLarge reports an archive that exceeds one of the Limits caps.
	ErrTooLarge = errors.New("anki package: exceeds the size limit")
)

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/retention"
)

// This file is the read side of the global media browser: one page of the readable set together with its deck-scoped
// names, the visible decks it belongs to and its reclamation state.
//
// The readable set is not redefined here. Every query starts from readableMediaUnionQuery, the same union the read
// decision and ListReadableMedia use, so a medium outside 'supplied by me ∪ referenced by a live note in a deck I can
// see' cannot appear in an item, in a name, in a deck label or in a page.
//
// The reference state is derived from the reclamation job's own definition, not a second copy of it: mediaAnyReferenceCondition
// and mediaNoteTypeReferenceCondition are the two halves of the job's orphanCondition, so the page and the collector cannot
// disagree about what is referenced.

// The four reference states a medium can be in. They are the page's filter values and the item's `state` field.
const (
	// MediaStateReferenced means a live note or a note type's `_`-prefixed asset reference resolves to the bytes.
	MediaStateReferenced = "referenced"
	// MediaStateRestorable means only mappings of soft-deleted notes point at the bytes: the collector keeps them so a
	// restored note can still show them.
	MediaStateRestorable = "restorable"
	// MediaStatePending means the collector's orphan condition holds and the orphan stamp is already written, but the
	// grace period has not necessarily elapsed and no sweep has reclaimed the bytes yet.
	MediaStatePending = "pending"
	// MediaStateUnreferenced means the collector's orphan condition holds and no orphan stamp is written yet: the next
	// sweep starts the grace clock.
	MediaStateUnreferenced = "unreferenced"
)

var (
	// ErrInvalidMediaState means the state filter is not one of the four reference states.
	ErrInvalidMediaState = errors.New("store: invalid media reference state")
	// ErrInvalidMediaClass means the class filter is not one of the seven media classes.
	ErrInvalidMediaClass = errors.New("store: invalid media class")
)

// mediaClasses is the set of class filter values, keyed by the names mediatype.Class uses. It stays a local literal
// rather than an import of mediatype's constants so the SQL mapping below and this validation list sit side by side;
// media_library_test.go proves the mapping against mediatype.ClassOfMime for every class.
var mediaClasses = map[string]struct{}{
	"image":  {},
	"audio":  {},
	"video":  {},
	"font":   {},
	"script": {},
	"style":  {},
	"other":  {},
}

// mediaStates is the set of state filter values.
var mediaStates = map[string]struct{}{
	MediaStateReferenced:   {},
	MediaStateRestorable:   {},
	MediaStatePending:      {},
	MediaStateUnreferenced: {},
}

// IsMediaClass reports whether class names one of the seven media classes a `class` filter accepts.
func IsMediaClass(class string) bool {
	_, ok := mediaClasses[class]
	return ok
}

// IsMediaState reports whether state names one of the four reference states a `state` filter accepts.
func IsMediaState(state string) bool {
	_, ok := mediaStates[state]
	return ok
}

// mediaLiveReferenceCondition is the portable SQL condition for 'a live (not soft-deleted) note references these bytes'.
//
// It is the difference between referenced and restorable: when every mapping belongs to a soft-deleted note the medium is
// restorable. The condition is intentionally not scoped to the caller's visible decks: the state must report what the
// reclamation job will do with the bytes, and the job looks at the whole instance.
const mediaLiveReferenceCondition = "EXISTS (SELECT 1 FROM media_notes AS live_ref " +
	"JOIN notes AS live_note ON live_note.id = live_ref.note_id AND live_note.deleted_at IS NULL " +
	"WHERE live_ref.media_sha = media.sha256)"

// mediaClassExpression maps a stored mime to its media class in portable SQL. It matches mediatype.ClassOfMime: the
// classifier lowercases and trims the mime, drops a `;` parameter suffix and aliases a few historical spellings, and
// this expression repeats exactly the parts that can change the class.
//
// The script aliases are the only aliases that change a class (application/javascript and application/x-javascript
// normalise to text/javascript); every other alias keeps its prefix, so the prefix branches already classify it. The
// parameter suffix is spelled out for the two exact-match text classes because a stored `text/javascript; charset=…`
// normalises to text/javascript.
const mediaClassExpression = "CASE " +
	"WHEN LOWER(TRIM(media.mime)) LIKE 'image/%' THEN 'image' " +
	"WHEN LOWER(TRIM(media.mime)) LIKE 'audio/%' THEN 'audio' " +
	"WHEN LOWER(TRIM(media.mime)) LIKE 'video/%' THEN 'video' " +
	"WHEN LOWER(TRIM(media.mime)) LIKE 'font/%' THEN 'font' " +
	"WHEN LOWER(TRIM(media.mime)) IN ('text/javascript', 'application/javascript', 'application/x-javascript') " +
	"OR LOWER(TRIM(media.mime)) LIKE 'text/javascript;%' " +
	"OR LOWER(TRIM(media.mime)) LIKE 'application/javascript;%' " +
	"OR LOWER(TRIM(media.mime)) LIKE 'application/x-javascript;%' THEN 'script' " +
	"WHEN LOWER(TRIM(media.mime)) = 'text/css' OR LOWER(TRIM(media.mime)) LIKE 'text/css;%' THEN 'style' " +
	"ELSE 'other' END"

// MediaLibraryFilter narrows a media library page; a zero value means 'no filter'.
//
// DeckID is a deck primary key, not a public id: the transport resolves the external id and checks that the caller can
// read the deck before setting it. Query is the substring matched against visible deck-scoped names.
type MediaLibraryFilter struct {
	DeckID uint64
	Class  string
	State  string
	Query  string
}

// MediaLibraryDeck is one visible deck that references a page medium: its external id and display name.
type MediaLibraryDeck struct {
	PublicID string
	Name     string
}

// MediaLibraryEntry is one medium of a library page: the stored row plus the labels and reclamation state the browser
// shows. Names and Decks are never nil (an empty page still serialises `[]`), and both only contain values from decks the
// caller can see, so a sha shared between users never leaks another user's file name or deck.
type MediaLibraryEntry struct {
	Media
	// Names are the deck-scoped names visible decks registered for these bytes, deduplicated and sorted.
	Names []string
	// Decks are the visible decks that hold a name for the bytes or reference them from a live note, sorted by name.
	Decks []MediaLibraryDeck
	// State is one of the four MediaState* values.
	State string
	// CleanupAt is when a sweep may remove the bytes; it is set only for a pending medium and comes from the server's
	// retention grace, never from a client constant.
	CleanupAt *time.Time
}

// mediaLibraryRow is one scanned page row: the media columns plus the three reference flags derived from the collector's
// conditions, so an item's state needs no additional query.
type mediaLibraryRow struct {
	Sha256     string     `gorm:"column:sha256"`
	RelPath    string     `gorm:"column:rel_path"`
	Mime       string     `gorm:"column:mime"`
	Bytes      int64      `gorm:"column:bytes"`
	Width      *int       `gorm:"column:width"`
	Height     *int       `gorm:"column:height"`
	CreatedBy  *uint64    `gorm:"column:created_by"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	OrphanedAt *time.Time `gorm:"column:orphaned_at"`
	LiveRef    bool       `gorm:"column:live_ref"`
	TypeRef    bool       `gorm:"column:type_ref"`
	AnyRef     bool       `gorm:"column:any_ref"`
}

// mediaLibraryDeckRow is one scanned (medium, visible deck) pair of the batch deck lookup.
type mediaLibraryDeckRow struct {
	MediaSha string `gorm:"column:media_sha"`
	DeckID   uint64 `gorm:"column:deck_id"`
	PublicID string `gorm:"column:public_id"`
	Name     string `gorm:"column:name"`
}

// ListMediaLibrary returns one page of the readable media set ordered by created_at DESC, sha256 DESC, narrowed by
// filter, plus the cursor of the next page (an empty string means the end). The keyset cursor has the same shape and
// meaning as ListReadableMedia's, so a filter and a cursor compose without gaps or duplicates.
//
// The page costs one keyset query plus two bounded batch lookups (names, decks) for the page's shas, never one query per
// item. The result includes each item's reference state; CleanupAt is set only for a pending item.
func ListMediaLibrary(ctx context.Context, db *gorm.DB, userID uint64, limit int, cursor string, filter MediaLibraryFilter) ([]MediaLibraryEntry, string, error) {
	if db == nil || userID == 0 {
		return nil, "", nil
	}
	if limit < 1 {
		limit = DefaultMediaPageSize
	}
	if limit > MaxMediaPageSize {
		limit = MaxMediaPageSize
	}
	if filter.Class != "" && !IsMediaClass(filter.Class) {
		return nil, "", ErrInvalidMediaClass
	}
	stateCondition, err := mediaStateCondition(filter.State)
	if err != nil {
		return nil, "", err
	}

	q := db.WithContext(ctx).Table("(?) AS media", readableMediaUnionQuery(db, userID)).
		Select(strings.Join([]string{
			"media.sha256", "media.rel_path", "media.mime", "media.bytes", "media.width", "media.height",
			"media.created_by", "media.created_at", "media.orphaned_at",
			"(" + mediaLiveReferenceCondition + ") AS live_ref",
			"(" + mediaNoteTypeReferenceCondition + ") AS type_ref",
			"(" + mediaAnyReferenceCondition + ") AS any_ref",
		}, ", "))
	if cursor != "" {
		at, sha, err := decodeMediaCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		q = q.Where("(media.created_at < ? OR (media.created_at = ? AND media.sha256 < ?))", at, at, sha)
	}
	if filter.DeckID != 0 {
		// The contract's deck filter is wider than a name lookup: a live note in the deck and a registered name both
		// make the medium belong to it, and a name row alone (an upload registered before its note was saved) counts.
		q = q.Where("(EXISTS (SELECT 1 FROM media_notes AS deck_ref "+
			"JOIN notes AS deck_note ON deck_note.id = deck_ref.note_id AND deck_note.deleted_at IS NULL "+
			"WHERE deck_ref.media_sha = media.sha256 AND deck_note.deck_id = ?) "+
			"OR EXISTS (SELECT 1 FROM media_names AS deck_name "+
			"WHERE deck_name.sha256 = media.sha256 AND deck_name.deck_id = ?))", filter.DeckID, filter.DeckID)
	}
	if filter.Class != "" {
		q = q.Where(mediaClassExpression+" = ?", filter.Class)
	}
	if stateCondition != "" {
		q = q.Where(stateCondition)
	}
	if text := strings.TrimSpace(filter.Query); text != "" {
		// A case-insensitive substring of a visible deck-scoped name only: the name subquery is scoped to the caller's
		// visible decks, so a name registered in another user's deck cannot match. The user text is escaped so `%` and
		// `_` are literal characters, not wildcards.
		//
		// Both sides are folded: the pattern with strings.ToLower and the stored name with the database's Unicode-aware
		// lower-case. PostgreSQL's LOWER folds all of Unicode, while SQLite's built-in LOWER folds ASCII only, so SQLite
		// compares through the Go function registered in sqlite_lower.go (the same rule as WhereNoteTagsAny).
		fold := "LOWER(search_name.name)"
		if db.Dialector.Name() == "sqlite" {
			fold = sqliteLowerFunc + "(search_name.name)"
		}
		q = q.Where("EXISTS (SELECT 1 FROM media_names AS search_name "+
			"WHERE search_name.sha256 = media.sha256 AND search_name.deck_id IN (?) "+
			"AND "+fold+" LIKE ? ESCAPE '\\')",
			visibleDeckIDsQuery(db, userID), "%"+escapeMediaLike(strings.ToLower(text))+"%")
	}

	var rows []mediaLibraryRow
	if err := q.Order("media.created_at DESC, media.sha256 DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", fmt.Errorf("store: list media library: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeMediaCursor(Media{Sha256: rows[len(rows)-1].Sha256, CreatedAt: rows[len(rows)-1].CreatedAt})
	}

	shas := make([]string, 0, len(rows))
	for _, row := range rows {
		shas = append(shas, row.Sha256)
	}
	names, err := mediaLibraryNames(ctx, db, userID, shas)
	if err != nil {
		return nil, "", err
	}
	decks, err := mediaLibraryDecks(ctx, db, userID, shas)
	if err != nil {
		return nil, "", err
	}

	entries := make([]MediaLibraryEntry, 0, len(rows))
	for _, row := range rows {
		entry := MediaLibraryEntry{
			Media: Media{
				Sha256:     row.Sha256,
				RelPath:    row.RelPath,
				Mime:       row.Mime,
				Bytes:      row.Bytes,
				Width:      row.Width,
				Height:     row.Height,
				CreatedBy:  row.CreatedBy,
				CreatedAt:  row.CreatedAt,
				OrphanedAt: row.OrphanedAt,
			},
			Names: names[row.Sha256],
			Decks: decks[row.Sha256],
			State: mediaEntryState(row),
		}
		if entry.Names == nil {
			entry.Names = []string{}
		}
		if entry.Decks == nil {
			entry.Decks = []MediaLibraryDeck{}
		}
		if entry.State == MediaStatePending && row.OrphanedAt != nil {
			cleanupAt := row.OrphanedAt.Add(retention.MediaOrphanGrace)
			entry.CleanupAt = &cleanupAt
		}
		entries = append(entries, entry)
	}
	return entries, next, nil
}

// mediaStateCondition returns the SQL condition selecting one reference state, or "" for 'no state filter'. It uses the
// collector's orphanCondition for the two orphan states, so a state can never claim bytes the collector will keep are
// reclaimable (or the other way round).
func mediaStateCondition(state string) (string, error) {
	switch state {
	case "":
		return "", nil
	case MediaStateReferenced:
		return "(" + mediaLiveReferenceCondition + " OR " + mediaNoteTypeReferenceCondition + ")", nil
	case MediaStateRestorable:
		// Mappings exist but none belongs to a live note, and the note-type asset branch is false as well: the
		// collector keeps the bytes because a restore may need them.
		return "NOT " + mediaLiveReferenceCondition + " AND NOT " + mediaNoteTypeReferenceCondition +
			" AND " + mediaAnyReferenceCondition, nil
	case MediaStatePending:
		return "(" + orphanCondition + ") AND media.orphaned_at IS NOT NULL", nil
	case MediaStateUnreferenced:
		return "(" + orphanCondition + ") AND media.orphaned_at IS NULL", nil
	default:
		return "", ErrInvalidMediaState
	}
}

// mediaEntryState derives one row's state from the three reference flags and the orphan stamp, in the same order the
// collector would judge it: live/note-type references win over a stale stamp, mappings of soft-deleted notes come next,
// and only then does the orphan stamp decide pending versus unreferenced.
func mediaEntryState(row mediaLibraryRow) string {
	switch {
	case row.LiveRef || row.TypeRef:
		return MediaStateReferenced
	case row.AnyRef:
		return MediaStateRestorable
	case row.OrphanedAt != nil:
		return MediaStatePending
	default:
		return MediaStateUnreferenced
	}
}

// mediaLibraryNames loads the visible deck-scoped names of the page's media in one query, grouped by sha and sorted
// (a name shared by two visible decks appears once).
func mediaLibraryNames(ctx context.Context, db *gorm.DB, userID uint64, shas []string) (map[string][]string, error) {
	out := make(map[string][]string, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	var rows []MediaName
	if err := db.WithContext(ctx).Model(&MediaName{}).
		Where("sha256 IN ?", shas).
		Where("deck_id IN (?)", visibleDeckIDsQuery(db, userID)).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: list media library names: %w", err)
	}
	for _, row := range rows {
		out[row.Sha256] = append(out[row.Sha256], row.Name)
	}
	for sha, names := range out {
		sort.Strings(names)
		deduped := names[:0]
		for i, name := range names {
			if i == 0 || name != names[i-1] {
				deduped = append(deduped, name)
			}
		}
		out[sha] = deduped
	}
	return out, nil
}

// mediaLibraryDecks loads the visible decks that reference the page's media, either by a name row or by a live note, in
// two bounded queries rather than one per item.
func mediaLibraryDecks(ctx context.Context, db *gorm.DB, userID uint64, shas []string) (map[string][]MediaLibraryDeck, error) {
	out := make(map[string][]MediaLibraryDeck, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	byName := db.WithContext(ctx).Table("decks").
		Select("media_names.sha256 AS media_sha, decks.id AS deck_id, decks.public_id AS public_id, decks.name AS name").
		Joins("JOIN media_names ON media_names.deck_id = decks.id").
		Where("media_names.sha256 IN ?", shas).
		Where("decks.id IN (?)", visibleDeckIDsQuery(db, userID))
	byNote := db.WithContext(ctx).Table("decks").
		Select("media_notes.media_sha AS media_sha, decks.id AS deck_id, decks.public_id AS public_id, decks.name AS name").
		Joins("JOIN notes ON notes.deck_id = decks.id AND notes.deleted_at IS NULL").
		Joins("JOIN media_notes ON media_notes.note_id = notes.id").
		Where("media_notes.media_sha IN ?", shas).
		Where("decks.id IN (?)", visibleDeckIDsQuery(db, userID))
	var rows []mediaLibraryDeckRow
	for _, query := range []*gorm.DB{byName, byNote} {
		var page []mediaLibraryDeckRow
		if err := query.Find(&page).Error; err != nil {
			return nil, fmt.Errorf("store: list media library decks: %w", err)
		}
		rows = append(rows, page...)
	}
	seen := make(map[string]map[uint64]bool, len(shas))
	for _, row := range rows {
		if seen[row.MediaSha] == nil {
			seen[row.MediaSha] = make(map[uint64]bool)
		}
		if seen[row.MediaSha][row.DeckID] {
			continue
		}
		seen[row.MediaSha][row.DeckID] = true
		out[row.MediaSha] = append(out[row.MediaSha], MediaLibraryDeck{PublicID: row.PublicID, Name: row.Name})
	}
	for sha := range out {
		decks := out[sha]
		// Sorted by name, then by external id: two decks may legitimately share a name, and the order must be stable
		// across paging so a client rendering the same item twice shows the same list.
		sort.Slice(decks, func(i, j int) bool {
			if decks[i].Name != decks[j].Name {
				return decks[i].Name < decks[j].Name
			}
			return decks[i].PublicID < decks[j].PublicID
		})
		out[sha] = decks
	}
	return out, nil
}

// escapeMediaLike escapes LIKE wildcards in user text so a search for a literal `%` or `_` matches that character
// instead of acting as a wildcard. The escape character is a backslash, named by ESCAPE in the query.
func escapeMediaLike(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
}

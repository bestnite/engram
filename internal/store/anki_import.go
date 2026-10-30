package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/notetype"
)

// Anki package (.apkg) import.
//
// The reader in internal/anki stays a leaf: it knows the three archive layouts and returns one
// model whose note types, decks, notes and cards are in memory while each media entry is an
// opener over its decompressed stream. This file maps that model onto the data model; it hashes
// the media once and streams each write, so memory does not grow with the package's media. It
// lives in internal/store, not internal/anki, because the writes need the store's unexported
// transaction, media, quota and note-type helpers; importing internal/store from internal/anki
// would create a cycle and force those helpers to become public API.

// ankiLimits turns the deployment caps into the reader's limits. The per-entry allocation guard
// stays the reader's constant; only the entry count and decompressed total are deployment choices.
func (l PackageImportLimits) ankiLimits() anki.Limits {
	return anki.Limits{
		MaxEntries:    l.MaxEntries,
		MaxFileBytes:  anki.DefaultLimits().MaxFileBytes,
		MaxTotalBytes: l.MaxUnpackedBytes,
	}
}

// isAnkiArchive reports whether raw is an Anki package. The decision reads the zip entry names:
// every .apkg layout carries a collection entry and/or a "meta" entry, while an .edeck carries
// manifest.json and note_types.json instead. The file name is never consulted.
func isAnkiArchive(raw []byte) bool {
	return isAnkiArchiveReader(bytes.NewReader(raw), int64(len(raw)))
}

// isAnkiArchiveReader is the file-backed form: the import sniffs the spooled package through
// random access instead of holding the archive in memory.
func isAnkiArchiveReader(r io.ReaderAt, size int64) bool {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		switch f.Name {
		case "meta", "collection.anki2", "collection.anki21", "collection.anki21b":
			return true
		}
	}
	return false
}

// mapAnkiPackageError maps a reader error onto the deck package's stable codes, so corrupt bytes
// never become a 500. The archive caps reuse package_too_large; unsafe entry names reuse
// package_unsafe_entry; every other decode failure is package_bad_format.
func mapAnkiPackageError(err error) error {
	var pe *PackageError
	if errors.As(err, &pe) {
		return pe
	}
	switch {
	case errors.Is(err, anki.ErrTooLarge):
		return &PackageError{Code: CodePackageTooLarge, Message: "Anki package exceeds the allowed size"}
	case errors.Is(err, anki.ErrUnsafeEntry):
		return &PackageError{Code: CodePackageUnsafeEntry, Message: "Anki package contains an unsafe entry"}
	default:
		return &PackageError{Code: CodePackageBadFormat, Message: "not a valid Anki package", Entries: []string{err.Error()}}
	}
}

// ankiDeckPlan is the deck half of the mapping: the package's top-level deck becomes one engram
// deck, and every other package deck becomes a card path relative to it.
type ankiDeckPlan struct {
	TopName     string
	Description string
	// Paths maps a package deck id to the deck_path a card in that deck gets. An empty string
	// means the card sits directly in the engram deck and deck_path is NULL.
	Paths map[int64]string
}

// ankiDeckName strips the leading "::" an exported root deck may carry.
func ankiDeckName(name string) string {
	return strings.TrimLeft(strings.TrimSpace(name), ":")
}

// ankiDeckParts splits a normalised deck name into its "::" components. An empty name has no
// components, so it cannot contribute to, or contradict, the shared prefix.
func ankiDeckParts(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, "::")
}

// ankiSharedDeckPrefix returns the leading components two component lists share.
func ankiSharedDeckPrefix(a, b []string) []string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return a[:n]
}

// planAnkiDecks chooses the top-level deck and computes every subdeck path.
//
// Only decks that at least one package card references take part in the choice: an Anki package
// always carries the deck "Default", usually empty, and picking it would put every real deck
// below a deck the user never used. The top name is the longest "::" component prefix shared by
// the referenced deck names. When the referenced decks share no first component, the top name is
// the first component of the referenced deck holding the most cards; the lowest deck id wins a
// tie, so the choice does not depend on package order. The description comes from the package
// deck whose full normalised name equals the top name; it is empty when no such deck exists, and
// empty when the deck model's own rules (length bound, control characters) would reject it.
//
// A card in the deck whose full name equals the top name sits directly in the engram deck and
// gets no path; a card below it gets the path relative to the top name; any other card gets its
// full package deck name, so no card loses the deck it came from.
func planAnkiDecks(pkg *anki.Package) ankiDeckPlan {
	plan := ankiDeckPlan{Paths: map[int64]string{}}

	names := make(map[int64]string, len(pkg.Decks))
	for i := range pkg.Decks {
		names[pkg.Decks[i].ID] = ankiDeckName(pkg.Decks[i].Name)
	}
	cardCount := make(map[int64]int, len(names))
	for _, card := range pkg.Cards {
		if names[card.DeckID] == "" {
			continue
		}
		cardCount[card.DeckID]++
	}

	var prefix []string
	prefixSet := false
	for i := range pkg.Decks {
		name := names[pkg.Decks[i].ID]
		if name == "" || cardCount[pkg.Decks[i].ID] == 0 {
			continue
		}
		parts := ankiDeckParts(name)
		if !prefixSet {
			prefix = parts
			prefixSet = true
			continue
		}
		prefix = ankiSharedDeckPrefix(prefix, parts)
	}

	switch {
	case len(prefix) > 0:
		plan.TopName = strings.Join(prefix, "::")
	case prefixSet:
		// The referenced decks branch away from the first component on, so the largest branch
		// names the deck. Iterating pkg.Decks keeps the tie break explicit: strictly more cards,
		// or the same count on a lower deck id.
		var bestID int64
		bestCount := 0
		for i := range pkg.Decks {
			id := pkg.Decks[i].ID
			if names[id] == "" || cardCount[id] == 0 {
				continue
			}
			if cardCount[id] > bestCount || (cardCount[id] == bestCount && id < bestID) {
				bestID = id
				bestCount = cardCount[id]
			}
		}
		plan.TopName = ankiDeckParts(names[bestID])[0]
	default:
		plan.TopName = "Imported deck"
	}

	for i := range pkg.Decks {
		if names[pkg.Decks[i].ID] == plan.TopName {
			// The deck model rejects a description over its length bound or carrying a control
			// character, and real Anki decks routinely carry multi-line HTML blurbs. A cosmetic
			// field must not fail the import of the cards, so an unstorable description is
			// dropped instead of written invalid.
			if len(deckMetaErrors("", pkg.Decks[i].Description)) == 0 {
				plan.Description = pkg.Decks[i].Description
			}
			break
		}
	}

	for i := range pkg.Decks {
		name := names[pkg.Decks[i].ID]
		switch {
		case name == "" || name == plan.TopName:
			plan.Paths[pkg.Decks[i].ID] = ""
		case strings.HasPrefix(name, plan.TopName+"::"):
			plan.Paths[pkg.Decks[i].ID] = strings.TrimPrefix(name, plan.TopName+"::")
		default:
			plan.Paths[pkg.Decks[i].ID] = name
		}
	}
	return plan
}

// ankiMediaFile is one media entry after the import's single hashing pass. The stream is read
// once to produce both digests, the size and the head bytes the unplayable-video check needs;
// every later step works from this record and never holds the payload in memory.
type ankiMediaFile struct {
	entry     *anki.MediaEntry
	sha1hex   string
	sha256hex string
	size      int64
	head      []byte
}

// hashAnkiMedia hashes every media stream exactly once. The clash rename, the quota check and
// the media write all use these results, so the archive payload is never materialised.
func hashAnkiMedia(pkg *anki.Package, limits anki.Limits) ([]ankiMediaFile, error) {
	files := make([]ankiMediaFile, 0, len(pkg.Media))
	total := pkg.UnpackedBytes
	for i := range pkg.Media {
		entry := &pkg.Media[i]
		h1 := sha1.New()
		h256 := sha256.New()
		head := &headBuffer{max: mediatype.HeadBytes}
		size, err := hashMediaEntry(entry, io.MultiWriter(h1, h256, head), limits.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		if entry.Size != 0 && int64(entry.Size) != size {
			return nil, fmt.Errorf("%w: media entry %q declares %d bytes, got %d", anki.ErrCorruptMedia, entry.Name, entry.Size, size)
		}
		sum1 := h1.Sum(nil)
		if len(entry.SHA1) == sha1.Size && !bytes.Equal(entry.SHA1, sum1) {
			return nil, fmt.Errorf("%w: media entry %q does not match its sha1", anki.ErrCorruptMedia, entry.Name)
		}
		total += size
		if total > limits.MaxTotalBytes {
			return nil, fmt.Errorf("%w: archive is over the total cap", anki.ErrTooLarge)
		}
		files = append(files, ankiMediaFile{
			entry:     entry,
			sha1hex:   hex.EncodeToString(sum1),
			sha256hex: hex.EncodeToString(h256.Sum(nil)),
			size:      size,
			head:      head.bytes,
		})
	}
	return files, nil
}

// hashMediaEntry copies one entry's decompressed stream into dst under the per-entry cap.
func hashMediaEntry(entry *anki.MediaEntry, dst io.Writer, max int64) (int64, error) {
	rc, err := entry.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: media entry %q: %v", anki.ErrCorruptMedia, entry.Name, err)
	}
	defer rc.Close()
	n, err := io.Copy(dst, io.LimitReader(rc, max+1))
	if err != nil {
		return n, fmt.Errorf("media entry %q: %w", entry.Name, err)
	}
	if n > max {
		return n, fmt.Errorf("%w: media entry %q decompresses over the per-file cap", anki.ErrTooLarge, entry.Name)
	}
	return n, nil
}

// headBuffer keeps the first max bytes written to it and discards the rest.
type headBuffer struct {
	max   int
	bytes []byte
}

func (h *headBuffer) Write(p []byte) (int, error) {
	if room := h.max - len(h.bytes); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		h.bytes = append(h.bytes, p[:room]...)
	}
	return len(p), nil
}

// ankiRawMediaSizes indexes the bytes each media entry carries by sha256, for the quota check.
// An entry without bytes is a missing file, not an empty file: it contributes no bytes.
func ankiRawMediaSizes(files []ankiMediaFile) map[string]int64 {
	out := make(map[string]int64, len(files))
	for i := range files {
		if files[i].size == 0 {
			continue
		}
		out[files[i].sha256hex] = files[i].size
	}
	return out
}

// ankiMediaNameIndex indexes the package's media names after the clash rename. Only entries with
// bytes are indexed: a name without bytes is reported as missing instead of resolving a reference.
func ankiMediaNameIndex(files []ankiMediaFile) map[string]bool {
	idx := make(map[string]bool, len(files))
	for i := range files {
		if files[i].size == 0 {
			continue
		}
		idx[normaliseMediaName(files[i].entry.Name)] = true
	}
	return idx
}

// ankiMediaRefs returns every media name referenced by note fields, note-type templates, CSS and
// LaTeX bodies, de-duplicated and sorted.
func ankiMediaRefs(pkg *anki.Package) []string {
	seen := map[string]struct{}{}
	add := func(value string) {
		for _, name := range mediaNamesInHTML(value) {
			seen[name] = struct{}{}
		}
	}
	for i := range pkg.Notes {
		for _, value := range pkg.Notes[i].Fields {
			add(value)
		}
	}
	for i := range pkg.NoteTypes {
		nt := &pkg.NoteTypes[i]
		for j := range nt.Templates {
			t := &nt.Templates[j]
			add(t.QFormat)
			add(t.AFormat)
			add(t.BQFormat)
			add(t.BAFormat)
		}
		add(nt.CSS)
		add(nt.LaTeXPre)
		add(nt.LaTeXPost)
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ankiMissingMedia lists every referenced name that the package has no bytes for. A reference
// that already carries a sha256 needs the package to carry that exact content; any other name is
// matched against the package's own media names.
func ankiMissingMedia(pkg *anki.Package, files []ankiMediaFile) []string {
	names := ankiMediaNameIndex(files)
	shas := map[string]bool{}
	for i := range files {
		if files[i].size == 0 {
			continue
		}
		shas[files[i].sha256hex] = true
	}
	var missing []string
	for _, ref := range ankiMediaRefs(pkg) {
		if match := mediaShaNameRE.FindStringSubmatch(ref); match != nil {
			if !shas[match[1]] {
				missing = append(missing, ref)
			}
			continue
		}
		if !names[normaliseMediaName(ref)] {
			missing = append(missing, ref)
		}
	}
	return missing
}

// videoExtensions are the file extensions that name a video. The set is used only to recognise a
// video the byte classifier did not; playability is decided by mp4/webm alone.
var videoExtensions = map[string]bool{
	"mp4": true, "m4v": true, "webm": true, "ogv": true, "mov": true, "avi": true,
	"mkv": true, "wmv": true, "flv": true, "mpeg": true, "mpg": true, "3gp": true,
}

// ankiUnplayableVideo lists video files a browser cannot play: everything except mp4 and webm.
func ankiUnplayableVideo(files []ankiMediaFile) []string {
	var out []string
	for i := range files {
		entry := files[i].entry
		if files[i].size == 0 {
			continue
		}
		mime, class := mediatype.Classify(files[i].head, entry.Name)
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(entry.Name)), ".")
		if class != mediatype.ClassVideo && !videoExtensions[ext] {
			continue
		}
		if mime == "video/mp4" || mime == "video/webm" || ext == "mp4" || ext == "webm" {
			continue
		}
		out = append(out, entry.Name)
	}
	sort.Strings(out)
	return out
}

var (
	// ankiHTMLTagRE matches one opening script or link tag, so its attributes can be inspected.
	ankiHTMLTagRE = regexp.MustCompile(`(?is)<(script|link)\b[^>]*>`)
	// ankiHTMLAttrRE matches one attribute in any of the three quoting forms.
	ankiHTMLAttrRE = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// ankiIsAbsoluteURL reports whether a reference points outside the package.
func ankiIsAbsoluteURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "//")
}

// ankiExternalAssets lists every absolute script src and stylesheet link href in the package's
// note fields, templates and CSS. A relative reference points at package media and is not an
// external asset.
func ankiExternalAssets(pkg *anki.Package) []string {
	seen := map[string]struct{}{}
	add := func(value string) {
		for _, tag := range ankiHTMLTagRE.FindAllString(value, -1) {
			name := strings.ToLower(strings.TrimSpace(tag[1 : 1+strings.IndexAny(tag, " \t\r\n>")]))
			attrs := map[string]string{}
			for _, match := range ankiHTMLAttrRE.FindAllStringSubmatch(tag, -1) {
				attrs[strings.ToLower(match[1])] = match[2] + match[3] + match[4]
			}
			switch name {
			case "script":
				if ankiIsAbsoluteURL(attrs["src"]) {
					seen[strings.TrimSpace(attrs["src"])] = struct{}{}
				}
			case "link":
				rel := strings.ToLower(strings.TrimSpace(attrs["rel"]))
				if strings.Contains(rel, "stylesheet") && ankiIsAbsoluteURL(attrs["href"]) {
					seen[strings.TrimSpace(attrs["href"])] = struct{}{}
				}
			}
		}
	}
	for i := range pkg.Notes {
		for _, value := range pkg.Notes[i].Fields {
			add(value)
		}
	}
	for i := range pkg.NoteTypes {
		nt := &pkg.NoteTypes[i]
		for j := range nt.Templates {
			t := &nt.Templates[j]
			add(t.QFormat)
			add(t.AFormat)
			add(t.BQFormat)
			add(t.BAFormat)
		}
		add(nt.CSS)
	}
	out := make([]string, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// ankiMissingLatexImages lists the LaTeX image names a note needs but the package does not carry.
// Anki rewrites [latex]…[/latex], [$]…[/$] and [$$]…[/$$] to latex-<sha1>.<ext>; the renderer
// computes the same name, so the expected file can be named without rendering.
func ankiMissingLatexImages(pkg *anki.Package, files []ankiMediaFile, types map[int64]anki.NoteType) []string {
	names := ankiMediaNameIndex(files)
	seen := map[string]struct{}{}
	for i := range pkg.Notes {
		note := &pkg.Notes[i]
		nt, ok := types[note.NoteTypeID]
		if !ok {
			continue
		}
		for _, value := range note.Fields {
			for _, name := range mediaNamesInHTML(notetype.RenderLatex(value, nt.LaTeXSVG)) {
				if !strings.HasPrefix(path.Base(name), "latex-") {
					continue
				}
				if names[normaliseMediaName(name)] {
					continue
				}
				seen[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ankiStockKind maps Anki's numeric original-stock-kind enum onto the note type model's names.
// Zero means the note type was not copied from a stock type.
func ankiStockKind(value int) *notetype.OriginalStockKind {
	var kind notetype.OriginalStockKind
	switch value {
	case 1:
		kind = notetype.StockBasic
	case 2:
		kind = notetype.StockBasicAndReversed
	case 3:
		kind = notetype.StockBasicOptionalReversed
	case 4:
		kind = notetype.StockBasicTyping
	case 5:
		kind = notetype.StockCloze
	case 6:
		kind = notetype.StockImageOcclusion
	default:
		return nil
	}
	return &kind
}

// ankiConfigFor maps one package note type onto the note type model. Fields become f0, f1, … and
// templates t0, t1, … in package order; the imported answer spec stays nil, because Anki's
// templates carry no engram answer control.
func ankiConfigFor(nt *anki.NoteType) (notetype.Config, []string, []string, error) {
	var kind notetype.Kind
	switch nt.Kind {
	case anki.KindNormal:
		kind = notetype.KindNormal
	case anki.KindCloze:
		kind = notetype.KindCloze
	default:
		return notetype.Config{}, nil, nil, &PackageError{
			Code:    CodePackageBadFormat,
			Message: "Anki note type has an unknown kind",
			Entries: []string{fmt.Sprintf("note_type %d (%s): kind %d", nt.ID, nt.Name, nt.Kind)},
		}
	}
	if len(nt.Fields) == 0 {
		return notetype.Config{}, nil, nil, &PackageError{
			Code:    CodePackageBadFormat,
			Message: "Anki note type has no fields",
			Entries: []string{fmt.Sprintf("note_type %d (%s)", nt.ID, nt.Name)},
		}
	}

	fields := make([]notetype.Field, len(nt.Fields))
	fieldIDs := make([]string, len(nt.Fields))
	for i, f := range nt.Fields {
		id := fmt.Sprintf("f%d", i)
		fieldIDs[i] = id
		fields[i] = notetype.Field{
			ID:                id,
			Name:              f.Name,
			Sticky:            f.Sticky,
			RTL:               f.RTL,
			Font:              f.Font,
			FontSize:          f.FontSize,
			Description:       f.Description,
			PlainText:         f.PlainText,
			Collapsed:         f.Collapsed,
			ExcludeFromSearch: f.ExcludeFromSearch,
			Tag:               f.Tag,
			PreventDeletion:   f.PreventDeletion,
		}
	}

	templates := make([]notetype.Template, len(nt.Templates))
	templateIDs := make([]string, len(nt.Templates))
	for i, t := range nt.Templates {
		id := fmt.Sprintf("t%d", i)
		templateIDs[i] = id
		templates[i] = notetype.Template{
			ID:              id,
			Name:            t.Name,
			QFormat:         t.QFormat,
			AFormat:         t.AFormat,
			BQFormat:        t.BQFormat,
			BAFormat:        t.BAFormat,
			BrowserFont:     t.BrowserFont,
			BrowserFontSize: t.BrowserFontSize,
		}
	}

	sortFieldID := fieldIDs[0]
	if nt.SortFieldIndex >= 0 && nt.SortFieldIndex < len(fieldIDs) {
		sortFieldID = fieldIDs[nt.SortFieldIndex]
	}

	return notetype.Config{
		Kind:              kind,
		Fields:            fields,
		Templates:         templates,
		CSS:               nt.CSS,
		SortFieldID:       sortFieldID,
		LaTeXPre:          nt.LaTeXPre,
		LaTeXPost:         nt.LaTeXPost,
		LaTeXSVG:          nt.LaTeXSVG,
		OriginalStockKind: ankiStockKind(nt.OriginalStockKind),
		// The Anki notetype id is the stable identity a re-import matches on; it is not the
		// engram row id. The reader exposes no note type mtime, so MTime stays 0.
		AnkiOrigin: &notetype.AnkiOrigin{NoteTypeID: nt.OriginalID},
	}, fieldIDs, templateIDs, nil
}

// ankiTypeTarget is one resolved package note type: the row it writes to and the ids the notes
// and cards refer to.
type ankiTypeTarget struct {
	ID          uint64
	Name        string
	Config      notetype.Config
	FieldIDs    []string
	TemplateIDs []string
}

// sameAnkiStructure compares the parts a re-import may preserve: the field names in order and the
// template names in order, with their counts.
func sameAnkiStructure(a, b notetype.Config) bool {
	if len(a.Fields) != len(b.Fields) || len(a.Templates) != len(b.Templates) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i].Name != b.Fields[i].Name {
			return false
		}
	}
	for i := range a.Templates {
		if a.Templates[i].Name != b.Templates[i].Name {
			return false
		}
	}
	return true
}

// copyAnkiIDs keeps an existing note type's field and template ids when its structure is equal, so
// the overwrite cannot detach a stored note's field map or a card's template value.
func copyAnkiIDs(dst *notetype.Config, src notetype.Config) {
	for i := range dst.Fields {
		if i < len(src.Fields) {
			dst.Fields[i].ID = src.Fields[i].ID
		}
	}
	for i := range dst.Templates {
		if i < len(src.Templates) {
			dst.Templates[i].ID = src.Templates[i].ID
		}
	}
}

// resolveAnkiNoteTypes creates or reuses the note types of the target deck.
//
// A note type is matched by anki_origin.notetype_id. When the stored and imported field and
// template structure is equal, the stored row is overwritten in place (templates, CSS and the
// other config values) and keeps its id. Otherwise a new note type is created; the matched notes
// are moved onto it when their content is written, and their cards' template ids are remapped by
// ordinal.
func resolveAnkiNoteTypes(ctx context.Context, tx *gorm.DB, deckID uint64, pkg *anki.Package) (map[int64]*ankiTypeTarget, error) {
	existing, err := NewNoteTypeStore(tx).ListByDeck(ctx, deckID)
	if err != nil {
		return nil, err
	}
	byOrigin := map[int64]NoteType{}
	for i := range existing {
		cfg, err := existing[i].Config()
		if err != nil || cfg.AnkiOrigin == nil || cfg.AnkiOrigin.NoteTypeID == 0 {
			continue
		}
		// If a structural change already forced a new note type, several rows share one Anki
		// origin. The highest id is the newest row and the one the notes were moved to, so a
		// second re-import updates it instead of creating yet another type.
		if prev, ok := byOrigin[cfg.AnkiOrigin.NoteTypeID]; !ok || existing[i].ID > prev.ID {
			byOrigin[cfg.AnkiOrigin.NoteTypeID] = existing[i]
		}
	}

	now := time.Now().UTC()
	out := make(map[int64]*ankiTypeTarget, len(pkg.NoteTypes))
	usedRow := map[uint64]bool{}
	for i := range pkg.NoteTypes {
		nt := &pkg.NoteTypes[i]
		cfg, fieldIDs, templateIDs, err := ankiConfigFor(nt)
		if err != nil {
			return nil, err
		}
		target := &ankiTypeTarget{Name: nt.Name, Config: cfg, FieldIDs: fieldIDs, TemplateIDs: templateIDs}
		row, matched := byOrigin[nt.OriginalID]
		if matched && !usedRow[row.ID] {
			prev, cerr := row.Config()
			if cerr == nil && sameAnkiStructure(prev, cfg) {
				copyAnkiIDs(&cfg, prev)
				// The overwrite runs the same rule a created note type runs: SetConfig is the one
				// validation point for a note type config, so a re-import cannot store a config a
				// REST write would reject.
				validated := NoteType{ID: row.ID}
				if err := validated.SetConfig(nt.Name, cfg); err != nil {
					return nil, &PackageError{
						Code:    CodePackageBadFormat,
						Message: "Anki note type is invalid",
						Entries: []string{fmt.Sprintf("note_type %d (%s): %v", nt.ID, nt.Name, err)},
					}
				}
				target.Config = cfg
				target.FieldIDs = fieldIDsFrom(cfg)
				target.TemplateIDs = templateIDsFrom(cfg)
				if err := tx.WithContext(ctx).Model(&NoteType{}).Where("id = ?", row.ID).Updates(map[string]any{
					"name":        validated.Name,
					"config_json": validated.ConfigJSON,
					"updated_at":  now,
					"version":     gorm.Expr("version + 1"),
				}).Error; err != nil {
					return nil, fmt.Errorf("update Anki note type %q: %w", nt.Name, err)
				}
				if err := NewMediaNoteTypeStore(tx).Replace(ctx, row.ID, noteTypeMediaNames(target.Config)); err != nil {
					return nil, fmt.Errorf("record media of note type %q: %w", nt.Name, err)
				}
				target.ID = row.ID
				usedRow[row.ID] = true
				out[nt.ID] = target
				continue
			}
		}

		created := NoteType{DeckID: deckID, CreatedAt: now, UpdatedAt: now}
		if err := created.SetConfig(nt.Name, target.Config); err != nil {
			return nil, &PackageError{
				Code:    CodePackageBadFormat,
				Message: "Anki note type is invalid",
				Entries: []string{fmt.Sprintf("note_type %d (%s): %v", nt.ID, nt.Name, err)},
			}
		}
		if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
			return nil, fmt.Errorf("create Anki note type %q: %w", nt.Name, err)
		}
		if err := NewMediaNoteTypeStore(tx).Replace(ctx, created.ID, noteTypeMediaNames(target.Config)); err != nil {
			return nil, fmt.Errorf("record media of note type %q: %w", nt.Name, err)
		}
		target.ID = created.ID
		usedRow[created.ID] = true
		out[nt.ID] = target
	}
	return out, nil
}

// fieldIDsFrom returns the config's field ids in order.
func fieldIDsFrom(cfg notetype.Config) []string {
	ids := make([]string, len(cfg.Fields))
	for i := range cfg.Fields {
		ids[i] = cfg.Fields[i].ID
	}
	return ids
}

// templateIDsFrom returns the config's template ids in order.
func templateIDsFrom(cfg notetype.Config) []string {
	ids := make([]string, len(cfg.Templates))
	for i := range cfg.Templates {
		ids[i] = cfg.Templates[i].ID
	}
	return ids
}

// ankiCardTemplate returns the stored cards.template value for one package card ordinal.
//
// A normal card's template is the imported template id at that ordinal. A cloze card's identity
// is "cloze:<number>", which is the convention the note write path uses and therefore what every
// reader (queue, editor, renderer) expects from an imported cloze card.
func ankiCardTemplate(target *ankiTypeTarget, ordinal int) (string, error) {
	if target.Config.Kind == notetype.KindCloze {
		if len(target.Config.Templates) == 0 {
			return "", errors.New("cloze note type has no template")
		}
		return fmt.Sprintf("cloze:%d", ordinal+1), nil
	}
	if ordinal < 0 || ordinal >= len(target.TemplateIDs) {
		return "", fmt.Errorf("card ordinal %d has no template", ordinal)
	}
	return target.TemplateIDs[ordinal], nil
}

// ankiValues maps a package note's field values (in package field order) onto the target note
// type's field ids. A short row is padded with empty values; a long row's extra values are
// ignored, because the note type defines the stored shape.
func ankiValues(note *anki.Note, target *ankiTypeTarget) map[string]string {
	values := make(map[string]string, len(target.FieldIDs))
	for i, id := range target.FieldIDs {
		if i < len(note.Fields) {
			values[id] = note.Fields[i]
		} else {
			values[id] = ""
		}
	}
	return values
}

// ankiNoteRef is the external reference a note carries: the Anki guid, namespaced so it cannot
// collide with another import's reference.
func ankiNoteRef(guid string) string { return "anki:" + guid }

// importAnkiPackage imports one decoded .apkg package. Everything the import writes happens in one
// transaction; under dry_run the transaction rolls back and no media file is written.
func (s *DeckStore) importAnkiPackage(ctx context.Context, actorUserID uint64, r io.ReaderAt, size int64, limits PackageImportLimits, opts PackageImportOptions, targetKind string, targetDeckID uint64) (*PackageImportReport, error) {
	ankiLimits := limits.ankiLimits()
	pkg, err := anki.ReadWithLimits(r, size, ankiLimits)
	if err != nil {
		return nil, mapAnkiPackageError(err)
	}
	files, err := hashAnkiMedia(pkg, ankiLimits)
	if err != nil {
		return nil, mapAnkiPackageError(err)
	}
	plan := planAnkiDecks(pkg)

	// Quota is checked before the transaction and before any disk write, exactly like the .edeck
	// path, so an over-quota package leaves no deck, row or file behind.
	if err := checkMediaQuota(ctx, s.db, actorUserID, ankiRawMediaSizes(files), opts.MediaQuotaBytes); err != nil {
		return nil, err
	}

	report := &PackageImportReport{
		Target:    targetKind,
		DryRun:    opts.DryRun,
		MatchRule: "anki_guid",
		Errors:    []PackageImportError{},
	}
	var writtenMedia []mediaWrite
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.importAnkiInTx(ctx, tx, actorUserID, pkg, files, &plan, targetKind, targetDeckID, opts, report, &writtenMedia)
	})
	if err != nil {
		s.cleanupMediaWrites(ctx, writtenMedia)
	}
	if err != nil && !errors.Is(err, ErrPackageDryRun) {
		return nil, err
	}
	return report, nil
}

// dropUnusedAnkiNoteTypes removes the package's note types that no note uses.
//
// Anki's importer creates only the note types its imported notes refer to; a package that carries
// an extra note type changes nothing for it. The fixture
// internal/notetype/testdata/anki/notetype-import.anki21b.apkg holds such a package, and its golden
// records the names Anki 26.09.3 added when it imported the package into an empty collection.
func dropUnusedAnkiNoteTypes(pkg *anki.Package) {
	if len(pkg.NoteTypes) == 0 {
		return
	}
	used := make(map[int64]bool, len(pkg.Notes))
	for i := range pkg.Notes {
		used[pkg.Notes[i].NoteTypeID] = true
	}
	kept := pkg.NoteTypes[:0]
	for i := range pkg.NoteTypes {
		if used[pkg.NoteTypes[i].ID] {
			kept = append(kept, pkg.NoteTypes[i])
		}
	}
	pkg.NoteTypes = kept
}

// importAnkiInTx performs the Anki mapping inside one transaction.
func (s *DeckStore) importAnkiInTx(ctx context.Context, tx *gorm.DB, actorUserID uint64, pkg *anki.Package, files []ankiMediaFile, plan *ankiDeckPlan, targetKind string, targetDeckID uint64, opts PackageImportOptions, report *PackageImportReport, writtenMedia *[]mediaWrite) error {
	// The unused note types leave no trace in the target: they are not created, they contribute no
	// media references to the report, and no stored row points at them.
	dropUnusedAnkiNoteTypes(pkg)

	deckID, err := s.resolveAnkiTargetDeck(ctx, tx, actorUserID, plan, targetKind, targetDeckID, opts)
	if err != nil {
		return err
	}
	report.DeckID = deckID
	pid, err := deckPublicID(ctx, tx, deckID)
	if err != nil {
		return err
	}
	report.DeckPublicID = pid

	if targetKind == "replace_deck" {
		if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Delete(&Note{}).Error; err != nil {
			return fmt.Errorf("replace deck: clear notes: %w", err)
		}
	}

	// Anki's clash rule applies to .apkg media as well: a name whose bytes differ from the target
	// deck's existing content is renamed with a SHA-1 suffix, and this package's references are
	// rewritten before notes are written.
	if opts.MediaRoot != "" {
		if err := renameAnkiMedia(ctx, tx, deckID, pkg, files); err != nil {
			return err
		}
	}

	// The reader exposes mtimes nowhere, so the note type index is keyed by the package's own
	// note type ids (the ids notes reference), and the config carries the Anki id separately.
	types := make(map[int64]anki.NoteType, len(pkg.NoteTypes))
	for i := range pkg.NoteTypes {
		types[pkg.NoteTypes[i].ID] = pkg.NoteTypes[i]
	}
	report.MissingMedia = ankiMissingMedia(pkg, files)
	report.MediaMissing = len(report.MissingMedia)
	report.UnplayableVideo = ankiUnplayableVideo(files)
	report.ExternalAssets = ankiExternalAssets(pkg)
	report.LatexMissingImages = ankiMissingLatexImages(pkg, files, types)

	targets, err := resolveAnkiNoteTypes(ctx, tx, deckID, pkg)
	if err != nil {
		return err
	}

	// Package-provided bytes are attributed to the importer before the note writes, so the
	// pre-write readability check sees them as supplied by the same user. dry_run writes no row.
	if !opts.DryRun && opts.MediaRoot != "" {
		for sha := range ankiRawMediaSizes(files) {
			if err := RecordMediaUploader(ctx, tx, sha, actorUserID); err != nil {
				return fmt.Errorf("import Anki package: record media uploader: %w", err)
			}
		}
	}
	if opts.MediaRoot != "" {
		if err := registerAnkiMediaNames(ctx, tx, deckID, files); err != nil {
			return err
		}
	}

	// Existing notes are matched by external_ref only. A content fingerprint match would attach
	// the import to a note the user wrote themselves, which re-using an Anki guid must not do.
	// The lookup includes soft-deleted rows: the unique index on (deck_id, external_ref) covers
	// them too, so a deleted note carrying the same guid must be restored instead of colliding
	// with a new row. replace_deck reaches this state by soft-deleting every note of the deck
	// before the package is written.
	var existing []Note
	if err := tx.WithContext(ctx).Unscoped().Where("deck_id = ?", deckID).Find(&existing).Error; err != nil {
		return fmt.Errorf("load existing notes: %w", err)
	}
	byRef := make(map[string]*Note, len(existing))
	for i := range existing {
		if existing[i].ExternalRef != nil && *existing[i].ExternalRef != "" {
			byRef[*existing[i].ExternalRef] = &existing[i]
		}
	}

	noteIDs := make(map[int64]uint64, len(pkg.Notes))
	// imported marks notes this pass actually wrote (create or update). A skipped or failed note
	// is not touched, including its card set.
	imported := make(map[int64]bool, len(pkg.Notes))
	now := time.Now().UTC()
	for i := range pkg.Notes {
		note := &pkg.Notes[i]
		target, ok := targets[note.NoteTypeID]
		if !ok {
			report.Errors = append(report.Errors, PackageImportError{
				Entry:  fmt.Sprintf("notes[%d]", i),
				Reason: fmt.Sprintf("unknown note type %d", note.NoteTypeID),
			})
			continue
		}
		ref := ankiNoteRef(note.GUID)
		old := byRef[ref]
		if old != nil {
			switch opts.OnConflict {
			case "skip":
				report.NotesSkipped++
				noteIDs[note.ID] = old.ID
				continue
			case "fail":
				report.Errors = append(report.Errors, PackageImportError{Entry: fmt.Sprintf("notes[%d]", i), Reason: "a note with this Anki guid already exists"})
				noteIDs[note.ID] = old.ID
				continue
			}
		}
		id, created, err := writeAnkiNote(ctx, tx, actorUserID, deckID, note, target, old, now)
		if err != nil {
			return fmt.Errorf("import Anki note %d: %w", i, asPackageMediaForbidden(err))
		}
		noteIDs[note.ID] = id
		imported[note.ID] = true
		if created {
			report.NotesCreated++
		} else {
			report.NotesUpdated++
		}
	}

	// Cards are the package's cards, not a regeneration: every entry of the package becomes (or
	// updates) exactly one row, and an existing row keeps its id and progress.
	//
	// The note index is built once, before the card loop: resolving a card through a scan of
	// pkg.Notes costs cards × notes, which a package of tens of thousands of notes and cards
	// turns into billions of comparisons. When two notes share an id, the earlier one wins, so
	// the lookup resolves the same note a scan from the start of the slice would have found.
	notesByID := make(map[int64]*anki.Note, len(pkg.Notes))
	for i := range pkg.Notes {
		if _, ok := notesByID[pkg.Notes[i].ID]; ok {
			continue
		}
		notesByID[pkg.Notes[i].ID] = &pkg.Notes[i]
	}
	existingCards := map[uint64]map[int]Card{}
	for _, card := range pkg.Cards {
		noteID, ok := noteIDs[card.NoteID]
		if !ok {
			report.Errors = append(report.Errors, PackageImportError{
				Entry:  fmt.Sprintf("cards[%d]", card.ID),
				Reason: fmt.Sprintf("unknown note %d", card.NoteID),
			})
			continue
		}
		if !imported[card.NoteID] {
			continue
		}
		// Resolve the note's own target through the package note it belongs to.
		packageNote := notesByID[card.NoteID]
		if packageNote == nil {
			report.Errors = append(report.Errors, PackageImportError{
				Entry:  fmt.Sprintf("cards[%d]", card.ID),
				Reason: fmt.Sprintf("unknown note %d", card.NoteID),
			})
			continue
		}
		noteTarget, ok := targets[packageNote.NoteTypeID]
		if !ok {
			report.Errors = append(report.Errors, PackageImportError{
				Entry:  fmt.Sprintf("cards[%d]", card.ID),
				Reason: fmt.Sprintf("unknown note type %d", packageNote.NoteTypeID),
			})
			continue
		}
		template, err := ankiCardTemplate(noteTarget, card.Ordinal)
		if err != nil {
			report.Errors = append(report.Errors, PackageImportError{
				Entry:  fmt.Sprintf("cards[%d]", card.ID),
				Reason: err.Error(),
			})
			continue
		}
		deckPath := plan.Paths[card.DeckID]
		var pathPtr *string
		if deckPath != "" {
			p := deckPath
			pathPtr = &p
		}
		if _, ok := existingCards[noteID]; !ok {
			var rows []Card
			if err := tx.WithContext(ctx).Unscoped().Where("note_id = ?", noteID).Find(&rows).Error; err != nil {
				return fmt.Errorf("load cards of note %d: %w", noteID, err)
			}
			m := make(map[int]Card, len(rows))
			for _, row := range rows {
				m[row.Ordinal] = row
			}
			existingCards[noteID] = m
		}
		row, ok := existingCards[noteID][card.Ordinal]
		if ok {
			updates := map[string]any{"template": template, "deck_path": pathPtr}
			if row.DeletedAt.Valid {
				updates["deleted_at"] = nil
			}
			if row.Template != template || !sameStringPtr(row.DeckPath, pathPtr) || row.DeletedAt.Valid {
				if err := tx.WithContext(ctx).Unscoped().Model(&Card{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return fmt.Errorf("update card %d: %w", row.ID, err)
				}
			}
			continue
		}
		created := Card{NoteID: noteID, Template: template, Ordinal: card.Ordinal, DeckPath: pathPtr, CreatedAt: now}
		if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
			return fmt.Errorf("create card for note %d: %w", noteID, err)
		}
		existingCards[noteID][card.Ordinal] = created
		report.CardsCreated++
	}

	// A note that moved to another note type must not keep a card the new type cannot render.
	// Cards are remapped by ordinal, so a stored card whose ordinal the package no longer carries
	// keeps the old type's template id; that id does not exist in the new type, and rendering or
	// rating such a card fails. The removal runs after the card loop, so a card the package remapped
	// already carries a declared template id and stays. It follows the note type's declared
	// templates, the same rule a note type update applies: a normal type removes exactly the cards
	// whose template id it no longer declares, and a cloze type removes none because its cards are
	// named after the cloze numbers found in the note's fields. A removed card's card_states and
	// reviews rows stay, and a later re-import that declares the template again restores the same
	// row through the reuse-by-ordinal branch above.
	for i := range pkg.Notes {
		note := &pkg.Notes[i]
		if !imported[note.ID] {
			continue
		}
		old := byRef[ankiNoteRef(note.GUID)]
		if old == nil || old.NoteTypeID == nil {
			continue
		}
		target, ok := targets[note.NoteTypeID]
		if !ok || *old.NoteTypeID == target.ID {
			continue
		}
		removed, err := removeStaleCardsForNoteType(ctx, tx, noteIDs[note.ID], target.Config, now)
		if err != nil {
			return err
		}
		report.CardsRemoved += removed
	}

	// Media bytes last, on the transaction handle so metadata and content are atomic. The entry is
	// streamed through the hashing pass's digest into a temporary file and renamed into place, so
	// a media payload is never held in memory. dry_run only counts; it neither writes a file nor a
	// row.
	for i := range files {
		file := &files[i]
		if opts.MediaRoot == "" || file.size == 0 {
			continue
		}
		mstore := NewMediaStore(tx)
		stored, err := mstore.BySha256(ctx, file.sha256hex)
		if err != nil {
			return fmt.Errorf("import Anki package: check media %s: %w", file.sha256hex, err)
		}
		if stored != nil {
			if opts.DryRun {
				continue
			}
			if _, _, err := mstore.reuse(ctx, stored, Ptr(actorUserID)); err != nil {
				return err
			}
			continue
		}
		report.MediaNew++
		if opts.DryRun {
			continue
		}
		rc, err := file.entry.Open()
		if err != nil {
			return fmt.Errorf("import Anki package: open media %q: %w", file.entry.Name, err)
		}
		mime, _ := mediatype.Classify(file.head, file.entry.Name)
		row, abs, err := mstore.SaveStreamTracked(ctx, opts.MediaRoot, mime, rc, Ptr(actorUserID))
		rc.Close()
		if abs != "" {
			*writtenMedia = append(*writtenMedia, mediaWrite{sha: file.sha256hex, abs: abs})
		}
		if err != nil {
			return err
		}
		if row.Sha256 != file.sha256hex {
			return fmt.Errorf("import Anki package: media %q streamed under an unexpected digest", file.entry.Name)
		}
	}

	if opts.OnConflict == "fail" && len(report.Errors) > 0 {
		return &PackageError{Code: CodePackageBadFormat, Message: "import aborted: one or more entries conflict or are invalid", Entries: errorEntries(report.Errors)}
	}
	if opts.DryRun {
		return ErrPackageDryRun
	}
	return nil
}

// removeStaleCardsForNoteType soft-deletes the live cards of a note that a re-import moved to a note
// type whose config no longer declares their template, and returns how many it removed.
//
// The rule is noteTypeCardKeepRule: a card survives when the config still declares its template id,
// so a normal type removes only the cards of a template it dropped and a cloze type removes none.
// Only live cards are loaded, so an already soft-deleted card is not counted twice; a card removed
// here keeps its id, and with it its card_states and reviews rows.
func removeStaleCardsForNoteType(ctx context.Context, tx *gorm.DB, noteID uint64, cfg notetype.Config, now time.Time) (int, error) {
	keep := noteTypeCardKeepRule(cfg)
	var cards []Card
	if err := tx.WithContext(ctx).Where("note_id = ?", noteID).Order("id ASC").Find(&cards).Error; err != nil {
		return 0, fmt.Errorf("load cards of note %d: %w", noteID, err)
	}
	var stale []uint64
	for i := range cards {
		if !keep(cards[i].Template) {
			stale = append(stale, cards[i].ID)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if err := tx.WithContext(ctx).Model(&Card{}).Where("id IN ?", stale).Update("deleted_at", now).Error; err != nil {
		return 0, fmt.Errorf("soft-delete stale cards of note %d: %w", noteID, err)
	}
	return len(stale), nil
}

// writeAnkiNote creates or updates one note and returns its id. The package's fields are the whole
// content: an update replaces them, so a re-import reproduces the package exactly.
func writeAnkiNote(ctx context.Context, tx *gorm.DB, actorUserID, deckID uint64, note *anki.Note, target *ankiTypeTarget, old *Note, now time.Time) (uint64, bool, error) {
	values := ankiValues(note, target)
	if err := normaliseNoteMediaNames(ctx, tx, deckID, values); err != nil {
		return 0, false, fmt.Errorf("normalise note media names: %w", err)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return 0, false, fmt.Errorf("encode note fields: %w", err)
	}
	search := noteSearchText(target.Config, values)
	tags := TagsJSON(note.Tags)
	noteTypeID := target.ID

	if old == nil {
		ref := ankiNoteRef(note.GUID)
		created := Note{
			DeckID:      deckID,
			NoteTypeID:  Ptr(noteTypeID),
			FieldsJSON:  string(raw),
			TagsJSON:    tags,
			SearchText:  search,
			ExternalRef: Ptr(ref),
			Source:      Ptr("import"),
			CreatedBy:   Ptr(actorUserID),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := checkNewMediaRefs(ctx, tx, actorUserID, deckID, nil, values); err != nil {
			return 0, false, err
		}
		if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
			return 0, false, fmt.Errorf("create note: %w", err)
		}
		if err := rebuildMediaNotes(ctx, tx, created.ID, deckID, values); err != nil {
			return 0, false, err
		}
		return created.ID, true, nil
	}

	oldValues, perr := ParseFieldValues(old.FieldsJSON)
	if perr != nil {
		oldValues = nil
	}
	if err := checkNewMediaRefs(ctx, tx, actorUserID, deckID, oldValues, values); err != nil {
		return 0, false, err
	}
	// A soft-deleted match is restored first: it keeps its id, so its cards and their card_states
	// stay attached, and a default-scoped update would otherwise touch no row.
	if old.DeletedAt.Valid {
		if err := NewNoteStore(tx).RestoreInTx(ctx, tx, old.ID); err != nil {
			return 0, false, err
		}
	}
	updates := map[string]any{
		"note_type_id": noteTypeID,
		"fields_json":  string(raw),
		"search_text":  search,
		"tags_json":    tags,
		"updated_at":   now,
	}
	if err := tx.WithContext(ctx).Model(&Note{}).Where("id = ?", old.ID).Updates(updates).Error; err != nil {
		return 0, false, fmt.Errorf("update note %d: %w", old.ID, err)
	}
	if err := rebuildMediaNotes(ctx, tx, old.ID, deckID, values); err != nil {
		return 0, false, err
	}
	return old.ID, false, nil
}

// resolveAnkiTargetDeck resolves new_deck, into_deck and replace_deck for an Anki import. A new
// deck gets its own preset built from the documented defaults, because an .apkg carries no preset.
func (s *DeckStore) resolveAnkiTargetDeck(ctx context.Context, tx *gorm.DB, actorUserID uint64, plan *ankiDeckPlan, targetKind string, targetDeckID uint64, opts PackageImportOptions) (uint64, error) {
	switch targetKind {
	case PackageTargetNewDeck:
		name := strings.TrimSpace(opts.NewDeckName)
		if name == "" {
			name = plan.TopName
		}
		if name == "" {
			name = "Imported deck"
		}
		if err := packageDeckMetaError(deckMetaErrors(name, plan.Description)); err != nil {
			return 0, err
		}
		unique, err := uniqueDeckName(ctx, tx, actorUserID, name)
		if err != nil {
			return 0, err
		}
		presetID, err := createAnkiPreset(ctx, tx, actorUserID, unique)
		if err != nil {
			return 0, err
		}
		deck := Deck{
			OwnerUserID: actorUserID,
			Name:        unique,
			Description: plan.Description,
			PresetID:    presetID,
			CreatedAt:   opts.Now().UTC(),
		}
		if err := tx.WithContext(ctx).Create(&deck).Error; err != nil {
			return 0, fmt.Errorf("import Anki package: create deck: %w", err)
		}
		return deck.ID, nil
	case "into_deck", "replace_deck":
		var deck Deck
		if err := tx.WithContext(ctx).First(&deck, "id = ?", targetDeckID).Error; err != nil {
			return 0, &PackageError{Code: CodePackageBadFormat, Message: "target deck not found"}
		}
		return deck.ID, nil
	default:
		return 0, &PackageError{Code: CodePackageBadFormat, Message: "unknown target"}
	}
}

// createAnkiPreset creates the imported deck's preset with the documented defaults. The name is
// independent of the user's default preset, so deleting this deck's preset cannot remove the
// account default and vice versa.
func createAnkiPreset(ctx context.Context, tx *gorm.DB, owner uint64, deckName string) (uint64, error) {
	base := "Imported — " + deckName
	name, err := uniqueOwnerName(ctx, tx, &Preset{}, owner, base)
	if err != nil {
		return 0, err
	}
	preset := NewPreset(owner, name)
	if err := NewPresetStore(tx).Create(ctx, &preset); err != nil {
		return 0, err
	}
	return preset.ID, nil
}

// registerAnkiMediaNames records every media name the package provides for the target deck.
func registerAnkiMediaNames(ctx context.Context, tx *gorm.DB, deckID uint64, files []ankiMediaFile) error {
	names := NewMediaNameStore(tx)
	for i := range files {
		if files[i].size == 0 {
			continue
		}
		name := normaliseMediaName(files[i].entry.Name)
		if name == "" {
			continue
		}
		if err := names.Upsert(ctx, deckID, name, files[i].sha256hex); err != nil {
			return err
		}
	}
	return nil
}

// renameAnkiMedia implements Anki's clash rule for an .apkg import and rewrites this package's
// references. It mirrors the .edeck rule but works on the reader's model, because that is what
// the mapping later consumes. The digests come from the single hashing pass.
func renameAnkiMedia(ctx context.Context, tx *gorm.DB, deckID uint64, pkg *anki.Package, files []ankiMediaFile) error {
	rows, err := NewMediaNameStore(tx).ListByDeck(ctx, deckID)
	if err != nil {
		return fmt.Errorf("import Anki package: list existing media names: %w", err)
	}
	existing := make(map[string]string, len(rows))
	for _, row := range rows {
		existing[row.Name] = row.Sha256
	}
	renames := map[string]string{}
	for i := range files {
		file := &files[i]
		if file.size == 0 {
			continue
		}
		oldName := normaliseMediaName(file.entry.Name)
		newName := addDataToFolderUniquely(existing, oldName, file.sha256hex, file.sha1hex)
		if newName != oldName {
			renames[oldName] = newName
		}
		file.entry.Name = newName
		existing[newName] = file.sha256hex
	}
	if len(renames) == 0 {
		return nil
	}
	for i := range pkg.Notes {
		for j := range pkg.Notes[i].Fields {
			pkg.Notes[i].Fields[j] = rewriteMediaRefs(pkg.Notes[i].Fields[j], renames)
		}
	}
	for i := range pkg.NoteTypes {
		nt := &pkg.NoteTypes[i]
		nt.CSS = rewriteMediaRefs(nt.CSS, renames)
		nt.LaTeXPre = rewriteMediaRefs(nt.LaTeXPre, renames)
		nt.LaTeXPost = rewriteMediaRefs(nt.LaTeXPost, renames)
		for j := range nt.Templates {
			t := &nt.Templates[j]
			t.QFormat = rewriteMediaRefs(t.QFormat, renames)
			t.AFormat = rewriteMediaRefs(t.AFormat, renames)
			t.BQFormat = rewriteMediaRefs(t.BQFormat, renames)
			t.BAFormat = rewriteMediaRefs(t.BAFormat, renames)
		}
	}
	return nil
}

// sameStringPtr compares two optional strings.
func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

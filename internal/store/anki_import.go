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
// in-memory model. This file maps that model onto the data model. It lives in internal/store,
// not internal/anki, because the writes need the store's unexported transaction, media, quota and
// note-type helpers; importing internal/store from internal/anki would create a cycle and force
// those helpers to become public API.

// isAnkiArchive reports whether raw is an Anki package. The decision reads the zip entry names:
// every .apkg layout carries a collection entry and/or a "meta" entry, while an .edeck carries
// manifest.json and note_types.json instead. The file name is never consulted.
func isAnkiArchive(raw []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
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

// planAnkiDecks chooses the top-level deck and computes every subdeck path. The top deck is the
// shortest normalised name (ties broken lexicographically, so the choice is deterministic). A
// package deck under the top deck loses the "Top::" prefix; a deck outside that tree keeps its
// own name as the path.
func planAnkiDecks(pkg *anki.Package) ankiDeckPlan {
	plan := ankiDeckPlan{Paths: map[int64]string{}}
	for i := range pkg.Decks {
		name := ankiDeckName(pkg.Decks[i].Name)
		if name == "" {
			continue
		}
		if plan.TopName == "" || len(name) < len(plan.TopName) ||
			(len(name) == len(plan.TopName) && name < plan.TopName) {
			plan.TopName = name
			plan.Description = pkg.Decks[i].Description
		}
	}
	if plan.TopName == "" {
		plan.TopName = "Imported deck"
	}
	for i := range pkg.Decks {
		name := ankiDeckName(pkg.Decks[i].Name)
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

// ankiMediaDigest returns the two digests the import needs for one media entry: sha1 for Anki's
// clash-rename suffix and sha256 for the content-addressed store.
func ankiMediaDigest(entry anki.MediaEntry) (sha1hex, sha256hex string) {
	sum1 := sha1.Sum(entry.Data)
	sum256 := sha256.Sum256(entry.Data)
	return hex.EncodeToString(sum1[:]), hex.EncodeToString(sum256[:])
}

// ankiRawMedia indexes the media bytes the package carries by sha256, for the quota check. An
// entry without bytes is a missing file, not an empty file: it contributes no bytes.
func ankiRawMedia(pkg *anki.Package) map[string][]byte {
	out := make(map[string][]byte, len(pkg.Media))
	for _, entry := range pkg.Media {
		if len(entry.Data) == 0 {
			continue
		}
		_, sha := ankiMediaDigest(entry)
		out[sha] = entry.Data
	}
	return out
}

// ankiMediaNameIndex indexes the package's media names after the clash rename. Only entries with
// bytes are indexed: a name without bytes is reported as missing instead of resolving a reference.
func ankiMediaNameIndex(pkg *anki.Package) map[string]bool {
	idx := make(map[string]bool, len(pkg.Media))
	for _, entry := range pkg.Media {
		if len(entry.Data) == 0 {
			continue
		}
		idx[normaliseMediaName(entry.Name)] = true
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
func ankiMissingMedia(pkg *anki.Package) []string {
	names := ankiMediaNameIndex(pkg)
	shas := map[string]bool{}
	for _, entry := range pkg.Media {
		if len(entry.Data) == 0 {
			continue
		}
		_, sha := ankiMediaDigest(entry)
		shas[sha] = true
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
func ankiUnplayableVideo(pkg *anki.Package) []string {
	var out []string
	for _, entry := range pkg.Media {
		if len(entry.Data) == 0 {
			continue
		}
		mime, class := mediatype.Classify(headBytes(entry.Data), entry.Name)
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
func ankiMissingLatexImages(pkg *anki.Package, types map[int64]anki.NoteType) []string {
	names := ankiMediaNameIndex(pkg)
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
func (s *DeckStore) importAnkiPackage(ctx context.Context, actorUserID uint64, raw []byte, opts PackageImportOptions, targetKind string, targetDeckID uint64) (*PackageImportReport, error) {
	pkg, err := anki.Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, mapAnkiPackageError(err)
	}
	plan := planAnkiDecks(pkg)

	// Quota is checked before the transaction and before any disk write, exactly like the .edeck
	// path, so an over-quota package leaves no deck, row or file behind.
	if err := checkMediaQuota(ctx, s.db, actorUserID, ankiRawMedia(pkg), opts.MediaQuotaBytes); err != nil {
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
		return s.importAnkiInTx(ctx, tx, actorUserID, pkg, &plan, targetKind, targetDeckID, opts, report, &writtenMedia)
	})
	if err != nil {
		s.cleanupMediaWrites(ctx, writtenMedia)
	}
	if err != nil && !errors.Is(err, ErrPackageDryRun) {
		return nil, err
	}
	return report, nil
}

// importAnkiInTx performs the Anki mapping inside one transaction.
func (s *DeckStore) importAnkiInTx(ctx context.Context, tx *gorm.DB, actorUserID uint64, pkg *anki.Package, plan *ankiDeckPlan, targetKind string, targetDeckID uint64, opts PackageImportOptions, report *PackageImportReport, writtenMedia *[]mediaWrite) error {
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
		if err := renameAnkiMedia(ctx, tx, deckID, pkg); err != nil {
			return err
		}
	}

	// The reader exposes mtimes nowhere, so the note type index is keyed by the package's own
	// note type ids (the ids notes reference), and the config carries the Anki id separately.
	types := make(map[int64]anki.NoteType, len(pkg.NoteTypes))
	for i := range pkg.NoteTypes {
		types[pkg.NoteTypes[i].ID] = pkg.NoteTypes[i]
	}
	report.MissingMedia = ankiMissingMedia(pkg)
	report.MediaMissing = len(report.MissingMedia)
	report.UnplayableVideo = ankiUnplayableVideo(pkg)
	report.ExternalAssets = ankiExternalAssets(pkg)
	report.LatexMissingImages = ankiMissingLatexImages(pkg, types)

	targets, err := resolveAnkiNoteTypes(ctx, tx, deckID, pkg)
	if err != nil {
		return err
	}

	// Package-provided bytes are attributed to the importer before the note writes, so the
	// pre-write readability check sees them as supplied by the same user. dry_run writes no row.
	if !opts.DryRun && opts.MediaRoot != "" {
		for sha := range ankiRawMedia(pkg) {
			if err := RecordMediaUploader(ctx, tx, sha, actorUserID); err != nil {
				return fmt.Errorf("import Anki package: record media uploader: %w", err)
			}
		}
	}
	if opts.MediaRoot != "" {
		if err := registerAnkiMediaNames(ctx, tx, deckID, pkg); err != nil {
			return err
		}
	}

	// Existing notes are matched by external_ref only. A content fingerprint match would attach
	// the import to a note the user wrote themselves, which re-using an Anki guid must not do.
	var existing []Note
	if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Find(&existing).Error; err != nil {
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
		var packageNote *anki.Note
		for i := range pkg.Notes {
			if pkg.Notes[i].ID == card.NoteID {
				packageNote = &pkg.Notes[i]
				break
			}
		}
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

	// Media bytes last, on the transaction handle so metadata and content are atomic. dry_run
	// only counts; it neither writes a file nor a row.
	for i := range pkg.Media {
		entry := pkg.Media[i]
		if opts.MediaRoot == "" {
			continue
		}
		_, sha := ankiMediaDigest(entry)
		if len(entry.Data) == 0 {
			continue
		}
		mstore := NewMediaStore(tx)
		stored, err := mstore.BySha256(ctx, sha)
		if err != nil {
			return fmt.Errorf("import Anki package: check media %s: %w", sha, err)
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
		mime, _ := mediatype.Classify(headBytes(entry.Data), entry.Name)
		_, abs, err := mstore.SaveBytesTracked(ctx, opts.MediaRoot, mime, entry.Data, Ptr(actorUserID))
		if abs != "" {
			*writtenMedia = append(*writtenMedia, mediaWrite{sha: sha, abs: abs})
		}
		if err != nil {
			return err
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
func registerAnkiMediaNames(ctx context.Context, tx *gorm.DB, deckID uint64, pkg *anki.Package) error {
	names := NewMediaNameStore(tx)
	for _, entry := range pkg.Media {
		if len(entry.Data) == 0 {
			continue
		}
		name := normaliseMediaName(entry.Name)
		if name == "" {
			continue
		}
		_, sha := ankiMediaDigest(entry)
		if err := names.Upsert(ctx, deckID, name, sha); err != nil {
			return err
		}
	}
	return nil
}

// renameAnkiMedia implements Anki's clash rule for an .apkg import and rewrites this package's
// references. It mirrors the .edeck rule but works on the reader's model, because that is what
// the mapping later consumes.
func renameAnkiMedia(ctx context.Context, tx *gorm.DB, deckID uint64, pkg *anki.Package) error {
	rows, err := NewMediaNameStore(tx).ListByDeck(ctx, deckID)
	if err != nil {
		return fmt.Errorf("import Anki package: list existing media names: %w", err)
	}
	existing := make(map[string]string, len(rows))
	for _, row := range rows {
		existing[row.Name] = row.Sha256
	}
	renames := map[string]string{}
	for i := range pkg.Media {
		entry := &pkg.Media[i]
		if len(entry.Data) == 0 {
			continue
		}
		sha1hex, sha256hex := ankiMediaDigest(*entry)
		oldName := normaliseMediaName(entry.Name)
		newName := addDataToFolderUniquely(existing, oldName, sha256hex, sha1hex)
		if newName != oldName {
			renames[oldName] = newName
		}
		entry.Name = newName
		existing[newName] = sha256hex
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

package store

import (
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

// This file holds the one-time conversion of the old kind-based note content to
// note types, plus the read-only pre-flight check that runs before it.

// MigrationIssue is one note the pre-flight check found. DeckID and NoteID are
// the public ids, so the operator can find the row in the application; Field
// names the offending field and Reason is a one-line English explanation.
type MigrationIssue struct {
	DeckID string
	NoteID string
	Field  string
	Reason string
}

// MigrationReport is the result of the pre-flight check.
//
// Blocking issues make the conversion refuse to run: an array item that
// contains a newline cannot be represented as one line of the joined HTML. Warns
// are informational: the old cloze rule that "|" and "\alt" separate accepted
// answers is removed by the new model, so those characters become ordinary
// content and the migration converts them without splitting.
type MigrationReport struct {
	Blocking []MigrationIssue
	Warnings []MigrationIssue
}

// Blocked reports whether at least one note stops the conversion.
func (r MigrationReport) Blocked() bool { return len(r.Blocking) > 0 }

// CheckNotesMigration reads every note (soft-deleted notes included, because
// they can be restored and the conversion covers them too) and reports the
// notes that block or warn. It never writes.
//
// The CLI subcommand and migration 0012 both call this function, so the check
// an operator sees before upgrading is the same check the migration enforces.
func CheckNotesMigration(ctx context.Context, db *gorm.DB) (MigrationReport, error) {
	return notesMigrationReport(db.WithContext(ctx))
}

// notesMigrationReport is the shared implementation of the pre-flight check.
func notesMigrationReport(db *gorm.DB) (MigrationReport, error) {
	var report MigrationReport
	m := db.Migrator()
	if !m.HasTable("notes") || !m.HasColumn("notes", "kind") {
		// Nothing to convert: the table does not exist yet, or migration 0013
		// already dropped the kind column.
		return report, nil
	}
	decks, err := deckPublicIDs(db)
	if err != nil {
		return report, err
	}
	var rows []struct {
		ID         uint64
		PublicID   string
		DeckID     uint64
		Kind       string
		FieldsJSON string `gorm:"column:fields_json"`
	}
	if err := db.Unscoped().Table("notes").
		Select("id, public_id, deck_id, kind, fields_json").
		Order("id ASC").Scan(&rows).Error; err != nil {
		return report, fmt.Errorf("check notes migration: scan notes: %w", err)
	}
	for _, row := range rows {
		fields, err := ParseFields(row.FieldsJSON)
		if err != nil {
			// A note with unreadable fields blocks the conversion when 0012
			// runs anyway; naming it here would repeat the decode error
			// without the field name, so it is left to the migration.
			continue
		}
		noteID := row.PublicID
		if noteID == "" {
			noteID = strconv.FormatUint(row.ID, 10)
		}
		deckID := decks[row.DeckID]
		if deckID == "" {
			deckID = strconv.FormatUint(row.DeckID, 10)
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, item := range stringItems(fields[key]) {
				if strings.Contains(item, "\n") {
					report.Blocking = append(report.Blocking, MigrationIssue{
						DeckID: deckID, NoteID: noteID, Field: key,
						Reason: "array item contains a newline",
					})
					break
				}
			}
		}
		if row.Kind != "cloze" {
			continue
		}
		text, _ := fields["text"].(string)
		if strings.Contains(text, "|") {
			report.Warnings = append(report.Warnings, MigrationIssue{
				DeckID: deckID, NoteID: noteID, Field: "text",
				Reason: `cloze text contains "|"; it becomes ordinary text, not an alternative-answer separator`,
			})
		}
		if strings.Contains(text, `\alt`) {
			report.Warnings = append(report.Warnings, MigrationIssue{
				DeckID: deckID, NoteID: noteID, Field: "text",
				Reason: `cloze text contains "\alt"; it becomes ordinary text, not an alternative-answer separator`,
			})
		}
	}
	return report, nil
}

// deckPublicIDs maps a deck's numeric id to its public id.
func deckPublicIDs(db *gorm.DB) (map[uint64]string, error) {
	var rows []struct {
		ID       uint64
		PublicID string `gorm:"column:public_id"`
	}
	if err := db.Table("decks").Select("id, public_id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("check notes migration: scan decks: %w", err)
	}
	out := make(map[uint64]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.PublicID
	}
	return out, nil
}

// stringItems returns the string elements of an array field value. Any other
// value yields nothing: only arrays take part in the newline rule.
func stringItems(value any) []string {
	switch items := value.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return items
	}
	return nil
}

// convertNotesToNoteTypes is migration 0012: it creates one note type per
// (deck, kind) and rewrites every note's fields as HTML.
//
// The whole conversion runs inside the migration transaction, so a failure in
// any note leaves the database exactly as it was. cards, card_states and
// reviews are not touched: the conversion changes content only, and every card
// keeps its template value and its progress.
func convertNotesToNoteTypes(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("notes") || !m.HasColumn("notes", "kind") {
		return nil
	}

	report, err := notesMigrationReport(tx)
	if err != nil {
		return err
	}
	if report.Blocked() {
		first := report.Blocking[0]
		return fmt.Errorf(
			"convert notes to note types: note %s in deck %s blocks the migration (%s: %s); run check-migration for the full list",
			first.NoteID, first.DeckID, first.Field, first.Reason)
	}
	if len(report.Warnings) > 0 {
		slog.Warn("notes conversion: cloze alternative-answer syntax becomes ordinary text",
			"warnings", len(report.Warnings))
	}

	types, err := createEngramNoteTypes(tx)
	if err != nil {
		return err
	}
	return convertNoteRows(tx, types)
}

// deckKind identifies one note type a deck needs.
type deckKind struct {
	DeckID uint64
	Kind   string
}

// noteTypeRef is the created note type plus the field ids whose values are plain
// text rather than HTML. The converter reads plainFields; the migration itself
// only needs the id.
type noteTypeRef struct {
	ID          uint64
	plainFields map[string]bool
}

// createEngramNoteTypes creates the note type a deck needs for each kind its
// notes use, and returns the numeric id per (deck, kind). Soft-deleted notes
// are included: they can be restored, and migration 0013 makes note_type_id
// NOT NULL, so a note without a type would be impossible to keep.
func createEngramNoteTypes(tx *gorm.DB) (map[deckKind]noteTypeRef, error) {
	var rows []struct {
		DeckID uint64 `gorm:"column:deck_id"`
		Kind   string `gorm:"column:kind"`
	}
	if err := tx.Unscoped().Table("notes").
		Select("DISTINCT deck_id, kind").
		Where("kind <> ''").
		Order("deck_id ASC, kind ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("convert notes to note types: list deck kinds: %w", err)
	}
	out := make(map[deckKind]noteTypeRef, len(rows))
	for _, row := range rows {
		stock, ok := notetype.EngramStockByKind(row.Kind)
		if !ok {
			return nil, fmt.Errorf("convert notes to note types: deck %d uses unknown kind %q", row.DeckID, row.Kind)
		}
		if err := validateEngramStock(stock); err != nil {
			return nil, err
		}
		nt := NoteType{DeckID: row.DeckID}
		if err := nt.SetConfig(stock.Name, stock.Config); err != nil {
			return nil, fmt.Errorf("convert notes to note types: deck %d kind %q: %w", row.DeckID, row.Kind, err)
		}
		if err := tx.Create(&nt).Error; err != nil {
			return nil, fmt.Errorf("convert notes to note types: create note type for deck %d kind %q: %w", row.DeckID, row.Kind, err)
		}
		plainFields := make(map[string]bool)
		for _, field := range stock.Config.Fields {
			if field.PlainText {
				plainFields[field.ID] = true
			}
		}
		out[deckKind{DeckID: row.DeckID, Kind: row.Kind}] = noteTypeRef{ID: nt.ID, plainFields: plainFields}
	}
	return out, nil
}

// validateEngramStock runs both validators the write path runs: the note type
// rules (fields, templates, placeholder count) and the answer control's role
// rules. A stock definition that fails would be created by the migration and
// only fail later when a card is rendered, so it is rejected here instead.
func validateEngramStock(stock notetype.EngramStock) error {
	if err := stock.Config.Validate(); err != nil {
		return fmt.Errorf("convert notes to note types: stock kind %q: %w", stock.Kind, err)
	}
	for _, template := range stock.Config.Templates {
		if template.Answer == nil {
			continue
		}
		if err := answer.ValidateSpec(*template.Answer); err != nil {
			return fmt.Errorf("convert notes to note types: stock kind %q template %q: %w", stock.Kind, template.Name, err)
		}
	}
	return nil
}

// convertNoteRows rewrites every note in primary-key batches, so memory use is
// independent of the note count. A note whose fields cannot be decoded stops
// the whole migration: partial content cannot be represented in the new model.
func convertNoteRows(tx *gorm.DB, types map[deckKind]noteTypeRef) error {
	const batchSize = 500
	var last uint64
	for {
		var batch []struct {
			ID         uint64
			DeckID     uint64
			Kind       string
			FieldsJSON string `gorm:"column:fields_json"`
		}
		if err := tx.Unscoped().Table("notes").
			Select("id, deck_id, kind, fields_json").
			Where("id > ?", last).Order("id ASC").Limit(batchSize).
			Scan(&batch).Error; err != nil {
			return fmt.Errorf("convert notes to note types: scan notes after id %d: %w", last, err)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			last = row.ID
			noteType, ok := types[deckKind{DeckID: row.DeckID, Kind: row.Kind}]
			if !ok {
				return fmt.Errorf("convert notes to note types: note %d has kind %q with no created note type", row.ID, row.Kind)
			}
			fields, err := ParseFields(row.FieldsJSON)
			if err != nil {
				return fmt.Errorf("convert notes to note types: note %d: %w", row.ID, err)
			}
			converter := &fieldConverter{
				tx: tx, deckID: row.DeckID, mediaMime: map[string]string{},
				plainFields: noteType.plainFields,
			}
			converted := make(map[string]any, len(fields))
			var search []string
			var sourceURL *string
			var extraJSON *string
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				value := fields[key]
				switch key {
				case "source_url":
					if text, ok := value.(string); ok && text != "" {
						sourceURL = &text
					}
					continue
				case "extra":
					if value == nil {
						continue
					}
					raw, err := json.Marshal(value)
					if err != nil {
						return fmt.Errorf("convert notes to note types: note %d field extra: %w", row.ID, err)
					}
					text := string(raw)
					extraJSON = &text
					continue
				}
				html, err := converter.fieldHTML(key, value)
				if err != nil {
					return fmt.Errorf("convert notes to note types: note %d: %w", row.ID, err)
				}
				converted[key] = html
				if text := notetype.PlainText(html); text != "" {
					search = append(search, text)
				}
			}
			raw, err := json.Marshal(converted)
			if err != nil {
				return fmt.Errorf("convert notes to note types: note %d: encode fields: %w", row.ID, err)
			}
			if err := tx.Unscoped().Model(&Note{}).Where("id = ?", row.ID).Updates(map[string]any{
				"fields_json":  string(raw),
				"search_text":  strings.ToLower(strings.Join(search, "\n")),
				"note_type_id": noteType.ID,
				"source_url":   sourceURL,
				"extra_json":   extraJSON,
				"updated_at":   time.Now().UTC(),
			}).Error; err != nil {
				return fmt.Errorf("convert notes to note types: note %d: write: %w", row.ID, err)
			}
			if err := converter.writeMediaNames(); err != nil {
				return fmt.Errorf("convert notes to note types: note %d: %w", row.ID, err)
			}
		}
	}
}

// fieldConverter converts one note's field values and remembers the media names
// that conversion referenced.
type fieldConverter struct {
	tx          *gorm.DB
	deckID      uint64
	mediaMime   map[string]string
	plainFields map[string]bool
	names       []MediaName
	seen        map[string]bool
}

// fieldHTML converts one field value to its stored form.
//
// A field whose note type marks it plain_text holds machine syntax, not
// user-visible rich text (typed notes' regex is the one such field today). Its
// value is HTML-escaped and stored literally: Markdown would interpret *, _ and
// backticks and the pattern would stop meaning what it meant. Every other
// string value runs the full Markdown/TTS/audio/media pipeline.
func (c *fieldConverter) fieldHTML(key string, value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		if c.plainFields[key] {
			return stdhtml.EscapeString(typed), nil
		}
		return c.markdown(typed, false)
	case []any:
		items := make([]string, 0, len(typed))
		for i, item := range typed {
			html, err := c.arrayItemHTML(key, i, item)
			if err != nil {
				return "", err
			}
			items = append(items, html)
		}
		return strings.Join(items, "<br>"), nil
	case []string:
		items := make([]string, 0, len(typed))
		for i, text := range typed {
			html, err := c.arrayItemHTML(key, i, text)
			if err != nil {
				return "", err
			}
			items = append(items, html)
		}
		return strings.Join(items, "<br>"), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case json.Number:
		return typed.String(), nil
	default:
		return "", fmt.Errorf("field %q has unsupported type %T", key, value)
	}
}

// arrayItemHTML converts one array element. Today an array is either a list of
// strings (options, items, accepted wordings) or a list of option indices
// (answers), so the element follows the same scalar rules as a field value. A
// string element runs the whole string pipeline (Markdown, then TTS, audio and
// media), because an item is a field value in miniature and the media rewrite
// has to reach a choice option that contains an image as well.
func (c *fieldConverter) arrayItemHTML(key string, index int, value any) (string, error) {
	switch typed := value.(type) {
	case string:
		if c.plainFields[key] {
			return stdhtml.EscapeString(typed), nil
		}
		return c.markdown(typed, true)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case json.Number:
		return typed.String(), nil
	case bool:
		return strconv.FormatBool(typed), nil
	default:
		return "", fmt.Errorf("field %q item %d has unsupported type %T", key, index, value)
	}
}

// markdown runs the conversion steps that touch a text value: Markdown to HTML,
// then TTS, audio and media references. inline selects the fragment form used
// for one string-array item.
func (c *fieldConverter) markdown(src string, inline bool) (string, error) {
	var html string
	var err error
	if inline {
		html, err = renderMigrationMarkdownInline(src)
	} else {
		html, err = renderMigrationMarkdown(src)
	}
	if err != nil {
		return "", err
	}
	html = expandTTSShortcodes(html)
	html = c.expandAudioShortcodes(html)
	return mediaPathRE.ReplaceAllStringFunc(html, func(match string) string {
		return c.mediaName(strings.TrimPrefix(match, "/media/"))
	}), nil
}

// mediaName returns the deck-scoped file name for a content sha and remembers
// the media_names row. The extension comes from mediaDeckName, the one
// sha-plus-mime rule the live note write path uses too; a reference whose media
// row is gone gets the fallback extension, so the field still names a file.
func (c *fieldConverter) mediaName(sha string) string {
	mime, ok := c.mediaMime[sha]
	if !ok {
		var media Media
		if err := c.tx.Select("mime").First(&media, "sha256 = ?", sha).Error; err == nil {
			mime = media.Mime
		}
		c.mediaMime[sha] = mime
	}
	name := mediaDeckName(sha, mime)
	if c.seen == nil {
		c.seen = make(map[string]bool)
	}
	if !c.seen[name] {
		c.seen[name] = true
		c.names = append(c.names, MediaName{DeckID: c.deckID, Name: normaliseMediaName(name), Sha256: sha})
	}
	return name
}

// writeMediaNames writes the note's media_names rows. The upsert makes the
// migration safe when two notes in one deck reference the same file name.
func (c *fieldConverter) writeMediaNames() error {
	if len(c.names) == 0 {
		return nil
	}
	if err := c.tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "deck_id"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"sha256"}),
	}).Create(&c.names).Error; err != nil {
		return fmt.Errorf("register media names: %w", err)
	}
	return nil
}

// ttsShortcodeRE matches the whole {{tts:...}} form. The body is allowed to
// contain HTML (step 1 already converted Markdown), so the match is non-greedy
// and stops at the first closing braces.
var ttsShortcodeRE = regexp.MustCompile(`(?s)\{\{tts:(.*?)\}\}`)

// audioShortcodeRE matches the whole {{audio:...}} form.
var audioShortcodeRE = regexp.MustCompile(`(?s)\{\{audio:(.*?)\}\}`)

// mediaPathRE matches one content-addressed media path.
var mediaPathRE = regexp.MustCompile(`/media/([0-9a-f]{64})`)

// shortcodeNumberRE is the numeric shortcode parameter shape: a non-negative
// decimal with at most six fractional digits.
var shortcodeNumberRE = regexp.MustCompile(`^[0-9]{1,9}(?:\.[0-9]{1,6})?$`)

// expandTTSShortcodes turns every {{tts:X|lang=L|rate=R|hide}} marker into
// Anki's speech syntax. The visible X stays in front of the directive, so a
// reader without a speech engine still sees the text; hide drops that visible
// copy because the author asked the client not to show it.
func expandTTSShortcodes(html string) string {
	return ttsShortcodeRE.ReplaceAllStringFunc(html, func(match string) string {
		inner := match[len("{{tts:") : len(match)-len("}}")]
		parts := strings.Split(inner, "|")
		body := parts[0]
		if body == "" {
			return match
		}
		lang, rate, hide := "", "", false
		for _, raw := range parts[1:] {
			field := strings.TrimSpace(raw)
			if field == "" {
				continue
			}
			key, value, hasValue := strings.Cut(field, "=")
			switch {
			case !hasValue && key == "hide":
				hide = true
			case hasValue && key == "lang":
				lang = strings.ReplaceAll(value, "-", "_")
			case hasValue && key == "rate":
				rate = value
			default:
				return match
			}
		}
		var b strings.Builder
		if !hide {
			b.WriteString(body)
		}
		b.WriteString("[anki:tts")
		if lang != "" {
			b.WriteString(" lang=" + lang)
		}
		if rate != "" {
			b.WriteString(" speed=" + rate)
		}
		b.WriteString("]")
		b.WriteString(body)
		b.WriteString("[/anki:tts]")
		return b.String()
	})
}

// expandAudioShortcodes turns every {{audio:/media/S|...}} marker into an Anki
// sound tag, wrapped in the engram-sound span when the author set any segment,
// rate or loop. Without parameters the plain [sound:S.ext] form is emitted, so
// Anki reads the file name directly.
func (c *fieldConverter) expandAudioShortcodes(html string) string {
	return audioShortcodeRE.ReplaceAllStringFunc(html, func(match string) string {
		inner := match[len("{{audio:") : len(match)-len("}}")]
		parts := strings.Split(inner, "|")
		src := strings.TrimSpace(parts[0])
		sha, ok := strings.CutPrefix(src, "/media/")
		if !ok || !shortcodeSHA(sha) {
			return match
		}
		values := map[string]string{}
		loop := false
		for _, raw := range parts[1:] {
			field := strings.TrimSpace(raw)
			if field == "" {
				continue
			}
			key, value, hasValue := strings.Cut(field, "=")
			switch {
			case !hasValue && key == "loop":
				loop = true
			case hasValue && (key == "start" || key == "end" || key == "rate") &&
				shortcodeNumberRE.MatchString(value):
				values[key] = value
			default:
				return match
			}
		}
		sound := "[sound:" + c.mediaName(sha) + "]"
		if len(values) == 0 && !loop {
			return sound
		}
		var b strings.Builder
		b.WriteString(`<span class="engram-sound"`)
		for _, key := range []string{"start", "end", "rate"} {
			if value, ok := values[key]; ok {
				b.WriteString(` data-` + key + `="` + value + `"`)
			}
		}
		if loop {
			b.WriteString(` data-loop="1"`)
		}
		b.WriteString(">")
		b.WriteString(sound)
		b.WriteString("</span>")
		return b.String()
	})
}

// shortcodeSHA reports whether s is one 64-character lowercase hex sha.
func shortcodeSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9') && !(s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// notesNoteTypeNotNull is migration 0013: it makes notes.note_type_id NOT NULL
// and drops notes.kind, the last two pieces of the old content model.
//
// SQLite cannot change a column's nullability in place, so its table is rebuilt
// from the current DDL with only that column replaced. The rebuild drops the
// table's indexes along with it, so their SQL is saved first and replayed
// afterwards. PostgreSQL changes the constraint and drops the column in place.
func notesNoteTypeNotNull(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("notes") || !m.HasColumn("notes", "note_type_id") {
		return nil
	}
	var nulls int64
	if err := tx.Unscoped().Table("notes").Where("note_type_id IS NULL").Count(&nulls).Error; err != nil {
		return fmt.Errorf("notes note type not null: count notes without a note type: %w", err)
	}
	if nulls > 0 {
		return fmt.Errorf("notes note type not null: %d note(s) still have no note type", nulls)
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		if m.HasColumn("notes", "kind") {
			if err := tx.Exec("ALTER TABLE notes DROP COLUMN kind").Error; err != nil {
				return fmt.Errorf("notes note type not null: drop kind: %w", err)
			}
		}
		// The rebuild drops every index and trigger attached to notes; save and
		// restore them so a unique constraint cannot silently disappear.
		var objects []struct{ SQL string }
		if err := tx.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL", "notes").Scan(&objects).Error; err != nil {
			return fmt.Errorf("notes note type not null: read indexes: %w", err)
		}
		if err := m.AlterColumn(&noteTypeNotNullColumn{}, "NoteTypeID"); err != nil {
			return fmt.Errorf("notes note type not null: alter column: %w", err)
		}
		for _, object := range objects {
			if err := tx.Exec(object.SQL).Error; err != nil {
				return fmt.Errorf("notes note type not null: restore index: %w", err)
			}
		}
		return nil
	case "postgres":
		if err := tx.Exec("ALTER TABLE notes ALTER COLUMN note_type_id SET NOT NULL").Error; err != nil {
			return fmt.Errorf("notes note type not null: set not null: %w", err)
		}
		if err := tx.Exec("ALTER TABLE notes DROP COLUMN IF EXISTS kind").Error; err != nil {
			return fmt.Errorf("notes note type not null: drop kind: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("notes note type not null: unsupported dialect %q", tx.Dialector.Name())
	}
}

// noteTypeNotNullColumn is the column shape migration 0013 writes: the same
// table, with note_type_id carrying NOT NULL. It exists only for the SQLite
// table rebuild.
type noteTypeNotNullColumn struct {
	NoteTypeID uint64 `gorm:"column:note_type_id;not null"`
}

func (noteTypeNotNullColumn) TableName() string { return "notes" }

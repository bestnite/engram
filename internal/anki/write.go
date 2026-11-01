package anki

import (
	"archive/zip"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	_ "github.com/glebarez/sqlite" // registers the pure-Go "sqlite" database/sql driver
)

// WritePackage is one package to write. Media entries are numbered in slice order:
// the first entry is written as zip entry "0", the second as "1", and the media table
// maps each index to the entry's file name.
type WritePackage struct {
	Collection WriteCollection
	Media      []WriteMedia
}

// WriteCollection is one schema-11 collection. The collection's creation timestamp is
// the only wall-clock value: every other timestamp and identifier is supplied by the
// caller from stored data, so exporting the same rows twice produces the same bytes.
type WriteCollection struct {
	// CreatedAt is col.crt in seconds since the Unix epoch. Review due days are measured
	// from it, as Anki measures them.
	CreatedAt int64
	// ModifiedMS is col.mod in milliseconds and SchemaModifiedMS is col.scm.
	ModifiedMS       int64
	SchemaModifiedMS int64
	NoteTypes        []WriteNoteType
	Decks            []WriteDeck
	DeckConfigs      []WriteDeckConfig
	Notes            []WriteNote
	Cards            []WriteCard
	Reviews          []WriteReview
}

// WriteNoteType is one Anki note type in the schema-11 model JSON. The required keys are
// id, name, type, mod, usn, did, sortf, css, flds and tmpls; req is omitted because the
// importer recomputes it.
type WriteNoteType struct {
	ID                int64
	Name              string
	Kind              int
	Modified          int64
	SortFieldIndex    int
	Fields            []Field
	Templates         []Template
	CSS               string
	LaTeXPre          string
	LaTeXPost         string
	LaTeXSVG          bool
	OriginalStockKind int
	// AnswerSpecs maps a template name to the answer control that template grades.
	// Bindings name fields, not field ids: field ids are reassigned when a package is
	// imported, while names survive that assignment.
	AnswerSpecs map[string]AnswerSpec
}

// AnswerSpec is the answer control of one template as carried in the note type's
// "engram" key. Bindings values are field names.
type AnswerSpec struct {
	Control  string            `json:"control"`
	Bindings map[string]string `json:"bindings"`
}

// WriteDeck is one Anki deck; Name uses "::" to separate subdeck components.
type WriteDeck struct {
	ID          int64
	Name        string
	Description string
	ConfigID    int64
	Modified    int64
}

// WriteDeckConfig is one legacy dconf entry. The entry's ID must not be 1: a config with
// id 1 merges into the target collection's stock "Default" and its values are lost.
type WriteDeckConfig struct {
	ID                  int64
	Name                string
	DesiredRetention    float64
	LearningSteps       []float64 // minutes
	RelearningSteps     []float64 // minutes
	MaximumIntervalDays int
	// FSRSWeights holds the 21 FSRS-6 parameters. Anki reads them from fsrsParams6;
	// fsrsWeights is the older 17-value field and is always written empty.
	FSRSWeights []float64
}

// WriteNote is one note row. Fields holds the values in the note type's field order;
// SortField is that field's plain text. Anki recomputes sfld and csum on import, so the
// writer's sfld and csum values are approximations.
type WriteNote struct {
	ID         int64
	GUID       string
	NoteTypeID int64
	Modified   int64
	Tags       []string
	Fields     []string
	SortField  string
}

// WriteCard is one card row.
type WriteCard struct {
	ID       int64
	NoteID   int64
	DeckID   int64
	Ordinal  int
	Modified int64
	// Scheduling state. A content-only card is new: Type and Queue zero, Due is its
	// new-card position, and every scheduling column is zero.
	Type       int
	Queue      int
	Due        int64
	Ivl        int
	Factor     int
	Reps       int
	Lapses     int
	Left       int
	Stability  *float64
	Difficulty *float64
	LastReview *int64
}

// WriteReview is one revlog row. CID must name a card written in the same archive;
// Anki silently drops rows whose cid does not resolve.
type WriteReview struct {
	ID      int64
	CardID  int64
	Ease    int
	Ivl     int
	LastIvl int
	Factor  int
	Time    int
	Type    int
}

// WriteMedia is one media file. Name is the file name Anki stores and the media table
// maps an index to; Bytes are the raw file contents.
type WriteMedia struct {
	Name  string
	Bytes []byte
}

// WriteArchive writes pkg as a legacy-2 .apkg: a "meta" entry with bytes 0x08 0x02, a
// "collection.anki21" SQLite file, a "media" JSON index, and one numbered root entry per
// media file. Anki 26.09.3 reads this layout, and so does this package's reader. No
// collection.anki2 stub and no collection.anki21b entry are written; the verified layout
// does not need either.
//
// The output is deterministic: the entry order is fixed, the zip header timestamps are
// zero, and the collection contains only values supplied by the caller. Exporting the
// same WritePackage twice yields byte-identical archives.
func WriteArchive(w io.Writer, pkg *WritePackage) error {
	collection, err := buildCollection(&pkg.Collection)
	if err != nil {
		return err
	}
	return writeZip(w, collection, pkg.Media)
}

// apiVersion is the schema-11 file version Anki accepts. The reference importer rejects
// 9 as FileTooOld and 12 as FileTooNew.
const apiVersion = 11

// metaLegacy2 tells Anki that collection.anki21 is a legacy-2 package.
var metaLegacy2 = []byte{0x08, 0x02}

// schema11DDL is the schema the reference package carries. The indexes are part of the
// layout Anki expects to find; notes and cards are read by both Anki and this repository.
var schema11DDL = []string{
	`CREATE TABLE col (
  id integer PRIMARY KEY,
  crt integer NOT NULL,
  mod integer NOT NULL,
  scm integer NOT NULL,
  ver integer NOT NULL,
  dty integer NOT NULL,
  usn integer NOT NULL,
  ls integer NOT NULL,
  conf text NOT NULL,
  models text NOT NULL,
  decks text NOT NULL,
  dconf text NOT NULL,
  tags text NOT NULL
)`,
	`CREATE TABLE notes (
  id integer PRIMARY KEY,
  guid text NOT NULL,
  mid integer NOT NULL,
  mod integer NOT NULL,
  usn integer NOT NULL,
  tags text NOT NULL,
  flds text NOT NULL,
  sfld integer NOT NULL,
  csum integer NOT NULL,
  flags integer NOT NULL,
  data text NOT NULL
)`,
	`CREATE TABLE cards (
  id integer PRIMARY KEY,
  nid integer NOT NULL,
  did integer NOT NULL,
  ord integer NOT NULL,
  mod integer NOT NULL,
  usn integer NOT NULL,
  type integer NOT NULL,
  queue integer NOT NULL,
  due integer NOT NULL,
  ivl integer NOT NULL,
  factor integer NOT NULL,
  reps integer NOT NULL,
  lapses integer NOT NULL,
  left integer NOT NULL,
  odue integer NOT NULL,
  odid integer NOT NULL,
  flags integer NOT NULL,
  data text NOT NULL
)`,
	`CREATE TABLE revlog (
  id integer PRIMARY KEY,
  cid integer NOT NULL,
  usn integer NOT NULL,
  ease integer NOT NULL,
  ivl integer NOT NULL,
  lastIvl integer NOT NULL,
  factor integer NOT NULL,
  time integer NOT NULL,
  type integer NOT NULL
)`,
	`CREATE TABLE graves (
  usn integer NOT NULL,
  oid integer NOT NULL,
  type integer NOT NULL
)`,
	`CREATE INDEX ix_notes_usn ON notes (usn)`,
	`CREATE INDEX ix_cards_usn ON cards (usn)`,
	`CREATE INDEX ix_revlog_usn ON revlog (usn)`,
	`CREATE INDEX ix_cards_nid ON cards (nid)`,
	`CREATE INDEX ix_cards_sched ON cards (did, queue, due)`,
	`CREATE INDEX ix_revlog_cid ON revlog (cid)`,
	`CREATE INDEX ix_notes_csum ON notes (csum)`,
}

// buildCollection renders the SQLite payload into a temporary file and returns its bytes.
func buildCollection(c *WriteCollection) ([]byte, error) {
	file, err := os.CreateTemp("", "engram-anki-export-*.sqlite")
	if err != nil {
		return nil, fmt.Errorf("anki package: create collection file: %w", err)
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("anki package: open collection: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("anki package: begin collection: %w", err)
	}
	defer tx.Rollback()

	for _, statement := range schema11DDL {
		if _, err := tx.Exec(statement); err != nil {
			return nil, fmt.Errorf("anki package: create collection schema: %w", err)
		}
	}

	models, err := json.Marshal(modelIndex(c.NoteTypes))
	if err != nil {
		return nil, fmt.Errorf("anki package: encode note types: %w", err)
	}
	decks, err := json.Marshal(deckIndex(c.Decks))
	if err != nil {
		return nil, fmt.Errorf("anki package: encode decks: %w", err)
	}
	dconf, err := json.Marshal(configIndex(c.DeckConfigs))
	if err != nil {
		return nil, fmt.Errorf("anki package: encode deck configs: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO col (id, crt, mod, scm, ver, dty, usn, ls, conf, models, decks, dconf, tags)
VALUES (1, ?, ?, ?, ?, 0, 0, 0, '{}', ?, ?, ?, '{}')`,
		c.CreatedAt, c.ModifiedMS, c.SchemaModifiedMS, apiVersion, string(models), string(decks), string(dconf)); err != nil {
		return nil, fmt.Errorf("anki package: write col row: %w", err)
	}

	noteStmt, err := tx.Prepare(`INSERT INTO notes (id, guid, mid, mod, usn, tags, flds, sfld, csum, flags, data)
VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, 0, '')`)
	if err != nil {
		return nil, fmt.Errorf("anki package: prepare notes: %w", err)
	}
	defer noteStmt.Close()
	for i := range c.Notes {
		n := &c.Notes[i]
		if _, err := noteStmt.Exec(n.ID, n.GUID, n.NoteTypeID, n.Modified, ankiTags(n.Tags),
			strings.Join(n.Fields, "\x1f"), n.SortField, fieldChecksum(n.SortField)); err != nil {
			return nil, fmt.Errorf("anki package: write note %d: %w", n.ID, err)
		}
	}

	cardStmt, err := tx.Prepare(`INSERT INTO cards (id, nid, did, ord, mod, usn, type, queue, due, ivl, factor, reps, lapses, left, odue, odid, flags, data)
VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, ?)`)
	if err != nil {
		return nil, fmt.Errorf("anki package: prepare cards: %w", err)
	}
	defer cardStmt.Close()
	for i := range c.Cards {
		card := &c.Cards[i]
		// Anki discards cards.data on import; an Engram round trip uses it to retain
		// memory state and the last review time (lrt).
		fields := make(map[string]any)
		if card.Stability != nil && card.Difficulty != nil {
			fields["s"], fields["d"] = *card.Stability, *card.Difficulty
		}
		if card.LastReview != nil {
			fields["lrt"] = *card.LastReview
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("anki package: encode card %d data: %w", card.ID, err)
		}
		data := string(encoded)
		if _, err := cardStmt.Exec(card.ID, card.NoteID, card.DeckID, card.Ordinal, card.Modified,
			card.Type, card.Queue, card.Due, card.Ivl, card.Factor, card.Reps, card.Lapses, card.Left, data); err != nil {
			return nil, fmt.Errorf("anki package: write card %d: %w", card.ID, err)
		}
	}

	reviewStmt, err := tx.Prepare(`INSERT INTO revlog (id, cid, usn, ease, ivl, lastIvl, factor, time, type)
VALUES (?, ?, 0, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("anki package: prepare revlog: %w", err)
	}
	defer reviewStmt.Close()
	for i := range c.Reviews {
		r := &c.Reviews[i]
		if _, err := reviewStmt.Exec(r.ID, r.CardID, r.Ease, r.Ivl, r.LastIvl, r.Factor, r.Time, r.Type); err != nil {
			return nil, fmt.Errorf("anki package: write review %d: %w", r.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("anki package: commit collection: %w", err)
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("anki package: close collection: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("anki package: read collection: %w", err)
	}
	return raw, nil
}

// legacyModelOut is the schema-11 model JSON. Field names match the layout Anki writes.
type legacyModelOut struct {
	ID                int64            `json:"id"`
	Name              string           `json:"name"`
	Type              int              `json:"type"`
	Mod               int64            `json:"mod"`
	Usn               int              `json:"usn"`
	Did               *int64           `json:"did"`
	Sortf             int              `json:"sortf"`
	CSS               string           `json:"css"`
	Flds              []legacyFieldOut `json:"flds"`
	Tmpls             []legacyTmplOut  `json:"tmpls"`
	LaTeXPre          string           `json:"latexPre"`
	LaTeXPost         string           `json:"latexPost"`
	LaTeXSVG          bool             `json:"latexsvg"`
	OriginalStockKind int              `json:"originalStockKind"`
	Engram            *legacyEngramOut `json:"engram,omitempty"`
}

type legacyFieldOut struct {
	Ord               int    `json:"ord"`
	Name              string `json:"name"`
	ID                int64  `json:"id"`
	Sticky            bool   `json:"sticky"`
	RTL               bool   `json:"rtl"`
	Font              string `json:"font"`
	Size              int    `json:"size"`
	Description       string `json:"description"`
	PlainText         bool   `json:"plainText"`
	Collapsed         bool   `json:"collapsed"`
	ExcludeFromSearch bool   `json:"excludeFromSearch"`
	Tag               *int   `json:"tag"`
	PreventDeletion   bool   `json:"preventDeletion"`
}

type legacyTmplOut struct {
	Ord      int    `json:"ord"`
	Name     string `json:"name"`
	ID       int64  `json:"id"`
	QFormat  string `json:"qfmt"`
	AFormat  string `json:"afmt"`
	BQFormat string `json:"bqfmt"`
	BAFormat string `json:"bafmt"`
	BFont    string `json:"bfont"`
	BSize    int    `json:"bsize"`
	Did      *int64 `json:"did"`
}

// legacyEngramOut is the one unknown model key engram adds. Anki carries an unknown
// top-level model key through import and through both of its exports, so a deck exported
// and imported back keeps machine grading. Version 1 stores answer specs keyed by
// template name, with binding values taken from field names.
type legacyEngramOut struct {
	V           int                   `json:"v"`
	AnswerSpecs map[string]AnswerSpec `json:"answer_specs,omitempty"`
}

func modelIndex(types []WriteNoteType) map[string]legacyModelOut {
	out := make(map[string]legacyModelOut, len(types))
	for _, nt := range types {
		fields := make([]legacyFieldOut, len(nt.Fields))
		for i, f := range nt.Fields {
			fields[i] = legacyFieldOut{
				Ord: f.Ord, Name: f.Name, ID: int64(i), Sticky: f.Sticky, RTL: f.RTL,
				Font: f.Font, Size: f.FontSize, Description: f.Description,
				PlainText: f.PlainText, Collapsed: f.Collapsed,
				ExcludeFromSearch: f.ExcludeFromSearch, Tag: f.Tag,
				PreventDeletion: f.PreventDeletion,
			}
		}
		templates := make([]legacyTmplOut, len(nt.Templates))
		for i, t := range nt.Templates {
			templates[i] = legacyTmplOut{
				Ord: t.Ord, Name: t.Name, ID: int64(i), QFormat: t.QFormat,
				AFormat: t.AFormat, BQFormat: t.BQFormat, BAFormat: t.BAFormat,
				BFont: t.BrowserFont, BSize: t.BrowserFontSize,
			}
		}
		model := legacyModelOut{
			ID: nt.ID, Name: nt.Name, Type: nt.Kind, Mod: nt.Modified, Usn: 0,
			Sortf: nt.SortFieldIndex, CSS: nt.CSS, Flds: fields, Tmpls: templates,
			LaTeXPre: nt.LaTeXPre, LaTeXPost: nt.LaTeXPost, LaTeXSVG: nt.LaTeXSVG,
			OriginalStockKind: nt.OriginalStockKind,
		}
		if len(nt.AnswerSpecs) > 0 {
			model.Engram = &legacyEngramOut{V: 1, AnswerSpecs: nt.AnswerSpecs}
		}
		out[strconv.FormatInt(nt.ID, 10)] = model
	}
	return out
}

// legacyDeckOut is the schema-11 deck JSON. Nullable counters are null, as the reference
// package writes them.
type legacyDeckOut struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Desc             string   `json:"desc"`
	Conf             int64    `json:"conf"`
	Usn              int      `json:"usn"`
	Mod              int64    `json:"mod"`
	Dyn              int      `json:"dyn"`
	Collapsed        bool     `json:"collapsed"`
	BrowserCollapsed bool     `json:"browserCollapsed"`
	NewToday         [2]int   `json:"newToday"`
	RevToday         [2]int   `json:"revToday"`
	LrnToday         [2]int   `json:"lrnToday"`
	TimeToday        [2]int   `json:"timeToday"`
	ExtendNew        int      `json:"extendNew"`
	ExtendRev        int      `json:"extendRev"`
	NewLimit         *int     `json:"newLimit"`
	NewLimitToday    *int     `json:"newLimitToday"`
	ReviewLimit      *int     `json:"reviewLimit"`
	ReviewLimitToday *int     `json:"reviewLimitToday"`
	DesiredRetention *float64 `json:"desiredRetention"`
}

func deckIndex(decks []WriteDeck) map[string]legacyDeckOut {
	out := make(map[string]legacyDeckOut, len(decks))
	for _, d := range decks {
		out[strconv.FormatInt(d.ID, 10)] = legacyDeckOut{
			ID: d.ID, Name: d.Name, Desc: d.Description, Conf: d.ConfigID,
			Mod: d.Modified, Collapsed: true, BrowserCollapsed: true,
		}
	}
	return out
}

// legacyDconfOut is the legacy deck-configuration JSON. fsrsWeights and fsrsParams5 stay
// empty: Anki reads fsrsWeights as the older 17-value fsrs_params_4 field, and the writer
// only carries the 21 FSRS-6 values that fsrsParams6 names.
type legacyDconfOut struct {
	ID                      int64       `json:"id"`
	Mod                     int64       `json:"mod"`
	Name                    string      `json:"name"`
	Usn                     int         `json:"usn"`
	MaxTaken                int         `json:"maxTaken"`
	Autoplay                bool        `json:"autoplay"`
	Timer                   int         `json:"timer"`
	Replayq                 bool        `json:"replayq"`
	New                     legacyNew   `json:"new"`
	Rev                     legacyRev   `json:"rev"`
	Lapse                   legacyLapse `json:"lapse"`
	Dyn                     bool        `json:"dyn"`
	NewMix                  int         `json:"newMix"`
	NewPerDayMinimum        int         `json:"newPerDayMinimum"`
	InterdayLearningMix     int         `json:"interdayLearningMix"`
	ReviewOrder             int         `json:"reviewOrder"`
	NewSortOrder            int         `json:"newSortOrder"`
	NewGatherPriority       int         `json:"newGatherPriority"`
	BuryInterdayLearning    bool        `json:"buryInterdayLearning"`
	FsrsWeights             []float64   `json:"fsrsWeights"`
	FsrsParams5             []float64   `json:"fsrsParams5"`
	FsrsParams6             []float64   `json:"fsrsParams6"`
	DesiredRetention        float64     `json:"desiredRetention"`
	IgnoreRevlogsBeforeDate string      `json:"ignoreRevlogsBeforeDate"`
	EasyDaysPercentages     []float64   `json:"easyDaysPercentages"`
	StopTimerOnAnswer       bool        `json:"stopTimerOnAnswer"`
	SecondsToShowQuestion   float64     `json:"secondsToShowQuestion"`
	SecondsToShowAnswer     float64     `json:"secondsToShowAnswer"`
	QuestionAction          int         `json:"questionAction"`
	AnswerAction            int         `json:"answerAction"`
	WaitForAudio            bool        `json:"waitForAudio"`
	SM2Retention            float64     `json:"sm2Retention"`
	WeightSearch            string      `json:"weightSearch"`
}

type legacyNew struct {
	Bury          bool      `json:"bury"`
	Delays        []float64 `json:"delays"`
	InitialFactor int       `json:"initialFactor"`
	Ints          [3]int    `json:"ints"`
	Order         int       `json:"order"`
	PerDay        int       `json:"perDay"`
}

type legacyRev struct {
	Bury       bool    `json:"bury"`
	Ease4      float64 `json:"ease4"`
	IvlFct     float64 `json:"ivlFct"`
	MaxIvl     int     `json:"maxIvl"`
	PerDay     int     `json:"perDay"`
	HardFactor float64 `json:"hardFactor"`
}

type legacyLapse struct {
	Delays      []float64 `json:"delays"`
	LeechAction int       `json:"leechAction"`
	LeechFails  int       `json:"leechFails"`
	MinInt      int       `json:"minInt"`
	Mult        float64   `json:"mult"`
}

func configIndex(configs []WriteDeckConfig) map[string]legacyDconfOut {
	out := make(map[string]legacyDconfOut, len(configs))
	for _, cfg := range configs {
		weights := cfg.FSRSWeights
		if weights == nil {
			weights = []float64{}
		}
		out[strconv.FormatInt(cfg.ID, 10)] = legacyDconfOut{
			ID: cfg.ID, Name: cfg.Name, MaxTaken: 60, Autoplay: true, Replayq: true,
			New: legacyNew{
				Delays: cfg.LearningSteps, InitialFactor: 2500, Ints: [3]int{1, 4, 0},
				Order: 1, PerDay: 20,
			},
			Rev: legacyRev{
				Ease4: 1.3, IvlFct: 1, MaxIvl: cfg.MaximumIntervalDays, PerDay: 200,
				HardFactor: 1.2,
			},
			Lapse: legacyLapse{
				Delays: cfg.RelearningSteps, LeechAction: 1, LeechFails: 8, MinInt: 1,
			},
			FsrsWeights:         []float64{},
			FsrsParams5:         []float64{},
			FsrsParams6:         weights,
			DesiredRetention:    cfg.DesiredRetention,
			EasyDaysPercentages: []float64{1, 1, 1, 1, 1, 1, 1},
			WaitForAudio:        true,
			SM2Retention:        0.9,
		}
	}
	return out
}

// ankiTags renders tags the way schema 11 stores them: a space-separated list with a
// leading and a trailing space. An empty list is the empty string.
func ankiTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return " " + strings.Join(tags, " ") + " "
}

// fieldChecksum approximates Anki's csum column: the first four bytes of the SHA-1 of the
// sort field's text, read as an integer. Anki recomputes it on import, so only the shape
// matters.
func fieldChecksum(text string) int64 {
	sum := sha1.Sum([]byte(text))
	value, err := strconv.ParseInt(hex.EncodeToString(sum[:4]), 16, 64)
	if err != nil {
		return 0
	}
	return value
}

// writeZip writes the archive entries in a fixed order. A media index of "{}" and no
// numbered entries is written when the package carries no media.
func writeZip(w io.Writer, collection []byte, media []WriteMedia) error {
	zw := zip.NewWriter(w)
	write := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("anki package: create entry %q: %w", name, err)
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("anki package: write entry %q: %w", name, err)
		}
		return nil
	}
	if err := write("meta", metaLegacy2); err != nil {
		return err
	}
	if err := write("collection.anki21", collection); err != nil {
		return err
	}
	index := make(map[string]string, len(media))
	for i := range media {
		index[strconv.Itoa(i)] = media[i].Name
	}
	raw, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("anki package: encode media index: %w", err)
	}
	if err := write("media", raw); err != nil {
		return err
	}
	for i := range media {
		if err := write(strconv.Itoa(i), media[i].Bytes); err != nil {
			return err
		}
	}
	return zw.Close()
}

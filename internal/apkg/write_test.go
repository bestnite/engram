package apkg

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/glebarez/sqlite"
)

// writeTestPackage builds one small package with every part Anki reads.
func writeTestPackage() *WritePackage {
	return &WritePackage{
		Collection: WriteCollection{
			CreatedAt:        1600000000,
			ModifiedMS:       1600000000000,
			SchemaModifiedMS: 1600000000000,
			NoteTypes: []WriteNoteType{{
				ID:             1000,
				Name:           "Basic",
				Kind:           KindNormal,
				Modified:       1600000000,
				SortFieldIndex: 0,
				Fields: []Field{
					{Ord: 0, Name: "Front", ID: 0},
					{Ord: 1, Name: "Back", ID: 1},
				},
				Templates: []Template{{
					Ord: 0, Name: "Card 1", ID: 0,
					QFormat: "{{Front}}{{type:Back}}", AFormat: "{{FrontSide}}<hr id=answer>{{Back}}",
				}},
				CSS: ".card { color: black; }",
				AnswerSpecs: map[string]AnswerSpec{
					"Card 1": {Control: "numeric", Bindings: map[string]string{"value": "Back"}},
				},
			}},
			Decks: []WriteDeck{{
				ID: 1, Name: "Deck", Description: "example", ConfigID: 2,
				Modified: 1600000000,
			}},
			DeckConfigs: []WriteDeckConfig{{
				ID: 2, Name: "Default", DesiredRetention: 0.9,
				LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10},
				MaximumIntervalDays: 36500,
				FSRSWeights:         []float64{0.4, 0.6, 2.4, 5.8, 4.93, 0.94, 0.86, 0.01, 1.49, 0.14, 0.94, 2.18, 0.05, 0.34, 1.26, 0.29, 2.61, 0.1, 0.5, 0.2, 0.3},
			}},
			Notes: []WriteNote{{
				ID: 1, GUID: "guid-one", NoteTypeID: 1000, Modified: 1600000000,
				Tags: []string{"geo"}, Fields: []string{"front", "back"}, SortField: "front",
			}},
			Cards: []WriteCard{{
				ID: 1, NoteID: 1, DeckID: 1, Ordinal: 0, Modified: 1600000000, Due: 1,
			}},
			Reviews: []WriteReview{{
				ID: 1600000000000, CardID: 1, Ease: 3, Ivl: 4, LastIvl: 1, Factor: 2500,
				Time: 12000, Type: 1,
			}},
		},
		Media: []WriteMedia{{Name: "pic.png", Bytes: []byte{0x89, 'P', 'N', 'G'}}},
	}
}

func TestWriteArchiveEntriesAndCollection(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteArchive(&buf, writeTestPackage()); err != nil {
		t.Fatalf("WriteArchive: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	names := make([]string, len(zr.File))
	for i, f := range zr.File {
		names[i] = f.Name
	}
	wantNames := []string{"meta", "collection.anki21", "media", "0"}
	if len(names) != len(wantNames) {
		t.Fatalf("entries = %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] {
			t.Fatalf("entries = %v, want %v", names, wantNames)
		}
	}

	meta, err := readEntry(zr.File[0])
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if !bytes.Equal(meta, []byte{0x08, 0x02}) {
		t.Fatalf("meta = % x, want 08 02", meta)
	}
	mediaIndex, err := readEntry(zr.File[2])
	if err != nil {
		t.Fatalf("read media index: %v", err)
	}
	if string(mediaIndex) != `{"0":"pic.png"}` {
		t.Fatalf("media index = %s, want {\"0\":\"pic.png\"}", mediaIndex)
	}
	mediaBytes, err := readEntry(zr.File[3])
	if err != nil {
		t.Fatalf("read media 0: %v", err)
	}
	if !bytes.Equal(mediaBytes, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("media 0 = % x", mediaBytes)
	}

	collection, err := readEntry(zr.File[1])
	if err != nil {
		t.Fatalf("read collection: %v", err)
	}
	path := filepath.Join(t.TempDir(), "collection.sqlite")
	if err := os.WriteFile(path, collection, 0o600); err != nil {
		t.Fatalf("write collection: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open collection: %v", err)
	}
	defer db.Close()

	for _, table := range []string{"col", "notes", "cards", "revlog", "graves"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	noteColumns := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(notes)`)
	if err != nil {
		t.Fatalf("notes columns: %v", err)
	}
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan notes column: %v", err)
		}
		noteColumns[name] = true
	}
	rows.Close()
	for _, column := range []string{"id", "guid", "mid", "mod", "usn", "tags", "flds", "sfld", "csum", "flags", "data"} {
		if !noteColumns[column] {
			t.Errorf("notes column %q missing", column)
		}
	}
	cardColumns := map[string]bool{}
	rows, err = db.Query(`PRAGMA table_info(cards)`)
	if err != nil {
		t.Fatalf("cards columns: %v", err)
	}
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan cards column: %v", err)
		}
		cardColumns[name] = true
	}
	rows.Close()
	for _, column := range []string{"id", "nid", "did", "ord", "type", "queue", "due", "ivl", "factor", "reps", "lapses", "left", "odue", "odid", "flags", "data"} {
		if !cardColumns[column] {
			t.Errorf("cards column %q missing", column)
		}
	}

	var ver int
	var conf, modelsJSON, dconfJSON string
	if err := db.QueryRow(`SELECT ver, conf, models, dconf FROM col`).Scan(&ver, &conf, &modelsJSON, &dconfJSON); err != nil {
		t.Fatalf("read col: %v", err)
	}
	if ver != 11 {
		t.Errorf("col.ver = %d, want 11", ver)
	}
	if conf != "{}" {
		t.Errorf("col.conf = %q, want {}", conf)
	}
	var models map[string]map[string]any
	if err := json.Unmarshal([]byte(modelsJSON), &models); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	model, ok := models["1000"]
	if !ok {
		t.Fatalf("models has no 1000: %v", modelsJSON)
	}
	for _, key := range []string{"id", "name", "type", "mod", "usn", "did", "sortf", "css", "flds", "tmpls"} {
		if _, ok := model[key]; !ok {
			t.Errorf("model key %q missing", key)
		}
	}
	engram, ok := model["engram"].(map[string]any)
	if !ok {
		t.Fatalf("model has no engram key: %v", model)
	}
	specs, ok := engram["answer_specs"].(map[string]any)
	if !ok || len(specs) != 1 {
		t.Fatalf("engram answer_specs = %v, want one spec", engram["answer_specs"])
	}

	var dconf map[string]map[string]any
	if err := json.Unmarshal([]byte(dconfJSON), &dconf); err != nil {
		t.Fatalf("decode dconf: %v", err)
	}
	config, ok := dconf["2"]
	if !ok {
		t.Fatalf("dconf has no entry 2: %s", dconfJSON)
	}
	if _, ok := dconf["1"]; ok {
		t.Fatalf("dconf wrote an entry with id 1")
	}
	if got := len(config["fsrsParams6"].([]any)); got != 21 {
		t.Errorf("fsrsParams6 has %d values, want 21", got)
	}
	if got := len(config["fsrsWeights"].([]any)); got != 0 {
		t.Errorf("fsrsWeights has %d values, want 0", got)
	}
	if got := len(config["fsrsParams5"].([]any)); got != 0 {
		t.Errorf("fsrsParams5 has %d values, want 0", got)
	}

	var flds, guid, tag string
	if err := db.QueryRow(`SELECT guid, tags, flds FROM notes`).Scan(&guid, &tag, &flds); err != nil {
		t.Fatalf("read note: %v", err)
	}
	if guid != "guid-one" || flds != "front\x1fback" || tag != " geo " {
		t.Errorf("note = guid %q tags %q flds %q", guid, tag, flds)
	}
	var queue, due int
	if err := db.QueryRow(`SELECT queue, due FROM cards`).Scan(&queue, &due); err != nil {
		t.Fatalf("read card: %v", err)
	}
	if queue != 0 || due != 1 {
		t.Errorf("card = queue %d due %d, want 0/1", queue, due)
	}
	var cid int64
	if err := db.QueryRow(`SELECT cid FROM revlog`).Scan(&cid); err != nil {
		t.Fatalf("read revlog: %v", err)
	}
	if cid != 1 {
		t.Errorf("revlog cid = %d, want 1", cid)
	}
}

func TestWriteArchiveIsDeterministic(t *testing.T) {
	var first, second bytes.Buffer
	if err := WriteArchive(&first, writeTestPackage()); err != nil {
		t.Fatalf("first WriteArchive: %v", err)
	}
	if err := WriteArchive(&second, writeTestPackage()); err != nil {
		t.Fatalf("second WriteArchive: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("two writes of one package differ: %d vs %d bytes", first.Len(), second.Len())
	}
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

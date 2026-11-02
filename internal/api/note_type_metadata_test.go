package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

func TestMetadataTemplateErrorLocalized(t *testing.T) {
	translator, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tag  language.Tag
		want string
	}{{language.English, "Templates"}, {language.SimplifiedChinese, "模板"}} {
		ctx := i18n.WithLocalizer(context.Background(), translator.Localizer(tc.tag))
		var service *ServiceError
		err := noteTypeWriteError(ctx, &notetype.ValidationError{Rule: notetype.RuleMetadataTemplate})
		if !errors.As(err, &service) || !strings.Contains(service.Message, tc.want) {
			t.Fatalf("%v: %v", tc.tag, err)
		}
	}
}

func TestMetadataFieldIsLockedOnUpdate(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	owner := seedUser(t, env.db, "metadata-owner", store.RoleUser)
	reader := seedUser(t, env.db, "metadata-reader", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatal(err)
	}
	card := seedKindCard(t, env.db, deck.ID, "cloze", `{"text":"{{c1::canonical}}"}`, "cloze:1")
	var note store.Note
	if err := env.db.First(&note, card.NoteID).Error; err != nil {
		t.Fatal(err)
	}
	var nt store.NoteType
	if err := env.db.First(&nt, *note.NoteTypeID).Error; err != nil {
		t.Fatal(err)
	}
	cfg, err := nt.Config()
	if err != nil {
		t.Fatal(err)
	}
	metadataID := ""
	for _, field := range cfg.Fields {
		if field.Name == notetype.MetadataFieldName {
			metadataID = field.ID
		}
	}
	if metadataID == "" {
		t.Fatal("the stock cloze note type has no built-in engram_meta field")
	}
	version := 0
	in := UpdateNoteTypeInput{Name: nt.Name, Config: cfg, Version: &version, DryRun: true}
	if _, err := env.api.UpdateNoteType(ctx, reader.ID, deck.ID, nt.PublicID, nil, in); err == nil {
		t.Fatal("reader update accepted")
	}
	impact, err := env.api.UpdateNoteType(ctx, owner.ID, deck.ID, nt.PublicID, nil, in)
	if err != nil || impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
		t.Fatalf("dry run: %+v %v", impact, err)
	}

	// Removal and rename are both refused before any write, so the stored config keeps the field.
	removed := cfg
	removed.Fields = nil
	for _, field := range cfg.Fields {
		if field.ID != metadataID {
			removed.Fields = append(removed.Fields, field)
		}
	}
	renamed := cfg
	for i := range renamed.Fields {
		if renamed.Fields[i].ID == metadataID {
			renamed.Fields[i].Name = "engram_meta_renamed"
		}
	}
	for name, candidate := range map[string]notetype.Config{"remove": removed, "rename": renamed} {
		in.Config = candidate
		_, err := env.api.UpdateNoteType(ctx, owner.ID, deck.ID, nt.PublicID, nil, in)
		var service *ServiceError
		if !errors.As(err, &service) || service.Code != CodeInvalidRequest || !strings.Contains(service.Message, "built in") {
			t.Errorf("%s: %v, want the catalog-backed locked-field refusal", name, err)
		}
	}
	var stored store.NoteType
	if err := env.db.First(&stored, nt.ID).Error; err != nil {
		t.Fatal(err)
	}
	storedCfg, err := stored.Config()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range storedCfg.Fields {
		if field.ID == metadataID && field.Name == notetype.MetadataFieldName {
			found = true
		}
	}
	if !found {
		t.Error("refused updates changed the stored metadata field")
	}
}

func TestReservedFieldNames(t *testing.T) {
	ctx := context.Background()
	old := notetype.Config{Fields: []notetype.Field{{ID: "old", Name: "engram_notes"}, {ID: "plain", Name: "Text"}}}
	for _, tc := range []struct {
		name   string
		fields []notetype.Field
		valid  bool
	}{
		{"legacy retained", old.Fields, true},
		{"manual metadata", []notetype.Field{{ID: "new", Name: "engram_meta"}}, true},
		{"new reserved", []notetype.Field{{ID: "new", Name: "engram_notes"}}, false},
		{"renamed reserved", []notetype.Field{{ID: "plain", Name: "engram_other"}}, false},
		{"case sensitive", []notetype.Field{{ID: "new", Name: "Engram_other"}}, true},
	} {
		err := validateReservedFields(ctx, notetype.Config{Fields: tc.fields}, old)
		if (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

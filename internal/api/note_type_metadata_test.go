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

func TestMetadataEnableDisableImpactAndPermissions(t *testing.T) {
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
	cfg.Fields = append(cfg.Fields, notetype.Field{ID: "meta-id", Name: "engram_meta"})
	version := 0
	in := UpdateNoteTypeInput{Name: nt.Name, Config: cfg, Version: &version, DryRun: true}
	if _, err := env.api.UpdateNoteType(ctx, reader.ID, deck.ID, nt.PublicID, nil, in); err == nil {
		t.Fatal("reader update accepted")
	}
	impact, err := env.api.UpdateNoteType(ctx, owner.ID, deck.ID, nt.PublicID, nil, in)
	if err != nil || impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
		t.Fatalf("dry run: %+v %v", impact, err)
	}
	in.DryRun = false
	impact, err = env.api.UpdateNoteType(ctx, owner.ID, deck.ID, nt.PublicID, nil, in)
	if err != nil || impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
		t.Fatalf("enable: %+v %v", impact, err)
	}
	if _, err := env.api.UpdateNoteType(ctx, owner.ID, deck.ID, nt.PublicID, nil, in); err == nil {
		t.Fatal("stale update accepted")
	}
	version = 1
	cfg.Fields = cfg.Fields[:len(cfg.Fields)-1]
	in.Config = cfg
	impact, err = env.api.UpdateNoteType(ctx, owner.ID, deck.ID, nt.PublicID, nil, in)
	if err != nil || impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
		t.Fatalf("remove: %+v %v", impact, err)
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

package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestReviewRenderTypeInFrontAndBack pins a type-in template without an answer spec: the frame front draws input#typeans
// in the field's own font and size, and the answer side replaces the marker with the comparison Anki's oracle recorded
// for the submitted value. The golden is the committed type-in fixture, so the output is Anki's, not a second rule.
func TestReviewRenderTypeInFrontAndBack(t *testing.T) {
	golden := loadTypeAnswerGolden(t)
	entries := map[string]struct {
		expected  string
		provided  string
		combining bool
		output    string
	}{}
	for _, entry := range golden.CompareAnswers {
		if entry.NotetypeName != "Type Basic" {
			continue
		}
		entries[entry.Provided] = struct {
			expected  string
			provided  string
			combining bool
			output    string
		}{entry.Expected, entry.Provided, entry.Combining, entry.Output}
	}
	exact, ok := entries["42"]
	if !ok {
		t.Fatal("type-in golden holds no entry for the provided value \"42\"")
	}

	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Type-in deck")
	note := seedTypeInNote(t, db, deck, "{{type:Back}}", exact.expected, "Courier New", 24)

	render := func(answer *string, forFrame bool) typeInRenderBody {
		t.Helper()
		body := map[string]any{"card_id": cardPublicIDOfNote(t, db, note.ID), "deck": []string{deck.PublicID}}
		if answer != nil {
			body["answer"] = *answer
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/review/render", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.CSRFHeaderName, csrf)
		if forFrame {
			req.Header.Set(frameOutputHeader, "1")
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
		}
		var out typeInRenderBody
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode render response: %v (body %s)", err, rec.Body.String())
		}
		return out
	}

	typed := exact.provided
	frame := render(&typed, true)
	if !frame.TypeIn {
		t.Error("type_in = false, want true for a type-in template without an answer spec")
	}
	if !strings.Contains(frame.Front.HTML, `id="typeans"`) {
		t.Errorf("frame front has no type-in input: %s", frame.Front.HTML)
	}
	if !strings.Contains(frame.Front.HTML, `style="font-family:Courier New;font-size:24px"`) {
		t.Errorf("frame front input is not in the field's font and size: %s", frame.Front.HTML)
	}
	if strings.Contains(frame.Front.HTML, "[[type:") || strings.Contains(frame.Back.HTML, "[[type:") {
		t.Errorf("frame faces kept a type marker: front=%s back=%s", frame.Front.HTML, frame.Back.HTML)
	}
	if !strings.Contains(frame.Back.HTML, exact.output) {
		t.Errorf("frame back does not match the golden comparison:\n got %s\nwant %s", frame.Back.HTML, exact.output)
	}

	// Without a typed value the answer side still shows the expected text, which is the comparison's empty-input form.
	empty := render(nil, true)
	if !strings.Contains(empty.Back.HTML, notetype.CompareAnswer(exact.expected, "", exact.combining)) {
		t.Errorf("answer side without a typed value = %s, want the expected text", empty.Back.HTML)
	}

	// Every request that is not frame output keeps the transitional behaviour: no input, and no marker left behind.
	plain := render(&typed, false)
	if strings.Contains(plain.Front.HTML, `id="typeans"`) || strings.Contains(plain.Front.HTML, "[[type:") {
		t.Errorf("non-frame front = %q, want the marker stripped and no input", plain.Front.HTML)
	}
}

// TestReviewRenderTypeInComparisonVariants covers the non-combining placeholder and a partial match, because the variant
// decides the comparison rule and the submitted value decides the marks; both results must still be Anki's.
func TestReviewRenderTypeInComparisonVariants(t *testing.T) {
	golden := loadTypeAnswerGolden(t)
	for _, entry := range golden.CompareAnswers {
		if entry.NotetypeName != "Type Nc" || entry.Combining {
			continue
		}
		entry := entry
		t.Run(entry.Provided, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "Type-in nc deck")
			note := seedTypeInNote(t, db, deck, "{{type-nc:Back}}", entry.Expected, "Arial", 20)

			payload, err := json.Marshal(map[string]any{
				"card_id": cardPublicIDOfNote(t, db, note.ID),
				"deck":    []string{deck.PublicID},
				"answer":  entry.Provided,
			})
			if err != nil {
				t.Fatalf("encode request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/review/render", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(auth.CSRFHeaderName, csrf)
			req.Header.Set(frameOutputHeader, "1")
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			var out typeInRenderBody
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode render response: %v", err)
			}
			if !strings.Contains(out.Back.HTML, entry.Output) {
				t.Errorf("type-nc back does not match the golden comparison:\n got %s\nwant %s", out.Back.HTML, entry.Output)
			}
		})
	}
}

// typeInRenderBody is the subset of the render response these cases read.
type typeInRenderBody struct {
	Front struct {
		HTML string `json:"html"`
	} `json:"front"`
	Back struct {
		HTML string `json:"html"`
	} `json:"back"`
	TypeIn bool `json:"type_in"`
}

// typeAnswerGolden is the part of the type-in oracle fixture these cases read.
type typeAnswerGolden struct {
	CompareAnswers []struct {
		NotetypeName   string `json:"notetype_name"`
		Expected       string `json:"expected"`
		ExpectedSource string `json:"expected_source"`
		Provided       string `json:"provided"`
		Combining      bool   `json:"combining"`
		Output         string `json:"output"`
	} `json:"compare_answers"`
}

// loadTypeAnswerGolden reads the committed type-in oracle fixture.
func loadTypeAnswerGolden(t *testing.T) typeAnswerGolden {
	t.Helper()
	raw, err := os.ReadFile("../notetype/testdata/anki/typeanswer.golden.json")
	if err != nil {
		t.Fatalf("read type-in golden: %v", err)
	}
	var golden typeAnswerGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode type-in golden: %v", err)
	}
	return golden
}

// seedTypeInNote stores a note type whose template carries a type placeholder and no answer spec, plus one note whose
// Back field is the expected text. The marker is the template fragment under test ([[type:Back]] or [[type-nc:Back]]).
func seedTypeInNote(t *testing.T, db *gorm.DB, deck *store.Deck, placeholder, back, font string, fontSize int) *store.Note {
	t.Helper()
	cfg := notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back", Font: font, FontSize: fontSize},
		},
		Templates: []notetype.Template{{
			ID:      "t-typein",
			Name:    "Card 1",
			QFormat: "{{Front}}\n\n" + placeholder,
			AFormat: "{{FrontSide}}\n\n<hr id=answer>\n\n" + placeholder,
		}},
	}
	noteType := &store.NoteType{DeckID: deck.ID, Name: "Typed answer"}
	if err := store.NewNoteTypeStore(db).Create(t.Context(), noteType, cfg); err != nil {
		t.Fatalf("create type-in note type: %v", err)
	}
	note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(noteType.ID)}
	if _, err := store.NewNoteStore(db).Create(t.Context(), note, map[string]any{"Front": "Question", "Back": back}); err != nil {
		t.Fatalf("create type-in note: %v", err)
	}
	return note
}

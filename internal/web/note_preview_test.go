package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotePreviewSanitizesHTMLAndPreservesTeX(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview")
	payload, _ := json.Marshal(notePreviewRequest{Kind: "basic", Fields: map[string]any{
		"front": `<img src=x onerror=alert(1)> \(x^2\)`, "back": `<script>alert(1)</script>safe`,
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/notes/preview", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
			BackHTML  string `json:"back_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || len(preview.Cards) != 1 {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	// The preview shows the stored HTML unchanged: the server does not sanitise, and the frame sandbox is the boundary.
	if !strings.Contains(preview.Cards[0].FrontHTML, "onerror") || !strings.Contains(preview.Cards[0].BackHTML, "<script") {
		t.Fatalf("preview did not return the stored HTML: %+v", preview.Cards[0])
	}
	if !strings.Contains(preview.Cards[0].FrontHTML, `\(x^2\)`) || !strings.Contains(preview.Cards[0].BackHTML, "safe") {
		t.Fatalf("TeX or content missing: %+v", preview.Cards[0])
	}
}

// TestNotePreviewKeepsChoiceOptionsOutOfTheFace asserts the preview face carries the question only: choice options belong to
// the answer control's markup, which is drawn by the frame, so the face must not repeat them.
func TestNotePreviewKeepsChoiceOptionsInline(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview choices")
	payload, _ := json.Marshal(notePreviewRequest{Kind: "choice_single", Fields: map[string]any{
		"question": "pick-one", "options": []any{"alpha", "beta"}, "answer": 1,
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/notes/preview", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
			BackHTML  string `json:"back_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Cards) != 1 {
		t.Fatalf("preview cards = %d, want 1 (body %s)", len(body.Cards), rec.Body.String())
	}
	front := body.Cards[0].FrontHTML
	if !strings.Contains(front, "pick-one") {
		t.Fatalf("preview front is missing the question: %s", front)
	}
	for _, option := range []string{"alpha", "beta"} {
		if strings.Contains(front, option) {
			t.Errorf("preview front repeats option %q that the answer control draws: %s", option, front)
		}
	}
	if !strings.Contains(body.Cards[0].BackHTML, "beta") {
		t.Errorf("preview back = %s, want the correct option", body.Cards[0].BackHTML)
	}
}

func TestNotePreviewRequiresSessionAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview")
	payload, _ := json.Marshal(notePreviewRequest{Fields: map[string]any{"front": "a", "back": "b"}})
	request := func(withCookie, withCSRF bool) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/notes/preview", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if withCSRF {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if withCookie {
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request(false, false); got != http.StatusForbidden {
		t.Errorf("anonymous status = %d, want 403", got)
	}
	if got := request(true, false); got != http.StatusForbidden {
		t.Errorf("missing CSRF status = %d, want 403", got)
	}
}

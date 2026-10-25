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
	body := rec.Body.String()
	if strings.Contains(body, "onerror") || strings.Contains(body, "<script") || strings.Contains(body, "javascript:") {
		t.Fatalf("unsafe rendered HTML: %s", body)
	}
	if !strings.Contains(body, `\\(x^2\\)`) || !strings.Contains(body, "safe") {
		t.Fatalf("TeX or safe content missing: %s", body)
	}
}

// TestNotePreviewKeepsChoiceOptionsInline asserts the preview keeps showing a choice card's options on the front: the preview has no
// answer controls, so appending Options to the face is the only path that puts them on the page.
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
	alpha, beta := strings.Index(front, "alpha"), strings.Index(front, "beta")
	if alpha < 0 || beta < 0 {
		t.Fatalf("preview front is missing an option: %s", front)
	}
	if alpha > beta {
		t.Errorf("preview front lists the options out of stored order: %s", front)
	}
	if !strings.Contains(body.Cards[0].BackHTML, "beta") {
		t.Errorf("preview back = %s, want the correct option", body.Cards[0].BackHTML)
	}
}

func TestNotePreviewRequiresSessionAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview")
	payload, _ := json.Marshal(notePreviewRequest{Kind: "basic", Fields: map[string]any{"front": "a", "back": "b"}})
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

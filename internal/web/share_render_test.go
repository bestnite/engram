package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Sanitized rendering of share content now travels as JSON: GET /api/v1/share/:token returns server-sanitized
// front_html/back_html (shareNoteView → renderSide), and the SPA only hands that HTML to {@html}.
// This file locks down two things: legal Markdown/Math appears as tags; and the same card renders byte-identical body HTML
// on the share and the review path (proving both go through the same internal/render pipeline).

// fetchShareHTML opens a no-password share link and fetches the front/back HTML of the first card.
func fetchShareHTML(t *testing.T, srv *Server, deckPublicID string, ownerCookies []*http.Cookie, ownerCSRF string) (front, back string) {
	t.Helper()
	token := createShareLinkJSON(t, srv, deckPublicID, ownerCookies, ownerCSRF, "")
	rec := get(t, srv, "/api/v1/share/"+token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/share/<token> = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body shareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode share content: %v", err)
	}
	if len(body.Notes) == 0 {
		t.Fatalf("share content has no notes: %s", snippet(rec.Body.String()))
	}
	return body.Notes[0].FrontHTML, body.Notes[0].BackHTML
}

// TestShareBrowseRendersSanitizedCardHTML locks this rule down: share content injects card text as already-sanitized HTML,
// so the user sees <span> rather than &lt;span&gt;. It reuses the same internal/render.RenderMarkdown as the review page.
func TestShareBrowseRendersSanitizedCardHTML(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Render deck")
	front := `<span class="hl">高亮</span> 与 **加粗** 与 \(x+y\)`
	seedBasic(t, db, deck.ID, front, "背面")

	frontHTML, _ := fetchShareHTML(t, srv, deck.PublicID, ownerCookies, ownerCSRF)

	// Legal markup must appear as tags.
	for _, want := range []string{`<span class="hl">高亮</span>`, `<strong>加粗</strong>`, `\(x+y\)`} {
		if !strings.Contains(frontHTML, want) {
			t.Errorf("shared card body missing rendered markup %q:\n%s", want, frontHTML)
		}
	}
	// And escaped tags or unconverted Markdown must never appear again.
	for _, bad := range []string{"&lt;span", "&lt;strong", "&quot;hl&quot;", "**加粗**"} {
		if strings.Contains(frontHTML, bad) {
			t.Errorf("shared card body still shows escaped/raw source %q:\n%s", bad, frontHTML)
		}
	}
}

// TestShareBrowseSanitizesUnsafeCardHTML is the end-to-end security negative case:
// with scripts, event attributes and a math-bypass sample placed in the same card's fields, neither the share nor the review path may show an executable form,
// and the body HTML the two paths render is exactly identical.
//
// Sanitizing itself is covered exhaustively by the internal/render tests:
//   - TestRenderMarkdownStripsUnsafe (script / onerror / javascript: URL)
//   - TestRenderMarkdownNeutralizesMathAttributeEscape (attribute escape after math restoration)
func TestShareBrowseSanitizesUnsafeCardHTML(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Unsafe deck")
	front := strings.Join([]string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<span class="\(a"onmouseover="alert(2)//\)">xss-span</span>`,
		`[x](\(javascript:alert(3)\))`,
	}, "\n\n")
	back := `**背面** 与 \(a<b\)`
	note := seedBasic(t, db, deck.ID, front, back)

	shareFront, shareBack := fetchShareHTML(t, srv, deck.PublicID, ownerCookies, ownerCSRF)

	renderRec := postJSONWithCSRF(t, srv, "/api/v1/review/render",
		map[string]any{"card_id": cardPublicIDOfNote(t, db, note.ID), "deck": []string{deck.PublicID}}, ownerCookies, ownerCSRF)
	if renderRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/review/render = %d, want 200 (body %s)", renderRec.Code, snippet(renderRec.Body.String()))
	}
	var renderBody struct {
		FrontHTML string `json:"front_html"`
		BackHTML  string `json:"back_html"`
	}
	if err := json.Unmarshal(renderRec.Body.Bytes(), &renderBody); err != nil {
		t.Fatalf("decode render response: %v", err)
	}

	for name, frag := range map[string]string{
		"share front": shareFront, "share back": shareBack,
		"review front": renderBody.FrontHTML, "review back": renderBody.BackHTML,
	} {
		for _, bad := range []string{"<script", "onerror", "onmouseover", "onclick", "javascript:", "alert("} {
			if strings.Contains(frag, bad) {
				t.Errorf("%s still contains executable form %q:\n%s", name, bad, frag)
			}
		}
	}
	// The content must not be swallowed whole: both legal leftovers and text have to be there.
	for _, want := range []string{"xss-span", `<img src="x"`} {
		if !strings.Contains(shareFront, want) {
			t.Errorf("share front lost benign content %q:\n%s", want, shareFront)
		}
	}
	// Consistency: for the same note, the body HTML from share and from review is byte-identical.
	if shareFront != renderBody.FrontHTML {
		t.Errorf("share/review front HTML differ:\nshare=%q\nreview=%q", shareFront, renderBody.FrontHTML)
	}
	if shareBack != renderBody.BackHTML {
		t.Errorf("share/review back HTML differ:\nshare=%q\nreview=%q", shareBack, renderBody.BackHTML)
	}
}

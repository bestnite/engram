package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// reviewFaceAV is the part of a rendered face this test reads.
type reviewFaceAV struct {
	HTML     string           `json:"html"`
	AVTags   []notetype.AVTag `json:"av_tags"`
	Autoplay bool             `json:"autoplay"`
}

// TestReviewRenderExtractsFaceAudio pins the server side of the playback model: a frame request gets play markers and
// the tag list per face, the answer face carries the question's tags (the {{FrontSide}} markers stay in its HTML) but
// its own tags are listed separately by side, and autoplay comes from the deck column.
func TestReviewRenderExtractsFaceAudio(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Audio deck")
	// The stock basic template answers with {{FrontSide}}, so the front sound appears in the answer HTML too. Only the
	// back field's sound is the answer's own tag.
	note := seedBasic(t, db, deck.ID, "listen [sound:front.mp3]", "answer [sound:back.mp3]")
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	rec := postJSON(srv, "/api/v1/review/render", map[string]any{
		"card_id": cardPub,
		"deck":    []string{deck.PublicID},
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body struct {
		Front reviewFaceAV `json:"front"`
		Back  reviewFaceAV `json:"back"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode render response: %v", err)
	}

	if !strings.Contains(body.Front.HTML, "[anki:play:q:0]") {
		t.Errorf("front html = %q, want a q marker", body.Front.HTML)
	}
	if len(body.Front.AVTags) != 1 || body.Front.AVTags[0].Side != "q" || body.Front.AVTags[0].Name != "front.mp3" {
		t.Errorf("front av_tags = %+v, want the one question sound", body.Front.AVTags)
	}

	// The answer template of this stock type does not use {{FrontSide}}, so the answer adds exactly one tag. The
	// FrontSide exclusion itself is pinned by TestRenderAnswerExcludesFrontSideAudio in internal/notetype.
	if !strings.Contains(body.Back.HTML, "[anki:play:a:0]") {
		t.Errorf("back html = %q, want the answer's a marker", body.Back.HTML)
	}
	if len(body.Back.AVTags) != 1 || body.Back.AVTags[0].Side != "a" || body.Back.AVTags[0].Name != "back.mp3" {
		t.Errorf("back av_tags = %+v, want only the answer's own sound", body.Back.AVTags)
	}

	if !body.Front.Autoplay || !body.Back.Autoplay {
		t.Errorf("face autoplay = %v/%v, want the deck default true", body.Front.Autoplay, body.Back.Autoplay)
	}
	if err := store.NewDeckStore(db).SetAutoplay(context.Background(), ownerID, deck.ID, false); err != nil {
		t.Fatalf("SetAutoplay: %v", err)
	}
	rec = postJSON(srv, "/api/v1/review/render", map[string]any{
		"card_id": cardPub,
		"deck":    []string{deck.PublicID},
	}, cookies, map[string]string{auth.CSRFHeaderName: csrf, frameOutputHeader: "1"})
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode second render response: %v", err)
	}
	if body.Front.Autoplay || body.Back.Autoplay {
		t.Errorf("face autoplay after the deck setting = %v/%v, want false", body.Front.Autoplay, body.Back.Autoplay)
	}
}

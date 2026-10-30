package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

type notePreviewRequest struct {
	NoteType string         `json:"note_type"`
	Fields   map[string]any `json:"fields"`
}

// noteTypePreviewRequest is the editor's unsaved note type and the template whose two faces it wants.
type noteTypePreviewRequest struct {
	Config     *notetype.Config `json:"config"`
	TemplateID string           `json:"template_id"`
}

// previewNoteTypeName is the name the unsaved config is validated under.
//
// SetConfig is the one validation path every note type write runs, and it validates a name before the config.
// The preview body carries no name, so this placeholder satisfies that rule; it is never stored.
const previewNoteTypeName = "preview"

// notePreview returns the card faces the submitted payload would produce.
func (s *Server) notePreview(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	// The path parameter is the public id; deck primary keys are not public, so swap in the numeric primary key before checking permissions.
	deck, err := s.decks.ByPublicID(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deck.ID, store.RoleEditor); !ok {
		return
	}
	var req notePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Fields == nil || strings.TrimSpace(req.NoteType) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	// The preview renders the note type the caller names, not a stock definition derived from a kind. A type of another deck
	// is reported as not found, so the endpoint never confirms ids outside the deck in the address.
	nt, err := store.NewNoteTypeStore(s.db).ByPublicID(c.Request.Context(), strings.TrimSpace(req.NoteType))
	if err != nil || nt.DeckID != deck.ID {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return
	}
	cfg, err := nt.Config()
	if err != nil {
		s.logger.Error("decode note type config failed", "note_type_id", nt.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	cards, ok := buildPreview(nt, cfg, deck, req.Fields)
	if !ok || len(cards) == 0 {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	// Preview renders the same card faces as the review path. The base travels on every response; the relative media
	// names are handed out only when the caller declares the HTML is for the frame, because without the frame's <base> a
	// relative name would resolve against the SPA page.
	mediaBase := s.frameMediaBase(c.Request.Context(), user.ID, deck.ID)
	forFrame := frameOutputRequested(c) && mediaBase != ""
	result := make([]gin.H, 0, len(cards))
	for _, card := range cards {
		front, back := card.Front, card.Back
		front.MediaBase, back.MediaBase = mediaBase, mediaBase
		if !forFrame {
			front.HTML = absoluteMediaRefs(front.HTML)
			back.HTML = absoluteMediaRefs(back.HTML)
		}
		result = append(result, gin.H{
			"front":      front,
			"back":       back,
			"front_html": front.HTML,
			"back_html":  back.HTML,
		})
	}
	c.JSON(http.StatusOK, gin.H{"cards": result, "media_base": mediaBase})
}

// noteTypePreview renders one template of an unsaved note type config.
//
// The note type editor shows what a card will look like while the config is still a draft, so the preview cannot
// read a stored type. The body carries the whole config, and the response is the same front/back card-face shape
// the review and note preview paths return, rendered by the server's own renderer instead of a second frontend one.
func (s *Server) noteTypePreview(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	// The path parameter is the public id; deck primary keys are not public, so swap in the numeric primary key before checking permissions.
	deck, err := s.decks.ByPublicID(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deck.ID, store.RoleEditor); !ok {
		return
	}
	var req noteTypePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Config == nil || strings.TrimSpace(req.TemplateID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	cfg := *req.Config
	// An unsaved config is validated with the rule a stored note type runs: SetConfig is the one validation path
	// shared by every write. The store is never touched, so a rejected draft leaves no row behind.
	var probe store.NoteType
	if err := probe.SetConfig(previewNoteTypeName, cfg); err != nil {
		writeRenderErrorDetail(c, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	tmpl, ok := previewTemplate(cfg, req.TemplateID)
	if !ok {
		writeRenderErrorDetail(c, http.StatusBadRequest, api.CodeInvalidRequest,
			fmt.Sprintf("template %q is not declared by the note type", req.TemplateID))
		return
	}
	deckName, subdeckName := deckFieldNames(deck.Name, nil)
	data := notetype.RenderData{
		Fields:      previewFieldValues(cfg),
		DeckName:    deckName,
		SubdeckName: subdeckName,
		CardName:    tmpl.Name,
	}
	frontHTML, err := notetype.Render(cfg, tmpl, notetype.SideFront, data)
	if err != nil {
		writeRenderErrorDetail(c, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	backHTML, err := notetype.Render(cfg, tmpl, notetype.SideBack, data)
	if err != nil {
		writeRenderErrorDetail(c, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	// The faces follow the same media rule as the note preview: the base travels on every response, and the relative
	// names are handed out only when the caller declares the HTML is for the frame.
	mediaBase := s.frameMediaBase(c.Request.Context(), user.ID, deck.ID)
	if !frameOutputRequested(c) || mediaBase == "" {
		frontHTML = absoluteMediaRefs(frontHTML)
		backHTML = absoluteMediaRefs(backHTML)
	}
	c.JSON(http.StatusOK, gin.H{
		"front": CardFace{
			HTML: frontHTML, CSS: cfg.CSS, BodyClass: "card", Side: notetype.SideFront.String(), MediaBase: mediaBase,
		},
		"back": CardFace{
			HTML: backHTML, CSS: cfg.CSS, BodyClass: "card", Side: notetype.SideBack.String(), MediaBase: mediaBase,
		},
		"media_base": mediaBase,
	})
}

// previewTemplate finds the template the editor selected by its stable id.
//
// The editor sends its own template id, which a cloze note type also carries; CardTemplate expects a card-template
// name ("cloze:1"), so the lookup walks the config instead.
func previewTemplate(cfg notetype.Config, templateID string) (notetype.Template, bool) {
	for _, tmpl := range cfg.Templates {
		if tmpl.ID == templateID {
			return tmpl, true
		}
	}
	return notetype.Template{}, false
}

// previewFieldValues maps each field id to the text the preview renders.
//
// Every field is shown as its own name, so the layout is visible without inventing card content. On a cloze note type
// the name is wrapped in a cloze deletion: the cloze filter emits its blank markup only for a value that carries a
// deletion, and a preview without one would show an empty side.
func previewFieldValues(cfg notetype.Config) map[string]string {
	values := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		value := field.Name
		if cfg.Kind == notetype.KindCloze {
			value = "{{c1::" + field.Name + "}}"
		}
		values[field.ID] = value
	}
	return values
}

func writeRenderError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code": code, "message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// writeRenderErrorDetail writes the stable error envelope with the rule's own English detail appended, so a rejected
// note type config names the template or field that failed instead of only the generic request error.
func writeRenderErrorDetail(c *gin.Context, status int, code, detail string) {
	message := api.ErrorMessage(c.Request.Context(), code)
	if detail = strings.TrimSpace(detail); detail != "" {
		message += " — " + detail
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

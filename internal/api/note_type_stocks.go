package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

// The source names a stock definition id starts with. A stock id is "source:kind", because the three
// definition lists name their kinds independently; a bare kind would be ambiguous the moment two
// sources name the same one. The template source holds the common templates of
// notetype.TemplateStockConfigs, and an anki id is listed only where engram has no definition for the
// same study shape.
const (
	stockSourceAnki     = "anki"
	stockSourceTemplate = "template"
	stockSourceEngram   = "engram"
)

// NoteTypeStockJSON is one stock note type a deck can create from. The config itself is not
// part of the response: a caller creates from the id, so an updated stock definition reaches
// every client without a release of its own, and the frontend never carries a copied config.
// LabelKey names the translation catalog entry for the display name; Name is the canonical
// English name, which is also the note type name written when the caller sends none.
type NoteTypeStockJSON struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	LabelKey string `json:"label_key"`
}

// NoteTypeStocksResponse is the response body of the stock definition list.
type NoteTypeStocksResponse struct {
	Stocks []NoteTypeStockJSON `json:"stocks"`
}

// stockNoteTypeDefinition pairs the public shape with the config it creates.
type stockNoteTypeDefinition struct {
	JSON   NoteTypeStockJSON
	Config notetype.Config
}

// stocksEngramReplaces lists the Anki stock kinds the picker does not offer, because engram's own
// definition covers the same study shape and listing both would show two rows with the same label and
// no way to choose between them. The same shapes are: Anki's basic and engram's basic, "and reversed"
// and "both directions", "type in the answer" and "typed answer", and the two cloze definitions.
// engram's definition is the one offered because it carries the answer controls and the metadata
// field; Anki's definition stays in notetype, where an imported package still names it.
var stocksEngramReplaces = map[notetype.OriginalStockKind]struct{}{
	notetype.StockBasic:            {},
	notetype.StockBasicAndReversed: {},
	notetype.StockBasicTyping:      {},
	notetype.StockCloze:            {},
}

// stockNoteTypeDefinitions returns the definitions a deck can be created from: engram's ten question
// types, then the common templates, then the two Anki definitions engram has no equivalent for, each
// group in its fixed declaration order so the picker shows the same list on every call. engram's
// definitions come first because the client preselects the first entry, and they are what a new deck
// should start from.
func stockNoteTypeDefinitions() []stockNoteTypeDefinition {
	templates := notetype.TemplateStockConfigs()
	out := make([]stockNoteTypeDefinition, 0, 10+len(templates)+2)
	for _, stock := range notetype.EngramStockConfigs() {
		out = append(out, stockNoteTypeDefinition{
			JSON: NoteTypeStockJSON{
				ID:       stockSourceEngram + ":" + stock.Kind,
				Source:   stockSourceEngram,
				Name:     stock.Name,
				LabelKey: "notetype.stock.engram." + stock.Kind,
			},
			Config: stock.Config,
		})
	}
	for _, stock := range templates {
		out = append(out, stockNoteTypeDefinition{
			JSON: NoteTypeStockJSON{
				ID:       stockSourceTemplate + ":" + stock.Kind,
				Source:   stockSourceTemplate,
				Name:     stock.Name,
				LabelKey: "notetype.stock.template." + stock.Kind,
			},
			Config: stock.Config,
		})
	}
	for _, cfg := range notetype.StockConfigs() {
		if cfg.OriginalStockKind == nil {
			continue
		}
		kind := *cfg.OriginalStockKind
		if _, replaced := stocksEngramReplaces[kind]; replaced {
			continue
		}
		out = append(out, stockNoteTypeDefinition{
			JSON: NoteTypeStockJSON{
				ID:       stockSourceAnki + ":" + string(kind),
				Source:   stockSourceAnki,
				Name:     notetype.StockName(kind),
				LabelKey: "notetype.stock.anki." + string(kind),
			},
			Config: cfg,
		})
	}
	return out
}

// stockNoteTypeByID resolves one stock id. The second result is false for an unknown id.
func stockNoteTypeByID(id string) (stockNoteTypeDefinition, bool) {
	for _, def := range stockNoteTypeDefinitions() {
		if def.JSON.ID == id {
			return def, true
		}
	}
	return stockNoteTypeDefinition{}, false
}

// NoteTypeStocks lists the stock definitions a deck's note type can be created from. It is
// static metadata, so it needs the read scope but no role on any deck.
func (a *API) NoteTypeStocks() NoteTypeStocksResponse {
	defs := stockNoteTypeDefinitions()
	out := make([]NoteTypeStockJSON, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.JSON)
	}
	return NoteTypeStocksResponse{Stocks: out}
}

// noteTypeStocks is GET /api/v1/note-type-stocks, scope: read.
func (a *API) noteTypeStocks(c *gin.Context) {
	c.JSON(http.StatusOK, a.NoteTypeStocks())
}

// AnswerControlRoleJSON is one role an answer control reads. Kind is the parse the role uses
// ("text", "lines", "bool" or "number"), and Required says whether the answer spec must bind it.
// LabelKey names the translation catalog entry for the role's display name, so the editor never
// shows the registry identifier.
type AnswerControlRoleJSON struct {
	Name     string `json:"name"`
	LabelKey string `json:"label_key"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

// AnswerControlJSON is one registered answer control and the roles it declares.
// LabelKey names the translation catalog entry for the display name, so the editor
// never shows the registry identifier.
type AnswerControlJSON struct {
	Name     string                  `json:"name"`
	LabelKey string                  `json:"label_key"`
	Roles    []AnswerControlRoleJSON `json:"roles"`
}

// AnswerControlsResponse is the response body of the answer-control registry list.
type AnswerControlsResponse struct {
	Controls []AnswerControlJSON `json:"controls"`
}

// AnswerControls describes every registered answer control, so the note type editor builds
// its control and role pickers from the registry instead of a second hard-coded list.
func (a *API) AnswerControls() AnswerControlsResponse {
	names := answer.Names()
	out := make([]AnswerControlJSON, 0, len(names))
	for _, name := range names {
		control, ok := answer.Lookup(name)
		if !ok {
			continue
		}
		declared, ok := control.(answer.Roles)
		if !ok {
			continue
		}
		roles := make([]AnswerControlRoleJSON, 0, len(declared.Roles()))
		for _, role := range declared.Roles() {
			roles = append(roles, AnswerControlRoleJSON{
				Name:     role.Name,
				LabelKey: "notetype.answer.role." + role.Name,
				Kind:     answerRoleKindName(role.Kind),
				Required: role.Required,
			})
		}
		out = append(out, AnswerControlJSON{Name: name, LabelKey: "notetype.answer.control." + name, Roles: roles})
	}
	return AnswerControlsResponse{Controls: out}
}

// answerRoleKindName renders answer.Kind as the stable identifier the frontend reads.
func answerRoleKindName(kind answer.Kind) string {
	switch kind {
	case answer.KindLines:
		return "lines"
	case answer.KindBool:
		return "bool"
	case answer.KindNumber:
		return "number"
	default:
		return "text"
	}
}

// answerControls is GET /api/v1/answer-controls, scope: read.
func (a *API) answerControls(c *gin.Context) {
	c.JSON(http.StatusOK, a.AnswerControls())
}

// stockNoteTypeName trims the caller's name and falls back to the stock definition's canonical
// name. It exists so the create path and its tests agree on what an empty name means.
func stockNoteTypeName(input, fallback string) string {
	if strings.TrimSpace(input) != "" {
		return input
	}
	return fallback
}

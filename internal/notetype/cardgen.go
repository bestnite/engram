// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/notetype/cardgen.rs at tag 26.09.3 of ankitects/anki,
// together with the empty-card detection from
// rslib/src/notetype/emptycards.rs at the same tag.
//
// The port covers the two decisions this repository needs before it has a
// collection: which cards a note requires, and which of a note's stored cards
// have become empty. Everything that writes cards is left out, because it
// belongs to the note write path: the deck a card is placed in, its due
// position, the forced first card, and the bulk walk over a whole note type.

package notetype

import (
	"fmt"
	"strconv"
)

// GeneratedCard identifies one card a note should have. Template is the
// template id for a normal note type, and "cloze:<n>" for a cloze note type,
// where n is the cloze number the card belongs to. Ordinal is the 0-based card
// ordinal: the template position for a normal note type, and the cloze number
// minus one for a cloze note type.
type GeneratedCard struct {
	Template string
	Ordinal  int
}

// ExistingCard is one card already stored for the note. Template is the value
// the caller records for the card's template and is only reported back;
// Ordinal is what decides whether the card is still wanted, because the
// ordinal, not the template id, is a card's identity within a note.
type ExistingCard struct {
	Template string
	Ordinal  int
}

// clozeTemplatePrefix marks the Template value of a cloze card.
const clozeTemplatePrefix = "cloze:"

// maxClozeCardOrdinal is the highest card ordinal a cloze number can map to.
// Anki clamps the ordinal this way, so numbers from 500 upwards share the last
// card ordinal.
const maxClozeCardOrdinal = 499

// GenerateCards returns the cards a note should have.
//
// A normal note type generates one card per template whose front can render
// with the note's non-empty fields. Literal template text alone does not make a
// card: the question is whether the front references a field that holds
// content, which is the rule Anki applies when it adds a note.
//
// A cloze note type generates one card per cloze number found in any of the
// note's fields, in ascending order. A field that no template renders still
// contributes its numbers, because the marker decides which numbers exist and
// the template only decides how they are displayed.
//
// A template that does not parse, or that references an unknown field, makes
// the call fail; the returned error names the template. Anki treats such a
// template as generating no card and says nothing, but this repository reports
// the template error to the user, so the caller gets it instead.
func GenerateCards(cfg Config, data RenderData) ([]GeneratedCard, error) {
	switch cfg.Kind {
	case KindCloze:
		return generateClozeCards(cfg, data), nil
	case KindNormal:
		return generateNormalCards(cfg, data)
	default:
		return nil, fmt.Errorf("notetype: card generation: unknown note type kind %q", string(cfg.Kind))
	}
}

// generateNormalCards returns one card per template whose front references a
// field that holds content. That is Anki's rule, not a test of the rendered
// text: a front that is nothing but literal HTML still yields no card.
//
// The front is rendered as well, so a template that cannot be rendered - an
// unknown field, a conditional that never closes - fails the call, and the
// caller can show the template error. The emptiness test does not depend on
// that rendering; it reads the same non-empty fields the card will show, which
// is what FrontProblem does, so a card that is generated never has a blank
// front.
//
// Anki counts every special field except FrontSide as non-empty without looking
// at its value; this port reads them from the render data instead, because that
// is what the rendered card shows. The two agree for every special field a
// caller can fill, since a card with an empty deck, type or flag name has no
// meaning.
func generateNormalCards(cfg Config, data RenderData) ([]GeneratedCard, error) {
	cards := make([]GeneratedCard, 0, len(cfg.Templates))
	for ordinal, tmpl := range cfg.Templates {
		if _, err := Render(cfg, tmpl, SideFront, data); err != nil {
			return nil, fmt.Errorf("template %q (id %q): %w", tmpl.Name, tmpl.ID, err)
		}
		if FrontIsEmpty(cfg, tmpl, data) {
			continue
		}
		cards = append(cards, GeneratedCard{Template: tmpl.ID, Ordinal: ordinal})
	}
	return cards, nil
}

// generateClozeCards returns one card per cloze number in the note's fields.
// The number is read from the field content, not from a template: a number that
// appears in a field the template never renders still gets a card.
func generateClozeCards(cfg Config, data RenderData) []GeneratedCard {
	values := make([]string, len(cfg.Fields))
	for i, field := range cfg.Fields {
		values[i] = data.Fields[field.ID]
	}
	numbers := clozeNumbersInFields(values)
	cards := make([]GeneratedCard, 0, len(numbers))
	for _, number := range numbers {
		ordinal := number - 1
		if ordinal > maxClozeCardOrdinal {
			ordinal = maxClozeCardOrdinal
		}
		cards = append(cards, GeneratedCard{
			Template: clozeTemplatePrefix + strconv.Itoa(number),
			Ordinal:  ordinal,
		})
	}
	return cards
}

// EmptyExistingCards reports the stored cards a note no longer generates, in
// the order they are passed in. It never deletes anything: removing an empty
// card is a separate, explicit action.
//
// A card is empty when its ordinal is not among the generated ordinals. That
// covers a normal card whose front now references no non-empty field, a normal
// card whose template no longer exists, and a cloze card whose cloze number
// appears in no field.
func EmptyExistingCards(cfg Config, data RenderData, existing []ExistingCard) ([]ExistingCard, error) {
	generated, err := GenerateCards(cfg, data)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]bool, len(generated))
	for _, card := range generated {
		wanted[card.Ordinal] = true
	}
	var empty []ExistingCard
	for _, card := range existing {
		if !wanted[card.Ordinal] {
			empty = append(empty, card)
		}
	}
	return empty, nil
}

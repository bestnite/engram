package answer

import (
	"fmt"
	"strings"
)

// Markup contract
//
// RenderFront returns one input element (or one per blank/item) carrying
// id="<element id>" and data-control="<name>". A text, numeric, list or cloze
// input is an <input>; a short answer is a <textarea>; the choice and
// true/false controls are radio buttons or checkboxes whose name is the element
// id and whose value is the postable answer (a zero-based option index for a
// choice control, the literal true/false for true/false). RenderBack returns an
// element with class="answer-breakdown" whose descendants carry
// data-correct="true" or "false" instead of localised wording.

// elementID returns the stable id of the control's input element: the caller's
// ElementID when it set one, otherwise a value derived from the marker.
func elementID(ctx RenderContext) string {
	if id := strings.TrimSpace(ctx.ElementID); id != "" {
		return id
	}
	if id := markerID(ctx.Marker); id != "" {
		return id
	}
	return "answer-input"
}

// markerID turns a placeholder such as [[type:answer]] into an id-safe string.
func markerID(marker string) string {
	if marker == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range marker {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// styleAttribute renders the field font and size as an inline style, or the
// empty string when neither is set.
func styleAttribute(ctx RenderContext) string {
	var styles []string
	if font := strings.TrimSpace(ctx.Font); font != "" {
		styles = append(styles, "font-family:"+font)
	}
	if ctx.FontSize > 0 {
		styles = append(styles, fmt.Sprintf("font-size:%dpt", ctx.FontSize))
	}
	if len(styles) == 0 {
		return ""
	}
	return ` style="` + escapeAttribute(strings.Join(styles, ";")) + `"`
}

// inputMarkup renders one text-like input element.
func inputMarkup(ctx RenderContext, control, attributes string) string {
	return `<input id="` + escapeAttribute(control+"-"+elementID(ctx)) + `" class="answer-input" data-control="` +
		escapeAttribute(control) + `" ` + attributes + styleAttribute(ctx) + `>`
}

// inputListMarkup renders one input per hint, numbered from the shared element
// id so every box has a stable, distinct id.
func inputListMarkup(ctx RenderContext, control string, count int) string {
	id := elementID(ctx)
	var b strings.Builder
	b.WriteString(`<div class="answer-inputs" data-control="` + escapeAttribute(control) + `">`)
	for i := 0; i < count; i++ {
		placeholder := ""
		if i < len(ctx.Hints) && strings.TrimSpace(ctx.Hints[i]) != "" {
			placeholder = ` placeholder="` + escapeAttribute(ctx.Hints[i]) + `"`
		}
		b.WriteString(`<input id="` + escapeAttribute(fmt.Sprintf("%s-%d", id, i)) +
			`" class="answer-input" data-control="` + escapeAttribute(control) +
			`" data-index="` + fmt.Sprintf("%d", i) + `" type="text" autocomplete="off"` +
			placeholder + styleAttribute(ctx) + `>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// choiceMarkup renders one radio or checkbox per option. The value is the
// zero-based stored index, and every control in the group shares the element
// id as its name.
func choiceMarkup(ctx RenderContext, control string, options []string, inputType string) string {
	id := elementID(ctx)
	var b strings.Builder
	b.WriteString(`<div class="answer-choices" data-control="` + escapeAttribute(control) + `">`)
	for i, option := range options {
		b.WriteString(`<label class="answer-choice"><input type="` + inputType +
			`" name="` + escapeAttribute(id) + `" value="` + fmt.Sprintf("%d", i) +
			`" data-control="` + escapeAttribute(control) + `"` + styleAttribute(ctx) + `> ` +
			escapeHTML(option) + `</label>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// boundaryMarkup wraps a plain-text breakdown in the common result element.
func boundaryMarkup(control, text string) string {
	return `<div class="answer-breakdown" data-control="` + escapeAttribute(control) + `">` +
		escapeHTML(text) + `</div>`
}

// escapeHTML escapes text for HTML content.
func escapeHTML(text string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(text)
}

// escapeAttribute escapes text for an HTML attribute value.
func escapeAttribute(text string) string {
	return escapeHTML(text)
}

package web

import (
	"regexp"
	"testing"
)

// This file keeps the testable part of the accessibility audit logic itself: the accessible-name check for form controls.
//
// The per-page form-control label audit used to target the HTML rendered by SSR pages; the page layer has switched to the SPA app shell (forms are rendered
// client-side), and page-level assertions are covered by the frontend tests. The visible focus styles (static/css/input.css) and review keyboard shortcuts
// (static/js/review.js) were removed together with the SSR static assets, and the corresponding static assertions went with them.
// Only the pure-function audit and its negative cases are kept here.

var (
	// reLabel matches a <label ...>...</label> block (including wrapping labels). Labels in the templates are not nested.
	reLabel = regexp.MustCompile(`(?s)<label\b([^>]*)>(.*?)</label>`)
	// reControl matches input/select/textarea start tags.
	reControl = regexp.MustCompile(`(?s)<(input|select|textarea)\b([^>]*)>`)
)

// attrOf takes the value of name="value" from a start tag's attribute string; returns an empty string when it cannot be found.
// The attribute name must be preceded by whitespace or the start of the string, so that "data-id" is not treated as "id".
func attrOf(attrs, name string) string {
	m := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	return m[1]
}

// unlabelledControls returns the snippets of non-hidden form controls in body that lack an accessible name.
// Three ways count as "has a name": wrapped by a <label>, carrying aria-label/aria-labelledby,
// or having an id pointed at by some label's for. Kept as a pure function so it can itself be negative-tested.
func unlabelledControls(body string) []string {
	type labelBlock struct {
		start, end int
		forID      string
	}
	var labels []labelBlock
	for _, m := range reLabel.FindAllStringSubmatchIndex(body, -1) {
		labels = append(labels, labelBlock{start: m[0], end: m[1], forID: attrOf(body[m[2]:m[3]], "for")})
	}

	var bad []string
	for _, m := range reControl.FindAllStringSubmatchIndex(body, -1) {
		tag := body[m[2]:m[3]]
		attrs := body[m[4]:m[5]]
		start, end := m[0], m[1]

		if tag == "input" && attrOf(attrs, "type") == "hidden" {
			continue
		}
		if attrOf(attrs, "aria-label") != "" || attrOf(attrs, "aria-labelledby") != "" {
			continue
		}

		labelled := false
		for _, b := range labels {
			if start >= b.start && end <= b.end {
				labelled = true
				break
			}
		}
		if !labelled {
			if id := attrOf(attrs, "id"); id != "" {
				for _, b := range labels {
					if b.forID == id {
						labelled = true
						break
					}
				}
			}
		}
		if !labelled {
			bad = append(bad, body[start:end])
		}
	}
	return bad
}

// TestUnlabelledControlsDetectsBareInput is the negative case for the audit logic itself: an input without a label must be caught,
// while inputs wrapped by a label, targeted by for=, or carrying aria-label pass. This keeps the audit from silently reporting a false green.
func TestUnlabelledControlsDetectsBareInput(t *testing.T) {
	bare := `<form><input type="text" name="q" placeholder="Search"/></form>`
	if got := unlabelledControls(bare); len(got) != 1 {
		t.Fatalf("bare input: got %d unlabelled control(s), want 1 (%v)", len(got), got)
	}

	labelled := []string{
		`<label><span>Name</span><input type="text" name="name"/></label>`,
		`<label for="tz">Timezone</label><input id="tz" name="tz"/>`,
		`<input type="text" name="x" aria-label="Answer"/>`,
		`<input type="hidden" name="csrf" value="t"/>`,
	}
	for _, html := range labelled {
		if got := unlabelledControls(html); len(got) != 0 {
			t.Errorf("labelled control flagged: %s -> %v", html, got)
		}
	}
	t.Logf("audit self-check: bare input flagged; wrapped/for=/aria-label/hidden controls pass")
}

package web

// This file keeps only the **markup checkers** for the mobile drawer defect and their negative cases.
//
// The defect users reported (the drawer using relative pushed the page header taller, and the scrim sat inside a <header>
// carrying backdrop-filter, so fixed inset-0 covered only a strip of the header) used to be asserted on HTML rendered by the SSR header. The page layer has moved to the SPA
// app shell: the drawer and scrim markup is rendered by the frontend (Svelte) now and the Go side no longer produces that HTML, so the page-level and component-level
// assertions went with it (the related regressions are covered by frontend tests).
//
// The checkers themselves can still be verified independently: below, the broken markup and the fixed markup are each run through, so the check logic cannot
// silently go green.

import (
	"regexp"
	"strings"
	"testing"
)

var reHeaderElement = regexp.MustCompile(`(?s)<header\b[^>]*>.*?</header>`)

// openingTagByID returns the opening tag of the element whose id equals id; an empty string when not found.
// The attribute name must start with whitespace, so "data-id" is not taken for "id".
func openingTagByID(html, id string) string {
	re := regexp.MustCompile(`<[a-zA-Z][^>]*\sid="` + regexp.QuoteMeta(id) + `"[^>]*>`)
	return re.FindString(html)
}

// classListOf returns the set of class names on the opening tag's class attribute (an empty set when there is no class).
func classListOf(tag string) map[string]bool {
	set := map[string]bool{}
	m := regexp.MustCompile(`\sclass="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return set
	}
	for _, c := range strings.Fields(m[1]) {
		set[c] = true
	}
	return set
}

// mobileNavDrawerInFlowProblem returns evidence that the drawer still takes part in normal document flow; empty string when none.
func mobileNavDrawerInFlowProblem(html string) string {
	tag := openingTagByID(html, "mobile-nav-menu")
	if tag == "" {
		return "the rendered page has no #mobile-nav-menu element"
	}
	classes := classListOf(tag)
	for _, inFlow := range []string{"relative", "static"} {
		if classes[inFlow] {
			return "#mobile-nav-menu uses position:" + inFlow + ", so opening it grows the header and pushes <main> down: " + tag
		}
	}
	if !classes["absolute"] && !classes["fixed"] {
		return "#mobile-nav-menu is neither absolute nor fixed, so it stays in normal flow: " + tag
	}
	return ""
}

// mobileNavScrimInsideHeaderProblem returns evidence that the scrim sits inside <header>; empty string when none.
func mobileNavScrimInsideHeaderProblem(html string) string {
	header := reHeaderElement.FindString(html)
	if header == "" {
		return "the rendered page has no <header> element"
	}
	if strings.Contains(header, `id="mobile-nav-backdrop"`) {
		return "the mobile nav scrim sits inside <header>; the header carries backdrop-filter, which makes it a containing block for fixed descendants, so fixed inset-0 covers only the header strip instead of the viewport"
	}
	return ""
}

// TestMobileNavProblemCheckersFlagBrokenMarkup is the negative case for the checker functions themselves: the markup from the time of the
// defect (drawer relative, scrim inside the header) must be caught, and the fixed markup must pass.
func TestMobileNavProblemCheckersFlagBrokenMarkup(t *testing.T) {
	broken := `<header class="sticky top-0 backdrop-blur-md">` +
		`<div id="mobile-nav-backdrop" class="fixed inset-0 z-40 hidden sm:hidden"></div>` +
		`<div id="mobile-nav-menu" class="hidden sm:hidden relative z-50"></div>` +
		`</header>`
	if got := mobileNavDrawerInFlowProblem(broken); got == "" {
		t.Error("the in-flow drawer check accepted a relative drawer: the reported defect would pass unnoticed")
	}
	if got := mobileNavScrimInsideHeaderProblem(broken); got == "" {
		t.Error("the scrim check accepted a scrim inside <header>: the reported defect would pass unnoticed")
	}

	fixed := `<header class="sticky top-0 backdrop-blur-md">` +
		`<div id="mobile-nav-menu" class="hidden sm:hidden absolute inset-x-0 top-full z-50"></div>` +
		`</header>` +
		`<div id="mobile-nav-backdrop" class="fixed inset-0 z-20 hidden sm:hidden"></div>`
	if got := mobileNavDrawerInFlowProblem(fixed); got != "" {
		t.Errorf("the in-flow drawer check rejected the fixed markup: %s", got)
	}
	if got := mobileNavScrimInsideHeaderProblem(fixed); got != "" {
		t.Errorf("the scrim check rejected the fixed markup: %s", got)
	}
	t.Logf("checkers: flag the reported markup, accept the fixed markup")
}

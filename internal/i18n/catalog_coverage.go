package i18n

import (
	"sort"
)

// Language pack completeness report.
//
// Definition (frozen; both the page and the tests rely on it): the baseline key set is the **union** of
// all language pack key sets, not any single language. Under the union, any key unique to one language
// is reported as "missing" for the others, which is exactly the difference "completeness" should expose;
// with en as the baseline, keys unique to en would go unreported. Both definitions agree once the packs
// are fully aligned (this repo enforces alignment at load time via Translator.CheckParity).

// LocaleCoverage is the translation coverage of one language.
type LocaleCoverage struct {
	// Code is the language code (file name without .yaml).
	Code string
	// Total is the size of the baseline (union) key set; Present is the number of keys this language has.
	Total   int
	Present int
	// Missing is the keys this language lacks versus the baseline, sorted lexicographically; nil when complete.
	Missing []string
}

// Percent returns the coverage percentage, rounded down; an empty baseline counts as 100.
func (c LocaleCoverage) Percent() int {
	if c.Total == 0 {
		return 100
	}
	return c.Present * 100 / c.Total
}

// Complete reports that this language is missing no keys versus the baseline.
func (c LocaleCoverage) Complete() bool { return len(c.Missing) == 0 }

// Coverage computes the coverage of each language against the baseline key set (see the comment at the
// top of this file for the definition). The returned order is lexicographic by language code, so page
// rendering stays stable.
func Coverage(sets map[string]map[string]bool) []LocaleCoverage {
	union := make(map[string]bool)
	for _, set := range sets {
		for id := range set {
			union[id] = true
		}
	}
	total := len(union)
	codes := make([]string, 0, len(sets))
	for code := range sets {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	out := make([]LocaleCoverage, 0, len(codes))
	for _, code := range codes {
		set := sets[code]
		missing := make([]string, 0)
		for id := range union {
			if !set[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		out = append(out, LocaleCoverage{
			Code: code, Total: total, Present: total - len(missing), Missing: missing,
		})
	}
	return out
}

// Coverage reports the coverage of the loaded language packs; the baseline is the union of the key sets
// of all packs (see Coverage). A normal instance (CheckParity passed) is always 100%; the value of the
// report page is exposing the gap when a new language is added.
func (t *Translator) Coverage() []LocaleCoverage {
	return Coverage(t.ids)
}

package store

import (
	"path"
	"strings"

	"git.nite07.com/nite/engram/internal/notetype"
)

// noteTypeMediaStrings returns every string of a note type config that may carry a media reference: each template's
// question and answer format, the shared CSS and the LaTeX pre/post bodies. The same extraction rule as a note field is
// used, so a name is recognised in src=, [sound:] and css url() positions only.
func noteTypeMediaStrings(cfg notetype.Config) []string {
	values := make([]string, 0, len(cfg.Templates)*2+3)
	for _, t := range cfg.Templates {
		values = append(values, t.QFormat, t.AFormat)
	}
	values = append(values, cfg.CSS, cfg.LaTeXPre, cfg.LaTeXPost)
	return values
}

// noteTypeMediaNames returns the `_`-prefixed media names a note type config refers to, de-duplicated.
//
// Only `_`-prefixed files are recorded: they are the assets a deck owner adds for the note type itself (fonts, CSS-only
// images) and no note field references them, so the note's media_notes mapping cannot keep them alive. Files a note
// references are recorded from the note fields instead.
func noteTypeMediaNames(cfg notetype.Config) []string {
	var names []string
	seen := make(map[string]struct{})
	for _, value := range noteTypeMediaStrings(cfg) {
		for _, name := range mediaNamesInHTML(value) {
			if !strings.HasPrefix(path.Base(name), "_") {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

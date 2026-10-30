package mail

import (
	"os"
	"path/filepath"
	"testing"

	"git.nite07.com/nite/engram/internal/i18n"
	"golang.org/x/text/language"
)

// The goldens in testdata/builtin/ are the before/after proof of the Markdown-to-HTML conversion of
// the built-in bodies. Each file is the complete HTML segment of one built-in mail, captured before
// the built-in bodies became HTML, with the fixed sample values below. The conversion is correct
// exactly when the current pipeline still produces these bytes.
//
// Regenerate only when a change to the shell, the built-in copy or the inline styles is intended:
//
//	UPDATE_GOLDEN=1 go test ./internal/mail -run TestBuiltinBodiesGolden
//
// The files are not hand-edited: a hand-edited golden proves nothing about the conversion.
func TestBuiltinBodiesGolden(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("load language packs: %v", err)
	}
	for _, code := range translator.SupportedCodes() {
		loc := translator.Localizer(language.Make(code))
		for _, def := range Catalog() {
			typ := def.Type
			variants := []string{VariantDefault}
			if typ == TypeEmailVerification {
				variants = append(variants, VariantEmailChange)
			}
			for _, variant := range variants {
				name := string(typ)
				if variant != VariantDefault {
					name += "_" + variant
				}
				path := filepath.Join("testdata", "builtin", name+"_"+code+".html")
				t.Run(name+"/"+code, func(t *testing.T) {
					vars := sampleVars(typ)
					unsubURL := ""
					if CanUnsubscribe(typ) {
						unsubURL = "https://example.com/unsubscribe?token=EXAMPLE"
					}
					footerNote, unsubLabel := Footer(loc.T, unsubURL)
					subject, body, ok := DefaultTemplate(typ, variant, loc.Tf, vars)
					if !ok {
						t.Fatal("no built-in template")
					}
					out, err := Render(RenderInput{
						Type:             typ,
						Site:             "Example",
						Lang:             code,
						Vars:             vars,
						FallbackSubject:  subject,
						FallbackBodyMD:   body,
						UnsubscribeURL:   unsubURL,
						FooterNote:       footerNote,
						UnsubscribeLabel: unsubLabel,
					})
					if err != nil {
						t.Fatalf("Render() error = %v", err)
					}
					if update {
						if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
							t.Fatalf("create golden directory: %v", err)
						}
						if err := os.WriteFile(path, []byte(out.HTML), 0o644); err != nil {
							t.Fatalf("write golden: %v", err)
						}
						return
					}
					want, err := os.ReadFile(path)
					if err != nil {
						t.Fatalf("read golden (regenerate with UPDATE_GOLDEN=1): %v", err)
					}
					if string(want) != out.HTML {
						t.Errorf("built-in mail HTML changed:\n--- golden ---\n%s\n--- current ---\n%s", want, out.HTML)
					}
				})
			}
		}
	}
}

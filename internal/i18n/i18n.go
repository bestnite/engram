// Package i18n loads the YAML language packs, resolves the language per request, and puts the localizer into the request context.
//
// Boundary: this package only owns user-facing text. Logs are always English and never take
// their values from here; error codes are English constants and are not defined here either.
package i18n

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// localesFS embeds the language packs into the binary (the packs ship with the binary).
//
//go:embed locales/*.yaml
var localesFS embed.FS

// localesDir is the directory name holding the language packs inside the embedded FS.
const localesDir = "locales"

// DefaultLocaleCode is the site default language; it is used when every language resolution fails.
const DefaultLocaleCode = "zh-CN"

// supportedCodes lists the launch languages; the order is the display order of the "language switcher".
// Adding a language = add locales/<code>.yaml and append the code here.
var supportedCodes = []string{"zh-CN", "en"}

// Translator holds the loaded language packs and the matching information for supported languages.
type Translator struct {
	bundle     *goi18n.Bundle
	tags       map[string]language.Tag
	codes      []string
	defaultTag language.Tag
	// ids records the message id set of each language pack, used for the load-time completeness check.
	ids map[string]map[string]bool
}

// New builds a Translator from the embedded language packs.
func New() (*Translator, error) {
	return NewFromFS(localesFS, localesDir)
}

// NewFromFS reads <dir>/*.yaml language packs from any file system; the file name (minus .yaml) is the language code.
func NewFromFS(fsys fs.FS, dir string) (*Translator, error) {
	bundle := goi18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locale dir %q: %w", dir, err)
	}
	loaded := make(map[string]bool)
	ids := make(map[string]map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		file := path.Join(dir, e.Name())
		if _, err := bundle.LoadMessageFileFS(fsys, file); err != nil {
			return nil, fmt.Errorf("i18n: load locale file %q: %w", e.Name(), err)
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("i18n: read locale file %q: %w", e.Name(), err)
		}
		code := strings.TrimSuffix(e.Name(), ".yaml")
		if ids[code], err = messageIDs(data); err != nil {
			return nil, fmt.Errorf("i18n: parse locale file %q: %w", e.Name(), err)
		}
		loaded[code] = true
	}

	t := &Translator{bundle: bundle, tags: make(map[string]language.Tag), ids: ids}
	for _, code := range supportedCodes {
		if !loaded[code] {
			return nil, fmt.Errorf("i18n: supported locale %q has no catalog file", code)
		}
		tag, err := language.Parse(code)
		if err != nil {
			return nil, fmt.Errorf("i18n: parse locale %q: %w", code, err)
		}
		t.tags[code] = tag
		t.codes = append(t.codes, code)
	}
	defaultTag, ok := t.tags[DefaultLocaleCode]
	if !ok {
		return nil, fmt.Errorf("i18n: default locale %q is not supported", DefaultLocaleCode)
	}
	t.defaultTag = defaultTag
	// A missing key is a hard defect: fail at load time so untranslated text cannot leak onto the page (AGENTS.md §4).
	if err := t.CheckParity(); err != nil {
		return nil, err
	}
	return t, nil
}

// CheckParity asserts that every language pack has exactly the same key set as the default language.
// Any error returned means the language pack needs entries added or removed.
func (t *Translator) CheckParity() error {
	base := t.ids[DefaultLocaleCode]
	for _, code := range t.codes {
		for id := range base {
			if !t.ids[code][id] {
				return fmt.Errorf("i18n: locale %q is missing message %q", code, id)
			}
		}
		for id := range t.ids[code] {
			if !base[id] {
				return fmt.Errorf("i18n: locale %q has message %q that %q lacks", code, id, DefaultLocaleCode)
			}
		}
	}
	return nil
}

// messageIDs parses the set of message ids in one YAML language pack (only used for the completeness check).
func messageIDs(data []byte) (map[string]bool, error) {
	var msgs []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			out[m.ID] = true
		}
	}
	return out, nil
}

// SupportedCodes returns a copy of the supported language codes, in stable order.
func (t *Translator) SupportedCodes() []string {
	out := make([]string, len(t.codes))
	copy(out, t.codes)
	return out
}

// Pick chooses the request language by fixed precedence: requested (explicit ?lang override) > userLocale (user setting)
// > acceptLanguage > site default. When one level yields an unsupported language it moves to the next level instead of
// doing a nearest match -- otherwise a user who set the language to "de" would beat the "en" in Accept-Language.
func (t *Translator) Pick(requested, userLocale, acceptLanguage string) language.Tag {
	for _, raw := range []string{requested, userLocale} {
		if raw == "" {
			continue
		}
		if tag, err := language.Parse(raw); err == nil {
			if matched, ok := t.match(tag); ok {
				return matched
			}
		}
	}
	if acceptLanguage != "" {
		if tags, _, err := language.ParseAcceptLanguage(acceptLanguage); err == nil {
			for _, tag := range tags {
				if matched, ok := t.match(tag); ok {
					return matched
				}
			}
		}
	}
	return t.defaultTag
}

// match only accepts tags with the same base as a supported language and maps them to the supported canonical
// tag (en-US->en, zh-TW->zh-CN).
func (t *Translator) match(tag language.Tag) (language.Tag, bool) {
	base, _ := tag.Base()
	for _, code := range t.codes {
		want, _ := t.tags[code].Base()
		if base.String() == want.String() {
			return t.tags[code], true
		}
	}
	return language.Tag{}, false
}

// Localizer is bound to one language, for handlers and templates to fetch values.
type Localizer struct {
	code string
	tag  language.Tag
	lc   *goi18n.Localizer
}

// Localizer builds a localizer for the given language; unsupported tags fall back to the site default language.
func (t *Translator) Localizer(tag language.Tag) *Localizer {
	code := DefaultLocaleCode
	base, _ := tag.Base()
	for _, c := range t.codes {
		want, _ := t.tags[c].Base()
		if base.String() == want.String() {
			code = c
			break
		}
	}
	return &Localizer{code: code, tag: tag, lc: goi18n.NewLocalizer(t.bundle, code)}
}

// Locale returns the canonical language code this localizer actually uses.
func (l *Localizer) Locale() string { return l.code }

// Tag returns the original language tag chosen by the request.
func (l *Localizer) Tag() language.Tag { return l.tag }

// T returns the translation for msgID. A missing key is a bug: log one English line and fall back to
// the key itself so the gap is visible on the page; language pack completeness is covered by unit tests (AGENTS.md §2.1, §4).
func (l *Localizer) T(msgID string) string {
	msg, err := l.lc.Localize(&goi18n.LocalizeConfig{MessageID: msgID})
	if err != nil {
		slog.Error("i18n: missing message", "message_id", msgID, "locale", l.code)
		return msgID
	}
	return msg
}

// Tf is like T but substitutes data as template data into a translation with placeholders (e.g. "{{.count}} cards total").
// The placeholder syntax comes from go-i18n text/template; missing keys are handled exactly like T.
func (l *Localizer) Tf(msgID string, data map[string]any) string {
	msg, err := l.lc.Localize(&goi18n.LocalizeConfig{MessageID: msgID, TemplateData: data})
	if err != nil {
		slog.Error("i18n: missing message", "message_id", msgID, "locale", l.code)
		return msgID
	}
	return msg
}

// localizerKey is a package-private context key, so it cannot collide with other packages.
type localizerKey struct{}

// WithLocalizer puts the localizer into the request context.
func WithLocalizer(ctx context.Context, l *Localizer) context.Context {
	return context.WithValue(ctx, localizerKey{}, l)
}

// FromContext pulls the localizer out of the request context; returns nil when absent.
func FromContext(ctx context.Context) *Localizer {
	l, _ := ctx.Value(localizerKey{}).(*Localizer)
	return l
}

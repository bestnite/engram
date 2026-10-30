package render

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// migrationMarkdown is today's card-content Markdown configuration without the
// shortcode extension: the data migration rewrites {{tts:...}} and
// {{audio:...}} into Anki syntax itself, so those markers have to survive
// goldmark as text. Formula protection is the same as the card pipeline's, so a
// migrated field keeps \( \) and \[ \] exactly as the browser-side MathJax
// needs them.
//
// There is no bluemonday pass. The new content model stores unsanitised HTML
// and shows it only inside the sandboxed frame, so sanitising during the
// migration would remove markup the model now allows.
var migrationMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe(), html.WithHardWraps()),
)

// RenderMigrationMarkdown converts one old Markdown/TeX field value to the HTML
// the new model stores.
//
// TEMPORARY: this entry point exists only for the one-time data migration. The
// package that owns the old Markdown pipeline is deleted once the application
// renders note types, and this function goes with it; new code must not depend
// on it.
func RenderMigrationMarkdown(src string) (string, error) {
	protected, spans := protectMath(src)
	var buf bytes.Buffer
	if err := migrationMarkdown.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render migration markdown: %w", err)
	}
	return restoreMath(buf.String(), spans), nil
}

// RenderMigrationMarkdownInline converts one string-array item to inline HTML.
//
// An array item is a line, not a document: the block renderer would wrap it in
// <p>...</p>, and joining those wrappers with <br> would put a paragraph inside
// a line. The fragment inside the single paragraph is returned instead. An item
// that goldmark reads as an HTML block (for example a leading <div>) has no
// paragraph wrapper and is returned with only its trailing newline removed.
//
// TEMPORARY, for the same reason as RenderMigrationMarkdown.
func RenderMigrationMarkdownInline(src string) (string, error) {
	rendered, err := RenderMigrationMarkdown(src)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rendered, "<p>") && strings.HasSuffix(rendered, "</p>\n") {
		return rendered[len("<p>") : len(rendered)-len("</p>\n")], nil
	}
	return strings.TrimSuffix(rendered, "\n"), nil
}

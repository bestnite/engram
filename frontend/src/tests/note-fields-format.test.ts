import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { createCatalog, fieldsForKind } from '../lib/card-types';
import { setLocale } from '../lib/i18n';
import { CARD_TYPES } from './card-type-fixture';

/**
 * The Markdown formatting row is rendered on the server side of the same component that renders the
 * field, so its presence is decided by the control shape rather than by the host view. These cases
 * cover both halves of that decision: every single-string control gets the row, and a field stored
 * as an array or a checkbox never does.
 */
describe('NoteFieldsForm Markdown formatting row', () => {
  const catalog = createCatalog(CARD_TYPES.kinds);

  beforeEach(() => {
    setLocale('zh-CN');
  });

  function renderKind(kind: string): string {
    const specs = fieldsForKind(catalog, kind);
    return render(NoteFieldsForm, {
      props: {
        specs,
        fields: emptyFields(specs),
        oninsertimage: () => {},
        oninsertshortcode: () => {},
      },
    }).html;
  }

  it('gives every text and textarea field the three formatting actions', () => {
    const html = renderKind('basic');
    for (const key of ['front', 'back', 'source_url', 'extra']) {
      expect(html).toContain(`data-testid="note-field-${key}-format-toolbar"`);
      for (const action of ['bold', 'italic', 'strike']) {
        expect(html).toContain(`data-testid="note-field-${key}-format-${action}"`);
      }
    }
  });

  it('names the actions and the group in the active language, with the field they apply to', () => {
    const html = renderKind('basic');
    expect(html).toContain('aria-label="「正面」的文字格式"');
    expect(html).toContain('aria-label="将「正面」设为粗体"');
    expect(html).toContain('aria-label="将「正面」设为斜体"');
    expect(html).toContain('aria-label="将「正面」设为删除线"');
    // The keyboard binding is exposed to assistive technology without becoming another visible word.
    expect(html).toContain('aria-keyshortcuts="Control+B Meta+B"');
    expect(html).toContain('aria-keyshortcuts="Control+Shift+X Meta+Shift+X"');
  });

  it('gives a lines field and a bool field no formatting row', () => {
    const html = renderKind('list');
    expect(html).toContain('data-testid="note-field-prompt-format-toolbar"');
    expect(html).not.toContain('data-testid="note-field-items-format-toolbar"');
    expect(html).not.toContain('data-testid="note-field-ordered-format-toolbar"');
  });

  it('gives no formatting row to numeric or option-index controls', () => {
    const numeric = renderKind('numeric');
    expect(numeric).toContain('data-testid="note-field-prompt-format-toolbar"');
    expect(numeric).not.toContain('data-testid="note-field-value-format-toolbar"');

    const choice = renderKind('choice_single');
    expect(choice).not.toContain('data-testid="note-field-answer-format-toolbar"');
    expect(choice).not.toContain('data-testid="note-field-options-format-toolbar"');
  });
});

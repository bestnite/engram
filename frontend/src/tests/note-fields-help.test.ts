import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { createCatalog, fieldsForKind } from '../lib/card-types';
import { setLocale } from '../lib/i18n';
import { CARD_TYPES } from './card-type-fixture';

/**
 * The cloze syntax explainer lives in a question-mark popover next to the label: the label
 * itself is now just the field name, and fields with no syntax to explain get no
 * question-mark button.
 */
describe('NoteFieldsForm field help', () => {
  const catalog = createCatalog(CARD_TYPES.kinds);

  beforeEach(() => {
    setLocale('zh-CN');
  });

  function renderKind(kind: string): string {
    const specs = fieldsForKind(catalog, kind);
    return render(NoteFieldsForm, { props: { specs, fields: emptyFields(specs) } }).html;
  }

  it('keeps the cloze label short and moves the syntax into a help button', () => {
    const html = renderKind('cloze');
    expect(html).toContain('data-testid="note-field-text-help"');
    expect(html).toContain('aria-label="文本是什么意思？"');
    // The syntax explainer is absent from the page before the popover opens, and the label no longer carries it either.
    expect(html).not.toContain('{{c1::');
  });

  it('adds no help button to fields without syntax to explain', () => {
    expect(renderKind('basic')).not.toContain('-help"');
  });
});

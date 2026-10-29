import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { backField, createCatalog, fieldsForKind, frontField } from '../lib/card-types';
import { setLocale } from '../lib/i18n';
import { CARD_TYPES } from './card-type-fixture';

/**
 * The Markdown formatting row is the header of the framed card face field. Its presence is decided by
 * two facts: the field is one of the kind's face fields, and its control is a textarea. These cases
 * cover both halves: a face textarea gets the whole set, and a field off the face, a single-line
 * field, a field stored as an array or a checkbox never does.
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
        faceFields: [frontField(catalog, kind), backField(catalog, kind)],
        oninsertimage: () => {},
        oninsertshortcode: () => {},
      },
    }).html;
  }

  it('gives the front and back fields every formatting action', () => {
    const html = renderKind('basic');
    for (const key of ['front', 'back']) {
      expect(html).toContain(`data-testid="note-field-${key}-toolbar"`);
      for (const action of ['bold', 'italic', 'strike', 'code', 'link', 'quote', 'bullet', 'ordered', 'code_block', 'table']) {
        expect(html).toContain(`data-testid="note-field-${key}-format-${action}"`);
      }
    }
  });

  it('gives the extra note and the source URL no formatting row', () => {
    const html = renderKind('basic');
    expect(html).not.toContain('data-testid="note-field-extra-toolbar"');
    expect(html).not.toContain('data-testid="note-field-source_url-toolbar"');
    expect(html).not.toContain('data-testid="note-field-extra-format-bold"');
  });

  it('frames the face field once: the textarea itself draws no field-input border', () => {
    const html = renderKind('basic');
    expect(html).toContain('data-testid="note-field-front-frame"');
    const area = html.slice(html.indexOf('<textarea'), html.indexOf('</textarea>'));
    expect(area).toContain('data-testid="note-field-front"');
    expect(area).toContain('field-frame-body');
    expect(area).not.toContain('field-input');
  });

  it('names the actions and the group in the active language, with the field they apply to', () => {
    const html = renderKind('basic');
    expect(html).toContain('aria-label="「正面」的文字格式"');
    expect(html).toContain('aria-label="将「正面」设为粗体"');
    expect(html).toContain('aria-label="将「正面」设为斜体"');
    expect(html).toContain('aria-label="将「正面」设为删除线"');
    expect(html).toContain('aria-label="将「正面」设为引用"');
    expect(html).toContain('aria-label="在「正面」插入表格"');
    // The keyboard binding is exposed to assistive technology without becoming another visible word.
    expect(html).toContain('aria-keyshortcuts="Control+B Meta+B"');
    expect(html).toContain('aria-keyshortcuts="Control+Shift+X Meta+Shift+X"');
  });

  it('gives a lines field and a bool field no formatting row', () => {
    const html = renderKind('list');
    expect(html).toContain('data-testid="note-field-prompt-toolbar"');
    expect(html).not.toContain('data-testid="note-field-items-toolbar"');
    expect(html).not.toContain('data-testid="note-field-ordered-toolbar"');
  });

  it('gives no formatting row to numeric or option-index controls', () => {
    const numeric = renderKind('numeric');
    expect(numeric).toContain('data-testid="note-field-prompt-toolbar"');
    expect(numeric).not.toContain('data-testid="note-field-value-toolbar"');

    const choice = renderKind('choice_single');
    expect(choice).not.toContain('data-testid="note-field-answer-toolbar"');
    expect(choice).not.toContain('data-testid="note-field-options-toolbar"');
  });
});

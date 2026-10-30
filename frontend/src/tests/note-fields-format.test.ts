import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { backField, createCatalog, fieldsForKind, frontField } from '../lib/card-types';
import { setLocale } from '../lib/i18n';
import { CARD_TYPES } from './card-type-fixture';

/**
 * The rich-text editor is the body of a card face field: an editing frame plus a toolbar whose
 * buttons send commands to it. These cases cover the decision that belongs to the form rather than
 * the editor: a face textarea gets the editor, and a field off the face, a single-line field, a
 * field stored as an array or a checkbox never does.
 */
describe('NoteFieldsForm rich-text field', () => {
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
        upload: async () => ({ url: '/media/a', name: 'a', mime: 'image/png' }),
      },
    }).html;
  }

  it('gives the front and back fields the whole toolbar', () => {
    const html = renderKind('basic');
    for (const key of ['front', 'back']) {
      expect(html).toContain(`data-testid="note-field-${key}-toolbar"`);
      for (const action of [
        'bold',
        'italic',
        'underline',
        'superscript',
        'subscript',
        'clear',
        'bullet',
        'ordered',
        'align_left',
        'align_center',
        'align_right',
        'align_justify',
        'indent',
        'outdent',
        'mathjax_inline',
        'mathjax_block',
        'latex',
        'cloze',
        'cloze_same',
      ]) {
        expect(html).toContain(`data-testid="note-field-${key}-format-${action}"`);
      }
      expect(html).toContain(`data-testid="note-field-${key}-text-color"`);
      expect(html).toContain(`data-testid="note-field-${key}-highlight"`);
      expect(html).toContain(`data-testid="note-field-${key}-source-toggle"`);
      expect(html).toContain(`data-testid="note-field-${key}-insert-image"`);
      // The editing surface is the sandboxed frame, not a textarea in the parent page.
      expect(html).toContain(`data-testid="note-field-${key}"`);
      expect(html).toContain('sandbox="allow-scripts"');
    }
  });

  it('gives the extra note and the source URL no rich-text editor', () => {
    const html = renderKind('basic');
    expect(html).not.toContain('data-testid="note-field-extra-toolbar"');
    expect(html).not.toContain('data-testid="note-field-source_url-toolbar"');
    expect(html).not.toContain('data-testid="note-field-extra-format-bold"');
  });

  it('names the actions and the group in the active language, with the field they apply to', () => {
    const html = renderKind('basic');
    expect(html).toContain('aria-label="「正面」的文字格式"');
    expect(html).toContain('aria-label="将「正面」设为粗体"');
    expect(html).toContain('aria-label="将「正面」设为斜体"');
    expect(html).toContain('aria-label="为「正面」添加下划线"');
    expect(html).toContain('aria-label="在「正面」插入行内 MathJax"');
    expect(html).toContain('aria-label="在「正面」新建填空"');
    // The keyboard binding is exposed to assistive technology without becoming another visible word.
    expect(html).toContain('aria-keyshortcuts="Control+B Meta+B"');
    expect(html).toContain('aria-keyshortcuts="Control+Shift+C Meta+Shift+C"');
  });

  it('gives a lines field and a bool field no rich-text editor', () => {
    const html = renderKind('list');
    expect(html).toContain('data-testid="note-field-prompt-toolbar"');
    expect(html).not.toContain('data-testid="note-field-items-toolbar"');
    expect(html).not.toContain('data-testid="note-field-ordered-toolbar"');
  });

  it('renders a plain-textarea field without the toolbar of a rich field', () => {
    const specs = fieldsForKind(catalog, 'basic');
    const html = render(NoteFieldsForm, {
      props: {
        specs,
        fields: emptyFields(specs),
        faceFields: ['front'],
        fieldOptions: { front: { plainText: true } },
      },
    }).html;
    expect(html).toContain('data-testid="note-field-front"');
    expect(html).not.toContain('data-testid="note-field-front-toolbar"');
  });
});

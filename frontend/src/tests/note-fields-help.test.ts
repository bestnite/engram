import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { backField, createCatalog, fieldsForKind, frontField } from '../lib/card-types';
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

/**
 * The editor's insertion and formatting actions live in the header of the card face field they act
 * on. Only a face textarea gets them; a `lines` field or a single-line answer field never does,
 * because a formatting command cannot address one element of an array or text compared verbatim.
 */
describe('NoteFieldsForm rich-text toolbar', () => {
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

  it('gives the front and back fields a media action and a formatting toolbar', () => {
    const html = renderKind('basic');
    for (const key of ['front', 'back']) {
      expect(html).toContain(`data-testid="note-field-${key}-toolbar"`);
      expect(html).toContain(`data-testid="note-field-${key}-insert-image"`);
      expect(html).toContain(`data-testid="note-field-${key}-format-bold"`);
    }
    // The icon buttons are named for the action and the field, so an unlabelled square is never read out on its own.
    expect(html).toContain('aria-label="在「正面」插入媒体"');
    expect(html).toContain('aria-label="将「正面」设为粗体"');
  });

  it('gives the fields off the card face no toolbar', () => {
    const html = renderKind('basic');
    for (const key of ['source_url', 'extra']) {
      expect(html).toContain(`data-testid="note-field-${key}"`);
      expect(html).not.toContain(`data-testid="note-field-${key}-toolbar"`);
      expect(html).not.toContain(`data-testid="note-field-${key}-insert-image"`);
    }
  });

  it('gives a lines back field no toolbar and a bool field none either', () => {
    const html = renderKind('list');
    expect(html).toContain('data-testid="note-field-prompt-toolbar"');
    expect(html).not.toContain('data-testid="note-field-items-toolbar"');
    expect(html).not.toContain('data-testid="note-field-ordered-toolbar"');
  });

  it('gives a single-line back field no toolbar, since its text is compared as the answer', () => {
    const html = renderKind('typed');
    expect(html).toContain('data-testid="note-field-prompt-toolbar"');
    expect(html).not.toContain('data-testid="note-field-answer-toolbar"');
  });

  it('renders no toolbar at all when the host names no face field', () => {
    const specs = fieldsForKind(catalog, 'basic');
    const html = render(NoteFieldsForm, { props: { specs, fields: emptyFields(specs) } }).html;
    expect(html).not.toContain('-toolbar"');
  });

  it('puts the insertion and formatting actions in the same header row', () => {
    const html = renderKind('basic');
    const header = html.slice(
      html.indexOf('data-testid="note-field-front-toolbar"'),
      html.indexOf('data-testid="note-field-front"')
    );
    expect(header).toContain('data-testid="note-field-front-format-bold"');
    expect(header).toContain('data-testid="note-field-front-insert-image"');
  });
});

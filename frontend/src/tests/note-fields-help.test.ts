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

/**
 * The insertion toolbar lives in the label row of the field it writes into. Both callbacks are
 * optional: a host that inserts nothing renders the same form without a toolbar, and a `lines`
 * field never gets one because an inline insertion cannot address one element of an array.
 */
describe('NoteFieldsForm insertion toolbar', () => {
  const catalog = createCatalog(CARD_TYPES.kinds);

  beforeEach(() => {
    setLocale('zh-CN');
  });

  function renderKind(kind: string, withActions: boolean): string {
    const specs = fieldsForKind(catalog, kind);
    return render(NoteFieldsForm, {
      props: {
        specs,
        fields: emptyFields(specs),
        ...(withActions
          ? { oninsertimage: () => {}, oninsertshortcode: () => {} }
          : {}),
      },
    }).html;
  }

  it('gives every text and textarea field the two insertion actions', () => {
    const html = renderKind('basic', true);
    for (const key of ['front', 'back', 'source_url', 'extra']) {
      expect(html).toContain(`data-testid="note-field-${key}-toolbar"`);
      expect(html).toContain(`data-testid="note-field-${key}-insert-image"`);
      expect(html).toContain(`data-testid="note-field-${key}-insert-shortcode"`);
    }
    // The icon buttons are named for the action and the field, so an unlabelled square is never read out on its own.
    expect(html).toContain('aria-label="在「正面」插入图片"');
    expect(html).toContain('aria-label="在「正面」插入短代码"');
  });

  it('gives a lines field no toolbar and a bool field none either', () => {
    const html = renderKind('list', true);
    expect(html).toContain('data-testid="note-field-prompt-toolbar"');
    expect(html).not.toContain('data-testid="note-field-items-toolbar"');
    expect(html).not.toContain('data-testid="note-field-ordered-toolbar"');
  });

  it('renders no toolbar at all when the host passes no insertion action', () => {
    const html = renderKind('basic', false);
    expect(html).not.toContain('-insert-image"');
    expect(html).not.toContain('-insert-shortcode"');
  });
});

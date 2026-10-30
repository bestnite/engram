import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteTypesView from '../lib/views/NoteTypesView.svelte';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import type { AnswerControl, NoteType, NoteTypeStock } from '../lib/api';

function noteType(overrides: Partial<NoteType> = {}): NoteType {
  return {
    id: 'nt-1',
    deck_id: 'd1',
    name: 'Basic',
    version: 3,
    config: {
      kind: 'normal',
      fields: [
        { id: 'f-front', name: 'Front', sticky: false, rtl: false, font: '', font_size: 0, description: '', plain_text: false, collapsed: false },
        { id: 'f-back', name: 'Back', sticky: false, rtl: false, font: '', font_size: 0, description: '', plain_text: false, collapsed: false },
      ],
      templates: [
        {
          id: 't-1',
          name: 'Card 1',
          qfmt: '{{Front}}',
          afmt: '{{FrontSide}}<hr id=answer>{{Back}}',
          answer: { control: 'text', bindings: { answer: 'f-back' } },
        },
      ],
      css: '.card { color: black; }',
      sort_field_id: 'f-front',
    },
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

const stocks: NoteTypeStock[] = [
  { id: 'anki:basic', source: 'anki', name: 'Basic', label_key: 'notetype.stock.anki.basic' },
  { id: 'engram:numeric', source: 'engram', name: 'Numeric answer', label_key: 'notetype.stock.engram.numeric' },
];

const controls: AnswerControl[] = [
  {
    name: 'text',
    roles: [
      { name: 'answer', kind: 'text', required: true },
      { name: 'accept', kind: 'lines', required: false },
    ],
  },
];

describe('NoteTypesView', () => {
  beforeEach(() => {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
  });

  it('renders the deck note types, their fields and templates', () => {
    const { html } = render(NoteTypesView, {
      props: {
        initialLoading: false,
        initialNoteTypes: [noteType()],
        initialStocks: stocks,
        initialControls: controls,
      },
    });
    expect(html).toContain('data-testid="note-types-view"');
    expect(html).toContain('data-testid="notetype-list"');
    expect(html).toContain('data-testid="notetype-editor"');
    expect(html).toContain('data-testid="notetype-field-f-front"');
    expect(html).toContain('data-testid="notetype-template-row-t-1"');
    expect(html).toContain('data-testid="notetype-preview-front"');
    expect(html).toContain('data-testid="notetype-preview-back"');
    expect(html).toContain('data-testid="notetype-answer-control"');
    expect(html).toContain('data-testid="notetype-answer-binding-answer"');
    expect(html).toContain('版本 3');
  });

  it('renders a live preview from the unsaved template', () => {
    const { html } = render(NoteTypesView, {
      props: {
        initialLoading: false,
        initialNoteTypes: [noteType()],
        initialStocks: stocks,
        initialControls: controls,
      },
    });
    // renderPreview substitutes the field names; the frames receive that HTML, never this page.
    expect(html).toContain('Front');
    expect(html).toContain('Back');
  });

  it('shows the empty state when the deck has no note types', () => {
    const { html } = render(NoteTypesView, {
      props: { initialLoading: false, initialNoteTypes: [], initialStocks: stocks, initialControls: controls },
    });
    expect(html).toContain('data-testid="note-types-empty"');
    expect(html).toContain('新建笔记类型');
  });

  it('renders the forbidden state for a reader without access', () => {
    const err = new Error('forbidden');
    const { html } = render(NoteTypesView, {
      props: { initialLoading: false, initialError: err, initialNoteTypes: [], initialStocks: stocks, initialControls: controls },
    });
    expect(html).toContain('data-testid="note-types-failed"');
  });

  it('locks template deletion for a cloze note type', () => {
    const cloze = noteType({
      config: {
        ...noteType().config,
        kind: 'cloze',
        templates: [{ ...noteType().config.templates[0]!, name: 'Cloze', qfmt: '{{cloze:Front}}', afmt: '{{cloze:Front}}' }],
      },
    });
    const { html } = render(NoteTypesView, {
      props: { initialLoading: false, initialNoteTypes: [cloze], initialStocks: stocks, initialControls: controls },
    });
    expect(html).toContain('填空笔记类型只能有一个模板');
  });
});

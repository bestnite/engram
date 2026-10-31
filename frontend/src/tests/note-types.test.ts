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
    label_key: 'notetype.answer.control.text',
    roles: [
      { name: 'answer', label_key: 'notetype.answer.role.answer', kind: 'text', required: true },
      { name: 'accept', label_key: 'notetype.answer.role.accept', kind: 'lines', required: false },
    ],
  },
];

describe('NoteTypesView', () => {
  beforeEach(() => {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
  });

  it('links to the help page instead of explaining the page inline, and keeps the syntax quick reference', () => {
    const { html } = render(NoteTypesView, {
      props: { initialLoading: false, initialNoteTypes: [noteType()], initialStocks: stocks, initialControls: controls },
    });
    expect(html).not.toContain('data-testid="notetype-guide"');
    expect(html).not.toContain('笔记类型决定一条笔记有哪些字段');
    expect(html).toContain('data-testid="notetype-help-link"');
    expect(html).toContain('href="/decks/d1/note-types/help?return=%2Fdecks%2Fd1%2Fnote-types"');
    // The syntax reference prints the template syntax literally, next to its translated description, and links to the help.
    expect(html).toContain('data-testid="notetype-syntax"');
    expect(html).toContain('{{Front}}');
    expect(html).toContain('{{tts zh_CN:Text}}');
    expect(html).toContain('翻面后只显示背面，和 Anki 一致');
    expect(html).toContain('data-testid="notetype-syntax-more"');
    // The styling hint names the rule behind the centred default.
    expect(html).toContain('text-align: center');
  });

  it('offers the help link on an empty page too, where a first-time reader lands', () => {
    const { html } = render(NoteTypesView, {
      props: { initialLoading: false, initialNoteTypes: [], initialStocks: stocks, initialControls: controls },
    });
    expect(html).toContain('data-testid="notetype-help-link"');
    expect(html).toContain('data-testid="note-types-empty"');
  });

  it('keeps the way back to the note editor through the help page, without reopening the create dialog', () => {
    const editor = '/decks/d1/notes/new?return=%2Fdecks%2Fd1';
    routeStore.set(matchRoute(`/decks/d1/note-types?create=1&return=${encodeURIComponent(editor)}`));
    const { html } = render(NoteTypesView, {
      props: { initialLoading: false, initialNoteTypes: [noteType()], initialStocks: stocks, initialControls: controls },
    });
    const match = /<a href="([^"]*)"[^>]*data-testid="notetype-help-link"/.exec(html);
    expect(match).not.toBeNull();
    const helpHref = (match?.[1] ?? '').replaceAll('&amp;', '&');
    const back = new URLSearchParams(helpHref.split('?')[1]).get('return') ?? '';
    expect(back.startsWith('/decks/d1/note-types?')).toBe(true);
    expect(new URLSearchParams(back.split('?')[1]).get('create')).toBeNull();
    expect(new URLSearchParams(back.split('?')[1]).get('return')).toBe(editor);
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
    // The control picker shows the catalog label, not the registry identifier.
    expect(html).toContain('文本答案');
    // The role labels are catalog entries too, not the registry identifiers.
    expect(html).toContain('角色「答案」');
    expect(html).toContain('角色「其他可接受写法」');
    expect(html).toContain('版本 3');
  });

  it('renders the preview frames the server response feeds', () => {
    const { html } = render(NoteTypesView, {
      props: {
        initialLoading: false,
        initialNoteTypes: [noteType()],
        initialStocks: stocks,
        initialControls: controls,
      },
    });
    // The preview HTML comes from the server after mount, so the SSR pass renders the two frames empty;
    // frontend/src/tests/note-types.client.ts covers the rendered face. The HTML must never be produced in this page.
    expect(html).toContain('data-testid="notetype-preview"');
    expect(html).toContain('data-side="question"');
    expect(html).toContain('data-side="answer"');
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

describe('common template catalog keys', () => {
  it.each(['vocabulary', 'japanese_furigana', 'listening', 'basic_extra'])('%s has a label in both catalogs', async (kind) => {
    const { formatMessage } = await import('../lib/i18n');
    for (const locale of ['zh-CN', 'en'] as const) {
      const key = `notetype.stock.template.${kind}`;
      expect(formatMessage(locale, key)).not.toBe(key);
    }
  });

  it.each(['choice_single', 'choice_multi', 'true_false', 'numeric', 'text', 'short_answer', 'list', 'cloze_blanks'])(
    'answer control %s has a label in both catalogs',
    async (control) => {
      const { formatMessage } = await import('../lib/i18n');
      for (const locale of ['zh-CN', 'en'] as const) {
        const key = `notetype.answer.control.${control}`;
        expect(formatMessage(locale, key)).not.toBe(key);
      }
    },
  );

  it.each([
    'answer', 'answers', 'accept', 'reference', 'text', 'value', 'unit',
    'tolerance_absolute', 'tolerance_relative', 'items', 'options', 'ordered',
    'ignore_case', 'ignore_whitespace', 'regex',
  ])(
    'answer role %s has a label in both catalogs',
    async (role) => {
      const { formatMessage } = await import('../lib/i18n');
      for (const locale of ['zh-CN', 'en'] as const) {
        const key = `notetype.answer.role.${role}`;
        expect(formatMessage(locale, key)).not.toBe(key);
      }
    },
  );
});

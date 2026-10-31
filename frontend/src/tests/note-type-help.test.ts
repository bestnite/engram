import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteTypeHelpView from '../lib/views/NoteTypeHelpView.svelte';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

const exampleIds = ['basic', 'reversed', 'optional_reverse', 'cloze', 'type_in', 'reading', 'hint', 'tts', 'choice', 'style'];

describe('NoteTypeHelpView', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('is the route of /decks/:id/note-types/help', () => {
    expect(matchRoute('/decks/d1/note-types/help').route?.name).toBe('deck-note-type-help');
    expect(matchRoute('/decks/d1/note-types').route?.name).toBe('deck-note-types');
  });

  it('explains the concepts, the steps and the full syntax that moved off the note type page', () => {
    routeStore.set(matchRoute('/decks/d1/note-types/help'));
    const { html } = render(NoteTypeHelpView);
    expect(html).toContain('笔记类型决定一条笔记有哪些字段');
    for (const term of ['note', 'field', 'type', 'template', 'card']) {
      expect(html).not.toContain(`notetype_help.concepts.${term}`);
    }
    for (const step of ['create', 'fields', 'templates', 'style', 'preview', 'use', 'tools']) {
      expect(html).toContain(`data-testid="note-type-help-step-${step}"`);
    }
    expect(html).toContain('data-testid="note-type-help-syntax"');
    expect(html).toContain('{{furigana:Expression}}');
  });

  it('shows every example with its fields, templates, sample note and result, all translated', () => {
    routeStore.set(matchRoute('/decks/d1/note-types/help'));
    const { html } = render(NoteTypeHelpView);
    for (const id of exampleIds) {
      expect(html).toContain(`data-testid="note-type-help-example-${id}"`);
    }
    // No catalog key leaks as text: each one resolves in this locale.
    expect(html).not.toMatch(/notetype_help\.[a-z_.]+/);
    // Template code is printed literally, markup included, and never interpreted.
    expect(html).toContain('{{cloze:Text}}');
    expect(html).toContain('{{#Add Reverse}}{{Back}}{{/Add Reverse}}');
    expect(html).toContain('&lt;hr id=answer>');
    expect(html).not.toContain('<hr id=answer>');
    expect(html).toContain('.card.nightMode');
    // The answer control example names the control and the field each role is bound to.
    expect(html).toContain('单选');
    expect(html).toContain('{{type:Options}}');
  });

  it('writes each sample note as field lines read from the same catalog values the preview renders', () => {
    routeStore.set(matchRoute('/decks/d1/note-types/help'));
    const { html } = render(NoteTypeHelpView);
    const sample = (id: string) => {
      const match = html.match(new RegExp(`data-testid="note-type-help-sample-${id}"><code>([\\s\\S]*?)</code>`));
      return match?.[1] ?? '';
    };
    expect(sample('basic')).toBe('Front: 法国的首都是哪里？\nBack: 巴黎');
    // A value of several lines is indented below its field, an empty field is marked, and tags come last.
    expect(sample('choice')).toBe('Question: 下面哪一个是质数？\nOptions:\n  9\n  7\n  15\nAnswer: 1');
    expect(sample('hint')).toContain('Notes: （留空）');
    expect(sample('style')).toMatch(/\n标签: 物理 热力学$/);
  });

  it('renders in English with no missing key either', () => {
    setLocale('en');
    routeStore.set(matchRoute('/decks/d1/note-types/help'));
    const { html } = render(NoteTypeHelpView);
    expect(html).toContain('Note type help');
    expect(html).not.toMatch(/notetype_help\.[a-z_.]+/);
  });

  it('returns to the note type page, or to the address it was given', () => {
    routeStore.set(matchRoute('/decks/d1/note-types/help'));
    expect(render(NoteTypeHelpView).html).toMatch(/href="\/decks\/d1\/note-types"[^>]*data-testid="note-type-help-back"|data-testid="note-type-help-back"[^>]*href="\/decks\/d1\/note-types"/);

    const from = '/decks/d1/note-types?return=%2Fdecks%2Fd1%2Fnotes%2Fnew';
    routeStore.set(matchRoute(`/decks/d1/note-types/help?return=${encodeURIComponent(from)}`));
    expect(render(NoteTypeHelpView).html).toContain(from.replaceAll('&', '&amp;'));

    routeStore.set(matchRoute(`/decks/d1/note-types/help?return=${encodeURIComponent('//example.com/x')}`));
    const { html } = render(NoteTypeHelpView);
    expect(html).not.toContain('example.com');
  });
});

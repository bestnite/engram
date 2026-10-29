import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { setCardTypes } from '../lib/card-types';
import type { Deck, Note } from '../lib/api';

const deck: Deck = {
  id: 'd1',
  name: '日语 N2 词汇',
  description: 'N2 核心词汇',
  new_per_day: 20,
  reviews_per_day: 200,
  preset_id: '1',
  created_at: '2026-01-01T00:00:00Z',
  role: 'owner',
};

function note(id: string, extra: Partial<Note> = {}): Note {
  return {
    id,
    deck_id: 'd1',
    kind: 'basic',
    fields: { front: '促す', back: 'うながす · 催促' },
    tags: ['动词', 'N2'],
    created_at: '2026-03-01T08:00:00Z',
    ...extra,
  } as Note;
}

function renderView(notes: Note[], role: Deck['role'] = 'owner') {
  return render(DeckDetailView, {
    props: { initialDeck: { ...deck, role }, initialNotes: notes, initialTotal: notes.length, initialLoading: false },
  }).html;
}

describe('DeckDetailView notes table', () => {
  beforeEach(() => {
    setLocale('zh-CN');
    setCardTypes(null);
    routeStore.set(matchRoute('/decks/d1'));
  });

  it('shows the note content as the row title instead of its id', () => {
    const id = '01a121a5-fd9e-7168-98fc-bc6ce8368020';
    const html = renderView([note(id)]);
    expect(html).toContain('促す');
    expect(html).toContain('うながす · 催促');
    // Internal IDs appear only in data-testid and links, never as visible text.
    expect(html).not.toContain(`#${id}`);
    // The editor link carries the list's address, so leaving the editor comes back to this list.
    expect(html).toContain(`href="/decks/d1/notes/${id}/edit?return=%2Fdecks%2Fd1"`);
  });

  it('renders the external reference as a labelled chip and marks suspended notes', () => {
    const html = renderView([note('n1', { external_ref: 'anki:1693042', suspended: true })]);
    expect(html).toContain('title="外部引用: anki:1693042"');
    expect(html).toContain('data-testid="note-suspended-n1"');
    expect(html).not.toContain('[anki:1693042]');
  });

  it('keeps row actions in a menu and never renders a delete confirmation inline', () => {
    const html = renderView([note('n1')]);
    expect(html).toContain('data-testid="note-menu-n1"');
    expect(html).toContain('data-testid="edit-note-n1"');
    expect(html).not.toContain('确定删除这张卡片？');
  });

  it('collapses extra tags into a count', () => {
    const html = renderView([note('n1', { tags: ['a', 'b', 'c', 'd', 'e'] })]);
    expect(html).toContain('+2');
  });

  it('lets the owner edit the deck name and description in place', () => {
    const html = renderView([note('n1')]);
    expect(html).toContain('data-testid="deck-name-edit"');
    expect(html).toContain('data-testid="deck-description-edit"');
    expect(html).toMatch(/data-testid="deck-title"[\s\S]*日语 N2 词汇/);
  });

  it('offers an "add a description" prompt to the owner when the deck has none', () => {
    const html = render(DeckDetailView, {
      props: { initialDeck: { ...deck, description: '' }, initialNotes: [], initialTotal: 0, initialLoading: false },
    }).html;
    expect(html).toContain('添加描述');
  });

  it('shows the name as plain text to a reader, without edit controls', () => {
    const html = renderView([note('n1')], 'reader');
    expect(html).toContain('日语 N2 词汇');
    expect(html).not.toContain('data-testid="deck-name-edit"');
    expect(html).not.toContain('data-testid="deck-description-edit"');
  });

  it('gives a reader no selection boxes and no edit entry', () => {
    const html = renderView([note('n1')], 'reader');
    expect(html).not.toContain('data-testid="select-note-n1"');
    expect(html).not.toContain('data-testid="bulk-select-all"');
    expect(html).not.toContain('data-testid="edit-note-n1"');
    expect(html).not.toContain('data-testid="create-note-link"');
    // Pausing is a personal action that readers can use too, so the row menu stays.
    expect(html).toContain('data-testid="note-menu-n1"');
  });
});

/**
 * Deleting a deck destroys its cards and is allowed only to the owner (the server enforces the same rule),
 * so the header entry point must follow the role the server reports.
 */
describe('DeckDetailView deck deletion entry', () => {
  beforeEach(() => {
    setLocale('zh-CN');
    setCardTypes(null);
    routeStore.set(matchRoute('/decks/d1'));
  });

  it('offers the owner a delete entry in the header', () => {
    const html = renderView([note('n1')], 'owner');
    expect(html).toContain('data-testid="deck-delete-open"');
  });

  it.each(['editor', 'reader'] as const)('hides the delete entry from a %s', (role) => {
    const html = renderView([note('n1')], role);
    expect(html).not.toContain('data-testid="deck-delete-open"');
  });

  it('keeps the actions each non-owner role may use', () => {
    const editor = renderView([note('n1')], 'editor');
    expect(editor).toContain('data-testid="deck-export-open"');
    expect(editor).toContain('data-testid="create-note-link"');
    expect(editor).toContain('data-testid="deck-tag-review-open"');
    expect(editor).toContain('data-testid="deck-start-review"');

    const reader = renderView([note('n1')], 'reader');
    expect(reader).toContain('data-testid="deck-export-open"');
    expect(reader).toContain('data-testid="deck-tag-review-open"');
    expect(reader).toContain('data-testid="deck-start-review"');
    expect(reader).not.toContain('data-testid="create-note-link"');
    expect(reader).not.toContain('data-testid="deck-delete-open"');
  });
});

import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck, Note, NoteType } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * The notes list after mount: the row summary follows the note type's field order once the types load, and the
 * resizable columns take their widths from the browser's storage.
 */
describe('DeckDetailView notes list', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const deck: Deck = {
    id: 'd1',
    name: 'Idioms',
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: 'p1',
    created_at: '',
    role: 'owner',
  };

  // The server's field map is ordered by key, so "back" comes first here as it does on the wire.
  const note: Note = {
    id: 'n1',
    deck_id: 'd1',
    note_type: 't1',
    fields: { back: '<p>meaning</p>', front: '<p>word</p>' },
    tags: [],
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    external_ref: '',
    suspended: false,
  };

  const noteType = {
    id: 't1',
    deck_id: 'd1',
    name: 'Basic',
    version: 1,
    config: { fields: [{ id: 'front', name: 'Front' }, { id: 'back', name: 'Back' }] },
    created_at: '',
    updated_at: '',
  } as unknown as NoteType;

  // happy-dom leaves window.localStorage undefined under this configuration, so each test gets an in-memory one.
  let stored: Map<string, string>;
  beforeEach(() => {
    stored = new Map();
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => stored.get(key) ?? null,
        setItem: (key: string, value: string) => void stored.set(key, String(value)),
        removeItem: (key: string) => void stored.delete(key),
        clear: () => stored.clear(),
      },
    });
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(listNoteTypes: () => Promise<{ note_types: NoteType[] }>): Promise<void> {
    setLocale('en');
    routeStore.set(matchRoute('/decks/d1'));
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listNoteTypes').mockImplementation(listNoteTypes);
    vi.spyOn(apiClient, 'getDeckNotes').mockResolvedValue({ notes: [note], total: 1, page: 1, per_page: 50 } as never);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckDetailView, { target, props: { initialDeck: deck } });
    await flush();
  }

  function rowLines(): string[] {
    const cell = target.querySelector('[data-testid="note-fields-n1"]')!;
    return [cell.querySelector('a, span')?.textContent ?? '', cell.querySelector('p')?.textContent ?? ''];
  }

  it('summarises the fields in the note type order', async () => {
    await setup(async () => ({ note_types: [noteType] }));
    expect(rowLines()).toEqual(['word', 'meaning']);
  });

  it('keeps the field map order when the note types fail to load', async () => {
    await setup(async () => {
      throw new Error('offline');
    });
    expect(rowLines()).toEqual(['meaning', 'word']);
    expect(target.querySelector('[data-testid="notes-list"]')).not.toBeNull();
  });

  it('applies stored column widths, clamped to their limits, and ignores a corrupt entry', async () => {
    window.localStorage.setItem('engram-notes-columns', JSON.stringify({ tags: 300, created: 5, ref: 'wide' }));
    await setup(async () => ({ note_types: [noteType] }));
    const list = target.querySelector<HTMLElement>('[data-testid="notes-list"]')!;
    expect(list.style.getPropertyValue('--notes-cols')).toBe('1.25rem minmax(0,1fr) 300px 88px 4.5rem');
  });

  it('resizes a column from the keyboard and stores the width', async () => {
    await setup(async () => ({ note_types: [noteType] }));
    const handle = target.querySelector<HTMLElement>('[data-testid="notes-resize-tags"]')!;
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    flushSync();
    expect(JSON.parse(window.localStorage.getItem('engram-notes-columns')!).tags).toBe(160);
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }));
    flushSync();
    expect(JSON.parse(window.localStorage.getItem('engram-notes-columns')!).tags).toBe(144);
  });
});

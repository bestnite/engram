import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  ApiClient,
  ApiClientError,
  getApiErrorMessageKey,
} from '../lib/api';
import type { NotesResponse, Note } from '../lib/api';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

describe('Deck and notes list API client and view contracts', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  describe('GET /api/v1/decks/:id/notes query and path encoding', () => {
    it('correctly constructs path for numeric deckId with default params', async () => {
      const mockResponse: NotesResponse = {
        notes: [],
        total: 0,
        page: 1,
        per_page: 50,
      };

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(mockResponse), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const res = await client.getDeckNotes('42');

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/decks/42/notes');
      expect(init?.credentials).toBe('same-origin');
      expect(res.total).toBe(0);
      expect(res.notes).toEqual([]);
    });

    it('encodes special characters in string deckId', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ notes: [], total: 0, page: 1, per_page: 50 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.getDeckNotes('deck 123/special');

      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/decks/deck%20123%2Fspecial/notes');
    });

    it('correctly serializes pagination and query filter parameters', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ notes: [], total: 0, page: 2, per_page: 20 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.getDeckNotes('10', {
        page: 2,
        per_page: 20,
        q: 'algorithm & data',
        tag: 'cs+math',
        kind: 'basic',
        status: 'active',
      });

      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toContain('/api/v1/decks/10/notes?');

      const parsedUrl = new URL(url, 'http://localhost');
      expect(parsedUrl.pathname).toBe('/api/v1/decks/10/notes');
      expect(parsedUrl.searchParams.get('page')).toBe('2');
      expect(parsedUrl.searchParams.get('per_page')).toBe('20');
      expect(parsedUrl.searchParams.get('q')).toBe('algorithm & data');
      expect(parsedUrl.searchParams.get('tag')).toBe('cs+math');
      expect(parsedUrl.searchParams.get('kind')).toBe('basic');
      expect(parsedUrl.searchParams.get('status')).toBe('active');
    });

    it('omits undefined or empty filter parameters from query string', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ notes: [], total: 0, page: 1, per_page: 50 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.getDeckNotes('5', {
        page: 1,
        q: '',
        tag: undefined,
      });

      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/decks/5/notes?page=1');
    });

    it('works identically via getNotes alias method', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ notes: [], total: 0, page: 1, per_page: 50 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.getNotes('7', { page: 3 });

      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/decks/7/notes?page=3');
    });
  });

  describe('Actual typed response fields (matching Go NoteJSON & listNotes)', () => {
    it('deserializes complete note objects with all fields and tags', async () => {
      const mockNote: Note = {
        id: '101',
        deck_id: '1',
        kind: 'basic',
        fields: {
          front: 'What is FSRS?',
          back: 'Free Spaced Repetition Scheduler',
        },
        tags: ['spaced-repetition', 'algorithm'],
        created_at: '2026-10-06T12:00:00Z',
        updated_at: '2026-10-06T12:30:00Z',
        external_ref: 'ext-note-001',
        suspended: false,
      };

      const mockResponse: NotesResponse = {
        notes: [mockNote],
        total: 1,
        page: 1,
        per_page: 50,
      };

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(mockResponse), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const res = await client.getDeckNotes('1');

      expect(res.total).toBe(1);
      expect(res.page).toBe(1);
      expect(res.per_page).toBe(50);
      expect(res.notes).toHaveLength(1);

      const item = res.notes[0]!;
      expect(item.id).toBe('101');
      expect(item.deck_id).toBe('1');
      expect(item.kind).toBe('basic');
      expect(item.fields).toEqual({
        front: 'What is FSRS?',
        back: 'Free Spaced Repetition Scheduler',
      });
      expect(item.tags).toEqual(['spaced-repetition', 'algorithm']);
      expect(item.created_at).toBe('2026-10-06T12:00:00Z');
      expect(item.updated_at).toBe('2026-10-06T12:30:00Z');
      expect(item.external_ref).toBe('ext-note-001');
    });
  });

  describe('Error handling and mapping for notes endpoint', () => {
    it('maps 401 Unauthorized to error.unauthorized', async () => {
      mockFetch.mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: 'unauthorized',
              message: 'authentication required',
            },
          }),
          { status: 401, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await expect(client.getDeckNotes('1')).rejects.toThrow(ApiClientError);

      try {
        await client.getDeckNotes('1');
      } catch (err) {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.isUnauthorized).toBe(true);
        expect(getApiErrorMessageKey(apiErr)).toBe('error.unauthorized');
      }
    });

    it('maps 403 Forbidden to error.forbidden', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'forbidden',
              message: 'access denied',
            },
          }),
          { status: 403, headers: { 'Content-Type': 'application/json' } }
        )
      );

      try {
        await client.getDeckNotes('1');
      } catch (err) {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.isForbidden).toBe(true);
        expect(getApiErrorMessageKey(apiErr)).toBe('error.forbidden');
      }
    });

    it('maps 404 Deck Not Found to error.not_found', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'not_found',
              message: 'deck not found',
            },
          }),
          { status: 404, headers: { 'Content-Type': 'application/json' } }
        )
      );

      try {
        await client.getDeckNotes('999');
      } catch (err) {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.isNotFound).toBe(true);
        expect(getApiErrorMessageKey(apiErr)).toBe('error.not_found');
      }
    });
  });

  describe('deck notes catalog keys exist in both locales', () => {
    it('ensures catalog keys for deck notes exist in both zh-CN and en with matching values', () => {
      const noteKeys = [
        'decks.view_notes',
        'notes.title',
        'notes.deck_title',
        'notes.back_to_decks',
        'notes.loading',
        'notes.empty',
        'notes.empty_filter',
        'notes.failed',
        'notes.unauthorized',
        'notes.forbidden',
        'notes.not_found',
        'notes.retry',
        'notes.search_placeholder',
        'notes.tag_placeholder',
        'notes.filter_kind',
        'notes.all_kinds',
        'notes.filter_apply',
        'notes.filter_reset',
        'notes.total_count',
        'notes.page_info',
        'notes.prev_page',
        'notes.next_page',
        'notes.col_id',
        'notes.col_kind',
        'notes.col_fields',
        'notes.col_tags',
        'notes.col_created',
        'notes.no_tags',
        'notes.status_filter',
        'notes.status_active',
        'notes.status_deleted',
        'notes.deleted_badge',
        'notes.deleted_readonly',
        'notes.select_all',
        'notes.selected_count',
        'notes.bulk_tag_placeholder',
        'notes.bulk_add_tags',
        'notes.bulk_remove_tags',
        'notes.bulk_set_tags',
        'notes.bulk_delete',
        'notes.bulk_confirm_delete',
        'notes.bulk_applying',
        'notes.bulk_result',
        'notes.bulk_skipped_not_found',
        'notes.bulk_skipped_forbidden',
        'notes.bulk_failed',
      ];

      for (const key of noteKeys) {
        expect(zhCN[key]).toBeDefined();
        expect(typeof zhCN[key]).toBe('string');
        expect(en[key]).toBeDefined();
        expect(typeof en[key]).toBe('string');
      }
    });
  });
});

describe('Deck notes deleted-status query and bulk note actions', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('requests the deleted notes listing via status=deleted', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ notes: [], total: 0, page: 1, per_page: 50 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await client.getDeckNotes('7', { status: 'deleted' });

    const [url] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/decks/7/notes?status=deleted');
  });

  it('POSTs delete with note_ids and no tags, carrying the session CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'bulk-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ dry_run: false, affected: 2, skipped: [] }), { status: 200 })
    );

    const res = await client.bulkNotes({ action: 'delete', note_ids: ['1', '2'], dry_run: false });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/notes/bulk');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(JSON.parse(String(init?.body))).toEqual({ action: 'delete', note_ids: ['1', '2'], dry_run: false });
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('bulk-csrf');
    expect(res.affected).toBe(2);
    expect(res.skipped).toEqual([]);
  });

  it('sends tags for tag actions and surfaces mixed skipped codes verbatim', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'bulk-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          dry_run: false,
          affected: 1,
          skipped: [
            { note_id: '9', code: 'not_found' },
            { note_id: '10', code: 'insufficient_role' },
          ],
        }),
        { status: 200 }
      )
    );

    const res = await client.bulkNotes({ action: 'add_tags', note_ids: ['1', '9', '10'], tags: ['math', 'cs'] });

    const [, init] = mockFetch.mock.calls[1]!;
    expect(JSON.parse(String(init?.body))).toEqual({
      action: 'add_tags',
      note_ids: ['1', '9', '10'],
      tags: ['math', 'cs'],
    });
    expect(res.affected).toBe(1);
    expect(res.skipped).toEqual([
      { note_id: '9', code: 'not_found' },
      { note_id: '10', code: 'insufficient_role' },
    ]);
  });

  it('surfaces a request-level rejection as the stable invalid_request code', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'bulk-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'invalid_request', message: 'note_ids must not be empty' } }), {
        status: 400,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await expect(client.bulkNotes({ action: 'delete', note_ids: [] })).rejects.toMatchObject({
      code: 'invalid_request',
    });
  });
});

describe('GET /api/v1/notes/:id single-note read', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('reads one note by public id and encodes the id in the path', async () => {
    const mockNote: Note = {
      id: 'note-7',
      deck_id: 'deck-3',
      kind: 'basic',
      fields: { front: 'q', back: 'a' },
      tags: [],
      created_at: '2026-10-01T00:00:00Z',
      updated_at: '2026-10-01T00:00:00Z',
      external_ref: '',
      suspended: false,
    };
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify(mockNote), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    const res = await client.getNote('note 7/x');

    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/notes/note%207%2Fx');
    expect(init?.credentials).toBe('same-origin');
    expect(res).toEqual(mockNote);
  });

  it('maps 404 to not_found for an unknown note id', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'not_found', message: 'note not found' } }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await expect(client.getNote('missing')).rejects.toMatchObject({ code: 'not_found' });
  });
});

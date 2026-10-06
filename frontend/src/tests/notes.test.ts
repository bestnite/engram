import { describe, it, expect, vi, beforeEach } from 'vitest';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
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

      const res = await client.getDeckNotes(42);

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

      await client.getDeckNotes(10, {
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

      await client.getDeckNotes(5, {
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

      await client.getNotes(7, { page: 3 });

      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/decks/7/notes?page=3');
    });
  });

  describe('Actual typed response fields (matching Go NoteJSON & listNotes)', () => {
    it('deserializes complete note objects with all fields and tags', async () => {
      const mockNote: Note = {
        id: 101,
        deck_id: 1,
        kind: 'basic',
        fields: {
          front: 'What is FSRS?',
          back: 'Free Spaced Repetition Scheduler',
        },
        tags: ['spaced-repetition', 'algorithm'],
        created_at: '2026-10-06T12:00:00Z',
        updated_at: '2026-10-06T12:30:00Z',
        external_ref: 'ext-note-001',
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

      const res = await client.getDeckNotes(1);

      expect(res.total).toBe(1);
      expect(res.page).toBe(1);
      expect(res.per_page).toBe(50);
      expect(res.notes).toHaveLength(1);

      const item = res.notes[0]!;
      expect(item.id).toBe(101);
      expect(item.deck_id).toBe(1);
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

      await expect(client.getDeckNotes(1)).rejects.toThrow(ApiClientError);

      try {
        await client.getDeckNotes(1);
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
        await client.getDeckNotes(1);
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
        await client.getDeckNotes(999);
      } catch (err) {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.isNotFound).toBe(true);
        expect(getApiErrorMessageKey(apiErr)).toBe('error.not_found');
      }
    });
  });

  describe('Strict security: sanitized preview is the only HTML sink', () => {
    it('allows HTML only from the note preview API response', () => {
      const viewsDir = fileURLToPath(new URL('../lib/views', import.meta.url));
      const viewFiles = fs.readdirSync(viewsDir).filter((f) => f.endsWith('.svelte'));

      expect(viewFiles.length).toBeGreaterThan(0);

      for (const file of viewFiles) {
        const content = fs.readFileSync(path.join(viewsDir, file), 'utf-8');
        if (file === 'NoteEditView.svelte') {
          expect(content).toContain('{@html card.front_html}');
          expect(content).toContain('{@html card.back_html}');
          expect(content).not.toContain('{@html fields');
          expect(content).not.toContain('{@html fieldsText');
        } else {
          expect(content, `File ${file} must not contain {@html} expressions`).not.toContain('{@html');
        }
      }
    });

    it('verifies that components also contain no {@html} expressions', () => {
      const compsDir = fileURLToPath(new URL('../lib/components', import.meta.url));
      const compFiles = fs.readdirSync(compsDir).filter((f) => f.endsWith('.svelte'));

      for (const file of compFiles) {
        const filePath = path.join(compsDir, file);
        const content = fs.readFileSync(filePath, 'utf-8');
        expect(
          content.includes('{@html'),
          `Component ${file} must not contain {@html} expressions`
        ).toBe(false);
      }
    });

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

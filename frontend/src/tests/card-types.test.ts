import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient } from '../lib/api';
import {
  cardTypes,
  createCatalog,
  currentCatalog,
  descriptionOf,
  loadCardTypes,
  setCardTypes,
} from '../lib/card-types';
import { get } from 'svelte/store';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { CARD_TYPES } from './card-type-fixture';

/**
 * Load contract of the metadata module: one request, idempotent caching, retryable failure; plus the consistency
 * requirement that "every card type has copy". The card type list follows the sample response, and the frontend keeps no second list.
 */
describe('card type metadata loading', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    setCardTypes(null);
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  function respondWithCatalog(): void {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify(CARD_TYPES), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );
  }

  it('fetches GET /api/v1/card-types once and exposes the server order', async () => {
    respondWithCatalog();
    const catalog = await loadCardTypes(client);

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(mockFetch.mock.calls[0]![0]).toBe('/api/v1/card-types');
    expect(catalog.kinds.map((description) => description.kind)).toEqual([
      'basic',
      'basic_both',
      'choice_multi',
      'choice_single',
      'cloze',
      'list',
      'numeric',
      'short_answer',
      'true_false',
      'typed',
    ]);
  });

  it('is idempotent: concurrent and later calls share one request and one catalog', async () => {
    respondWithCatalog();
    const [first, second] = await Promise.all([loadCardTypes(client), loadCardTypes(client)]);
    const third = await loadCardTypes(client);

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(first).toBe(second);
    expect(third).toBe(first);
    expect(currentCatalog()).toBe(first);
  });

  it('stays unloaded on failure and retries on the next call', async () => {
    mockFetch.mockRejectedValueOnce(new Error('network down'));
    await expect(loadCardTypes(client)).rejects.toBeTruthy();
    // A failure writes nothing to the store: views take the degraded path because of that, instead of taking half the metadata for a complete list.
    expect(get(cardTypes)).toBeNull();

    respondWithCatalog();
    const catalog = await loadCardTypes(client);
    expect(mockFetch).toHaveBeenCalledTimes(2);
    expect(catalog.kinds).toHaveLength(CARD_TYPES.kinds.length);
  });

  it('reads accessors from the injected catalog', () => {
    const catalog = createCatalog(CARD_TYPES.kinds);
    expect(descriptionOf(catalog, 'short_answer')?.front_field).toBe('prompt');
    expect(descriptionOf(catalog, 'short_answer')?.back_field).toBe('reference');
    expect(descriptionOf(catalog, 'nope')).toBeNull();
  });
});

describe('every kind has a catalog label', () => {
  it('provides notes.kind.<kind> in both locales for every server kind', () => {
    for (const description of CARD_TYPES.kinds) {
      const key = `notes.kind.${description.kind}`;
      // When one piece of copy is missing the UI prints a bare key, which is worse than a missing badge — so it is pinned in both language packs.
      expect(zhCN[key], `zh-CN ${key}`).toBeTruthy();
      expect(en[key], `en ${key}`).toBeTruthy();
    }
  });
});

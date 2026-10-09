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
 * 元数据模块的加载契约：一次请求、幂等缓存、失败可重试；以及「每个题型都有文案」的
 * 一致性。题型清单以样例响应为准，前端不维护第二份清单。
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
    // 失败不写 store：视图据此走降级路径，而不是把半份元数据当完整清单。
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
      // 缺一条文案时界面会印出裸 key，比少一格徽标更糟——所以在两侧语言包里都钉死。
      expect(zhCN[key], `zh-CN ${key}`).toBeTruthy();
      expect(en[key], `en ${key}`).toBeTruthy();
    }
  });
});

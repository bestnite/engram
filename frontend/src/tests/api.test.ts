import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  ApiClient,
  ApiClientError,
  getApiErrorMessageKey,
} from '../lib/api';
import type { DecksResponse, StatsSummary, DueCardsResponse } from '../lib/api';
import { formatMessage } from '../lib/i18n';

describe('Centralized typed same-origin REST API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  describe('Request boundary and same-origin credentials', () => {
    it('always sends credentials: same-origin and Accept: application/json', async () => {
      const mockResponse: DecksResponse = {
        decks: [
          {
            id: 1,
            name: '默认卡组',
            description: '系统默认卡组',
            visibility: 'private',
            new_per_day: 20,
            reviews_per_day: 100,
            preset_id: 1,
            created_at: '2026-10-06T00:00:00Z',
          },
        ],
      };

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(mockResponse), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const result = await client.getDecks();

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const call = mockFetch.mock.calls[0];
      expect(call).toBeDefined();
      const [url, init] = call!;
      expect(url).toBe('/api/v1/decks');

      // 验证同源 HttpOnly 凭据策略（DESIGN.md §8.3：绝不使用 API Key，使用 same-origin cookie）
      expect(init?.credentials).toBe('same-origin');

      // 验证请求头包含 Accept: application/json，且无 Authorization 标头
      const headers = new Headers(init?.headers);
      expect(headers.get('Accept')).toBe('application/json');
      expect(headers.has('Authorization')).toBe(false);

      // 验证返回的数据结构与类型一致
      expect(result.decks).toHaveLength(1);
      expect(result.decks[0]?.id).toBe(1);
      expect(result.decks[0]?.name).toBe('默认卡组');
    });

    it('respects optional baseUrl if configured', async () => {
      const customClient = new ApiClient({
        baseUrl: 'https://example.com',
        fetch: mockFetch as unknown as typeof fetch,
      });

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ decks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await customClient.getDecks();
      expect(mockFetch).toHaveBeenCalledWith(
        'https://example.com/api/v1/decks',
        expect.objectContaining({ credentials: 'same-origin' })
      );
    });

    it('parses empty deck list correctly', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ decks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const result = await client.getDecks();
      expect(result.decks).toEqual([]);
    });

    it('handles 204 No Content gracefully', async () => {
      mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
      const result = await client.request<void>('/api/v1/dummy');
      expect(result).toBeUndefined();
    });
  });

  describe('HTTP error code handling with Go error envelope', () => {
    it('handles 401 Unauthorized safely and parses error code', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'unauthorized',
              message: 'Authentication is required.',
            },
          }),
          {
            status: 401,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(401);
        expect(apiErr.code).toBe('unauthorized');
        expect(apiErr.isUnauthorized).toBe(true);
        expect(apiErr.isForbidden).toBe(false);
        return true;
      });
    });

    it('handles 403 Forbidden and insufficient_role errors', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'insufficient_role',
              message: 'Your role on this deck is not sufficient for this action.',
            },
          }),
          {
            status: 403,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(403);
        expect(apiErr.code).toBe('insufficient_role');
        expect(apiErr.isForbidden).toBe(true);
        return true;
      });
    });

    it('handles 409 Conflict and version_conflict errors', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'version_conflict',
              message: 'The resource was changed by someone else. Reload and try again.',
            },
          }),
          {
            status: 409,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(409);
        expect(apiErr.code).toBe('version_conflict');
        expect(apiErr.isConflict).toBe(true);
        return true;
      });
    });

    it('handles 404 Not Found error', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'not_found',
              message: 'The requested resource was not found.',
            },
          }),
          {
            status: 404,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(404);
        expect(apiErr.code).toBe('not_found');
        expect(apiErr.isNotFound).toBe(true);
        return true;
      });
    });

    it('handles 429 Rate Limited error', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'rate_limited',
              message: 'Rate limit exceeded.',
            },
          }),
          {
            status: 429,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(429);
        expect(apiErr.code).toBe('rate_limited');
        expect(apiErr.isRateLimited).toBe(true);
        return true;
      });
    });
  });

  describe('Malformed JSON and network failures fallback', () => {
    it('handles malformed JSON on 200 OK without crashing', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response('{"decks": [invalid json', {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(200);
        expect(apiErr.code).toBe('invalid_response');
        return true;
      });
    });

    it('handles non-JSON error response (e.g. HTML 502/500 gateway error) with status fallback', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response('<html><body>502 Bad Gateway</body></html>', {
          status: 502,
          headers: { 'Content-Type': 'text/html' },
        })
      );

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(502);
        expect(apiErr.code).toBe('internal_error');
        return true;
      });
    });

    it('handles network failure (fetch throws) as network_error', async () => {
      mockFetch.mockRejectedValueOnce(new TypeError('Failed to fetch'));

      await expect(client.getDecks()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(0);
        expect(apiErr.code).toBe('network_error');
        expect(apiErr.isNetworkError).toBe(true);
        return true;
      });
    });
  });

  describe('Local error message-code mapping and i18n isolation', () => {
    it('maps error codes to frontend catalog keys without exposing backend English detail', () => {
      const errUnauthorized = new ApiClientError('HTTP 401', {
        status: 401,
        code: 'unauthorized',
      });
      const keyUnauthorized = getApiErrorMessageKey(errUnauthorized);
      expect(keyUnauthorized).toBe('error.unauthorized');
      expect(formatMessage('zh-CN', keyUnauthorized)).toBe('未登录或会话已过期');
      expect(formatMessage('en', keyUnauthorized)).toBe('Authentication required');

      const errForbidden = new ApiClientError('HTTP 403', {
        status: 403,
        code: 'forbidden',
      });
      const keyForbidden = getApiErrorMessageKey(errForbidden);
      expect(keyForbidden).toBe('error.forbidden');
      expect(formatMessage('zh-CN', keyForbidden)).toBe('没有访问权限');
      expect(formatMessage('en', keyForbidden)).toBe('Access denied');

      const errConflict = new ApiClientError('HTTP 409', {
        status: 409,
        code: 'version_conflict',
      });
      const keyConflict = getApiErrorMessageKey(errConflict);
      expect(keyConflict).toBe('error.conflict');
      expect(formatMessage('zh-CN', keyConflict)).toBe('数据状态冲突，请刷新后重试');
      expect(formatMessage('en', keyConflict)).toBe('Conflict with current state, please refresh');

      const errRateLimited = new ApiClientError('HTTP 429', {
        status: 429,
        code: 'rate_limited',
      });
      const keyRateLimited = getApiErrorMessageKey(errRateLimited);
      expect(keyRateLimited).toBe('error.rate_limited');
      expect(formatMessage('zh-CN', keyRateLimited)).toBe('请求过于频繁，请稍后重试');
      expect(formatMessage('en', keyRateLimited)).toBe('Too many requests, please try again later');

      const errNotFound = new ApiClientError('HTTP 404', {
        status: 404,
        code: 'not_found',
      });
      const keyNotFound = getApiErrorMessageKey(errNotFound);
      expect(keyNotFound).toBe('error.not_found');
      expect(formatMessage('zh-CN', keyNotFound)).toBe('请求的资源不存在');
      expect(formatMessage('en', keyNotFound)).toBe('Resource not found');

      const errNetwork = new ApiClientError('Network failed', {
        status: 0,
        code: 'network_error',
      });
      const keyNetwork = getApiErrorMessageKey(errNetwork);
      expect(keyNetwork).toBe('error.network');
      expect(formatMessage('zh-CN', keyNetwork)).toBe('网络连接失败，请检查网络设置');
      expect(formatMessage('en', keyNetwork)).toBe('Network error, please check connection');

      const errInvalidResponse = new ApiClientError('Invalid JSON', {
        status: 200,
        code: 'invalid_response',
      });
      const keyInvalidResponse = getApiErrorMessageKey(errInvalidResponse);
      expect(keyInvalidResponse).toBe('error.invalid_response');
      expect(formatMessage('zh-CN', keyInvalidResponse)).toBe('服务器响应格式异常');
      expect(formatMessage('en', keyInvalidResponse)).toBe('Invalid server response');

      const errUnknown = new Error('Random JS error');
      const keyUnknown = getApiErrorMessageKey(errUnknown);
      expect(keyUnknown).toBe('error.unknown');
      expect(formatMessage('zh-CN', keyUnknown)).toBe('操作失败，请稍后重试');
      expect(formatMessage('en', keyUnknown)).toBe('Operation failed, please try again');

      const errCsrfFailed = new ApiClientError('HTTP 403', {
        status: 403,
        code: 'csrf_failed',
      });
      const keyCsrfFailed = getApiErrorMessageKey(errCsrfFailed);
      expect(keyCsrfFailed).toBe('error.csrf_failed');
      expect(formatMessage('zh-CN', keyCsrfFailed)).toBe('安全校验令牌失效，请刷新页面后重试');
      expect(formatMessage('en', keyCsrfFailed)).toBe('Security validation token expired, please refresh the page');
    });
  });

  describe('CSRF token awareness on mutating requests (DESIGN.md §4.3)', () => {
    it('manages CSRF token state', () => {
      expect(client.getCsrfToken()).toBeNull();
      client.setCsrfToken('test-csrf-token-123');
      expect(client.getCsrfToken()).toBe('test-csrf-token-123');
      client.setCsrfToken(null);
      expect(client.getCsrfToken()).toBeNull();
    });

    it('attaches X-CSRF-Token header to mutating requests (PATCH, POST) when token is configured', async () => {
      client.setCsrfToken('csrf-token-xyz');
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            profile: {
              display_name: 'Updated Name',
              locale: 'en',
              timezone: 'UTC',
              day_cutoff_hour: 4,
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await client.updateProfile({
        display_name: 'Updated Name',
        locale: 'en',
        timezone: 'UTC',
        day_cutoff_hour: 4,
      });

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/profile');
      expect(init?.method).toBe('PATCH');
      expect(init?.credentials).toBe('same-origin');

      const headers = new Headers(init?.headers);
      expect(headers.get('X-CSRF-Token')).toBe('csrf-token-xyz');
      expect(headers.get('Content-Type')).toBe('application/json');
      expect(headers.get('Accept')).toBe('application/json');
    });

    it('does not attach X-CSRF-Token on GET requests even if token is configured', async () => {
      client.setCsrfToken('csrf-token-xyz');
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ decks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.getDecks();
      const [, init] = mockFetch.mock.calls[0]!;
      const headers = new Headers(init?.headers);
      expect(headers.has('X-CSRF-Token')).toBe(false);
    });

    it('allows overriding CSRF token via options on updateProfile and updateLocale', async () => {
      client.setCsrfToken('global-token');
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ locale: 'zh-CN' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.updateLocale('zh-CN', { csrfToken: 'explicit-token' });
      const [, init] = mockFetch.mock.calls[0]!;
      const headers = new Headers(init?.headers);
      expect(headers.get('X-CSRF-Token')).toBe('explicit-token');
    });
  });

  describe('User profile and locale API methods (DESIGN.md §4.1、§8.3)', () => {
    it('getProfile fetches and unwraps user profile payload', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            profile: {
              id: 1,
              username: 'alice',
              display_name: 'Alice',
              locale: 'zh-CN',
              timezone: 'Asia/Shanghai',
              day_cutoff_hour: 4,
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      );

      const profile = await client.getProfile();
      expect(profile.display_name).toBe('Alice');
      expect(profile.locale).toBe('zh-CN');
      expect(profile.timezone).toBe('Asia/Shanghai');
      expect(profile.day_cutoff_hour).toBe(4);
    });

    it('getProfile raises ApiClientError with not_found on 404 (detects API gap)', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'not_found',
              message: 'Route not found',
            },
          }),
          { status: 404, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await expect(client.getProfile()).rejects.toSatisfy((err: unknown) => {
        return err instanceof ApiClientError && err.isNotFound && err.status === 404;
      });
    });

    it('updateProfile throws ApiClientError on invalid response format', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response('<html>502 Bad Gateway</html>', {
          status: 502,
          headers: { 'Content-Type': 'text/html' },
        })
      );

      await expect(
        client.updateProfile({
          display_name: 'Bob',
          locale: 'en',
          timezone: 'UTC',
        })
      ).rejects.toSatisfy((err: unknown) => {
        return err instanceof ApiClientError && err.status === 502 && err.code === 'internal_error';
      });
    });

    it('updateLocale issues PATCH request with locale payload', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ locale: 'en' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const res = await client.updateLocale('en');
      expect(res.locale).toBe('en');

      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/settings/locale');
      expect(init?.method).toBe('PATCH');
      expect(JSON.parse(init?.body as string)).toEqual({ locale: 'en' });
    });
  });

  describe('GET /api/v1/stats/summary data wiring and contract fixtures', () => {
    it('requests GET /api/v1/stats/summary with same-origin credentials and parses Go fixture', async () => {
      // 捕获自 Go 测试 visible_scope_test.go:TestStatsVisibleContentButFullReviewHistory 的真实响应形态
      const goFixture: StatsSummary = {
        decks: 1,
        notes: 1,
        cards: 1,
        due: 1,
        reviews_today: 2,
        reviews_total: 2,
        retention: 0.5,
      };

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(goFixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const result = await client.getStatsSummary();

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/stats/summary');
      expect(init?.credentials).toBe('same-origin');
      const headers = new Headers(init?.headers);
      expect(headers.get('Accept')).toBe('application/json');
      expect(headers.has('Authorization')).toBe(false);

      expect(result).toEqual(goFixture);
      expect(result.decks).toBe(1);
      expect(result.due).toBe(1);
      expect(result.reviews_today).toBe(2);
      expect(result.reviews_total).toBe(2);
      expect(result.retention).toBe(0.5);
    });

    it('correctly handles empty/revoked stats fixture without fabricating values', async () => {
      const revokedFixture: StatsSummary = {
        decks: 0,
        notes: 0,
        cards: 0,
        due: 0,
        reviews_today: 0,
        reviews_total: 0,
        retention: 0,
      };

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(revokedFixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const result = await client.getStatsSummary();
      expect(result).toEqual(revokedFixture);
      expect(result.decks).toBe(0);
      expect(result.due).toBe(0);
      expect(result.retention).toBe(0);
    });

    it('handles 401 Unauthorized for stats/summary', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: { code: 'unauthorized', message: 'Authentication required' },
          }),
          { status: 401, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await expect(client.getStatsSummary()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(401);
        expect(apiErr.code).toBe('unauthorized');
        expect(apiErr.isUnauthorized).toBe(true);
        return true;
      });
    });

    it('handles 403 Forbidden / insufficient scope for stats/summary', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: { code: 'insufficient_role', message: 'Scope required' },
          }),
          { status: 403, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await expect(client.getStatsSummary()).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(403);
        expect(apiErr.isForbidden).toBe(true);
        return true;
      });
    });
  });

  describe('GET /api/v1/review/due query semantics and contract fixtures', () => {
    it('requests GET /api/v1/review/due with no query params when query is omitted', async () => {
      const fixture: DueCardsResponse = { cards: [] };
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(fixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const result = await client.getDueCards();

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/review/due');
      expect(init?.credentials).toBe('same-origin');
      expect(result.cards).toEqual([]);
    });

    it('appends repeated deck query params matching Go Gin c.QueryArray("deck") semantics', async () => {
      // 对应 Go 测试 review_scope_test.go:TestDueCardsAcceptsRepeatedDeckParams
      const goFixture: DueCardsResponse = {
        cards: [
          {
            card_id: 101,
            note_id: 201,
            deck_id: 1,
            state: 'review',
            due_at: '2026-10-06T00:00:00Z',
            retrievability: 0.88,
            kind: 'basic',
            fields: { front: 'question 1', back: 'answer 1' },
            tags: ['biology'],
            template: 'forward',
          },
          {
            card_id: 102,
            note_id: 202,
            deck_id: 2,
            state: 'new',
            due_at: '2026-10-06T00:00:00Z',
            retrievability: 0,
            kind: 'basic',
            fields: { front: 'question 2', back: 'answer 2' },
            tags: ['chemistry'],
          },
        ],
      };

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify(goFixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      const result = await client.getDueCards({ deck: [1, 2], limit: 50 });

      expect(mockFetch).toHaveBeenCalledTimes(1);
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/review/due?deck=1&deck=2&limit=50');
      expect(init?.credentials).toBe('same-origin');
      expect(result.cards).toHaveLength(2);
      expect(result.cards[0]?.card_id).toBe(101);
      expect(result.cards[0]?.deck_id).toBe(1);
      expect(result.cards[0]?.fields).toEqual({ front: 'question 1', back: 'answer 1' });
      expect(result.cards[1]?.card_id).toBe(102);
      expect(result.cards[1]?.deck_id).toBe(2);
    });

    it('handles single deck param correctly', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ cards: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.getDueCards({ deck: 42 });

      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/review/due?deck=42');
    });

    it('handles 400 Bad Request when deck param is invalid (reproducing TestDueCardsRejectsBadDeckParam)', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: { code: 'invalid_request', message: 'Invalid deck ID' },
          }),
          { status: 400, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await expect(client.getDueCards({ deck: 0 })).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(400);
        expect(apiErr.code).toBe('invalid_request');
        return true;
      });
    });

    it('handles 403 Forbidden when deck is unreadable (reproducing TestDueCardsFailsWholeRequestForUnreadableDeck)', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: { code: 'forbidden', message: 'You do not have access to this deck' },
          }),
          { status: 403, headers: { 'Content-Type': 'application/json' } }
        )
      );

      await expect(client.getDueCards({ deck: 999 })).rejects.toSatisfy((err: unknown) => {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(403);
        expect(apiErr.code).toBe('forbidden');
        expect(apiErr.isForbidden).toBe(true);
        return true;
      });
    });
  });
});

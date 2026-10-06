import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  ApiClient,
  ApiClientError,
  getApiErrorMessageKey,
} from '../lib/api';
import type { DecksResponse } from '../lib/api';
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

      const errCsrf = new ApiClientError('HTTP 403', {
        status: 403,
        code: 'csrf_failed',
      });
      const keyCsrf = getApiErrorMessageKey(errCsrf);
      expect(keyCsrf).toBe('error.csrf_failed');
      expect(errCsrf.isCsrfError).toBe(true);

      const errCreds = new ApiClientError('HTTP 401', {
        status: 401,
        code: 'invalid_credentials',
      });
      const keyCreds = getApiErrorMessageKey(errCreds);
      expect(keyCreds).toBe('error.invalid_credentials');
      expect(errCreds.isInvalidCredentials).toBe(true);

      const errDisabled = new ApiClientError('HTTP 401', {
        status: 401,
        code: 'user_disabled',
      });
      const keyDisabled = getApiErrorMessageKey(errDisabled);
      expect(keyDisabled).toBe('error.user_disabled');
      expect(errDisabled.isUserDisabled).toBe(true);

      const errTotp = new ApiClientError('HTTP 401', {
        status: 401,
        code: 'totp_required',
      });
      const keyTotp = getApiErrorMessageKey(errTotp);
      expect(keyTotp).toBe('error.totp_required');
      expect(errTotp.isTotpRequired).toBe(true);
    });
  });

  describe('Session bootstrap, CSRF token management and mutating requests', () => {
    it('bootstraps unauthenticated session safely and stores CSRF token in memory', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            authenticated: false,
            user: null,
            csrf_token: 'double_submit_token_123',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      const session = await client.getSession();
      expect(session.authenticated).toBe(false);
      expect(session.user).toBeNull();
      expect(session.csrf_token).toBe('double_submit_token_123');

      // 验证客户端内存中持有了该 CSRF token
      expect(client.getCsrfToken()).toBe('double_submit_token_123');

      // 验证请求使用了 same-origin credentials 且无 Authorization 标头
      const [, init] = mockFetch.mock.calls[0]!;
      expect(init?.credentials).toBe('same-origin');
      const headers = new Headers(init?.headers);
      expect(headers.has('Authorization')).toBe(false);
    });

    it('bootstraps authenticated session and updates session-bound CSRF token', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            authenticated: true,
            user: {
              id: 1,
              username: 'alice',
              email: 'alice@example.com',
              role: 'user',
              locale: 'zh-CN',
            },
            csrf_token: 'session_csrf_token_456',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      const session = await client.getSession();
      expect(session.authenticated).toBe(true);
      expect(session.user?.username).toBe('alice');
      expect(session.csrf_token).toBe('session_csrf_token_456');
      expect(client.getCsrfToken()).toBe('session_csrf_token_456');
    });

    it('attaches X-CSRF-Token automatically on mutating POST requests but not GET', async () => {
      client.setCsrfToken('active_csrf_token_789');

      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

      await client.request('/api/v1/decks', {
        method: 'POST',
        body: JSON.stringify({ name: 'New Deck' }),
      });

      const [, postInit] = mockFetch.mock.calls[0]!;
      const postHeaders = new Headers(postInit?.headers);
      expect(postHeaders.get('X-CSRF-Token')).toBe('active_csrf_token_789');
      expect(postHeaders.get('Content-Type')).toBe('application/json');
      expect(postHeaders.has('Authorization')).toBe(false);

      // 验证 GET 请求不自动携带 X-CSRF-Token
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ decks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );
      await client.getDecks();
      const [, getInit] = mockFetch.mock.calls[1]!;
      const getHeaders = new Headers(getInit?.headers);
      expect(getHeaders.has('X-CSRF-Token')).toBe(false);
    });

    it('handles mutating CSRF rejection (403 csrf_failed)', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'csrf_failed',
              message: 'Invalid or missing CSRF token.',
            },
          }),
          {
            status: 403,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      try {
        await client.request('/api/v1/dummy', { method: 'POST' });
        expect.unreachable('should have thrown ApiClientError');
      } catch (err) {
        expect(err).toBeInstanceOf(ApiClientError);
        const apiErr = err as ApiClientError;
        expect(apiErr.status).toBe(403);
        expect(apiErr.code).toBe('csrf_failed');
        expect(apiErr.isCsrfError).toBe(true);
        expect(apiErr.isForbidden).toBe(true);
      }
    });
  });

  describe('SPA login and logout methods', () => {
    it('executes login flow, prefetches CSRF if needed, and stores session CSRF token', async () => {
      // 1. 模拟未获取 CSRF token 时自动调用 getSession
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            authenticated: false,
            user: null,
            csrf_token: 'bootstrap_double_csrf',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      // 2. 模拟 POST /api/v1/auth/login 响应成功
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            authenticated: true,
            user: {
              id: 2,
              username: 'bob',
              email: 'bob@example.com',
              role: 'user',
              locale: 'en',
            },
            csrf_token: 'new_session_csrf_token',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      const loginRes = await client.login({ username: 'bob', password: 'Password123!' });
      expect(loginRes.authenticated).toBe(true);
      expect(loginRes.user?.username).toBe('bob');
      expect(client.getCsrfToken()).toBe('new_session_csrf_token');

      // 验证两次调用：先 GET /api/v1/auth/session 再 POST /api/v1/auth/login
      expect(mockFetch).toHaveBeenCalledTimes(2);
      const [loginUrl, loginInit] = mockFetch.mock.calls[1]!;
      expect(loginUrl).toBe('/api/v1/auth/login');
      expect(loginInit?.method).toBe('POST');
      const loginHeaders = new Headers(loginInit?.headers);
      expect(loginHeaders.get('X-CSRF-Token')).toBe('bootstrap_double_csrf');
      expect(loginHeaders.has('Authorization')).toBe(false);
      expect(loginInit?.credentials).toBe('same-origin');
    });

    it('handles login failure on invalid credentials', async () => {
      client.setCsrfToken('existing_token');

      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'invalid_credentials',
              message: 'Invalid username or password.',
            },
          }),
          {
            status: 401,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      await expect(client.login({ username: 'bob', password: 'wrong' })).rejects.toThrow(
        ApiClientError
      );
    });

    it('handles TOTP required challenge on login', async () => {
      client.setCsrfToken('existing_token');

      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            requires_totp: true,
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      const res = await client.login({ username: 'totp_user', password: 'correct' });
      expect(res.requires_totp).toBe(true);
      expect(res.authenticated).toBeUndefined();
    });

    it('executes logout flow and updates CSRF token to fresh double-submit token', async () => {
      client.setCsrfToken('logged_in_session_csrf');

      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            authenticated: false,
            csrf_token: 'fresh_double_submit_csrf',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );

      const logoutRes = await client.logout();
      expect(logoutRes.authenticated).toBe(false);
      expect(logoutRes.csrf_token).toBe('fresh_double_submit_csrf');
      expect(client.getCsrfToken()).toBe('fresh_double_submit_csrf');

      const [logoutUrl, logoutInit] = mockFetch.mock.calls[0]!;
      expect(logoutUrl).toBe('/api/v1/auth/logout');
      expect(logoutInit?.method).toBe('POST');
      const logoutHeaders = new Headers(logoutInit?.headers);
      expect(logoutHeaders.get('X-CSRF-Token')).toBe('logged_in_session_csrf');
      expect(logoutHeaders.has('Authorization')).toBe(false);
      expect(logoutInit?.credentials).toBe('same-origin');
    });
  });

  describe('Token isolation and no storage leaks', () => {
    it('never touches or leaks any token to localStorage or sessionStorage', async () => {
      // 验证 localStorage 与 sessionStorage 均未被读写
      if (typeof window !== 'undefined') {
        expect(window.localStorage.length).toBe(0);
        expect(window.sessionStorage.length).toBe(0);
      }

      client.setCsrfToken('secret_token_value');
      await client.request('/api/v1/dummy', { method: 'POST' }).catch(() => {});

      if (typeof window !== 'undefined') {
        expect(window.localStorage.length).toBe(0);
        expect(window.sessionStorage.length).toBe(0);
        expect(window.localStorage.getItem('token')).toBeNull();
        expect(window.localStorage.getItem('csrf_token')).toBeNull();
        expect(window.localStorage.getItem('session')).toBeNull();
        expect(window.sessionStorage.getItem('token')).toBeNull();
      }
    });
  });
});

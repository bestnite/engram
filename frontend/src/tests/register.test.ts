import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient, ApiClientError, apiClient, getApiErrorMessageKey } from '../lib/api';
import { register, setup } from '../lib/auth';

// SPA 注册与首个管理员引导的前端契约：同源 JSON、写请求自动带内存 CSRF、稳定 code 映射本地化。
// 服务端行为（策略、邀请、引导门）由 internal/web/auth_register_test.go 覆盖。
describe('SPA register and setup client flows', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('loads session CSRF then posts the register body to /api/v1/auth/register', async () => {
    mockFetch
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ authenticated: false, user: null, csrf_token: 'reg-csrf' }), {
          status: 200,
        })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ created: true }), { status: 200 }));

    const res = await client.register({
      username: 'alice',
      email: 'alice@example.com',
      display_name: 'Alice',
      password: 'Sup3rSecret!',
      invite: 'tok-123',
    });

    expect(res).toEqual({ created: true });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/auth/register');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('reg-csrf');
    expect(JSON.parse(String(init?.body))).toEqual({
      username: 'alice',
      email: 'alice@example.com',
      display_name: 'Alice',
      password: 'Sup3rSecret!',
      invite: 'tok-123',
    });
  });

  it('loads session CSRF then posts the setup body to /api/v1/auth/setup', async () => {
    mockFetch
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ authenticated: false, user: null, csrf_token: 'setup-csrf' }), {
          status: 200,
        })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ created: true }), { status: 200 }));

    const res = await client.setup({ username: 'root', email: '', password: 'Sup3rSecret!' });

    expect(res).toEqual({ created: true });
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/auth/setup');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('setup-csrf');
  });

  it('probes the registration policy from the anonymous read-only endpoint', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ policy: 'closed' }), { status: 200 }));

    await expect(client.registrationInfo()).resolves.toEqual({ policy: 'closed' });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/registration');
    expect(mockFetch.mock.calls[0]?.[1]?.method).toBeUndefined();
  });

  it('keeps the server stable code on registration failure', async () => {
    client.setCsrfToken('reg-csrf');
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'registration_closed', message: 'closed' } }), {
        status: 403,
      })
    );

    await expect(
      client.register({ username: 'x', email: 'x@example.com', password: 'Sup3rSecret!' })
    ).rejects.toMatchObject({ status: 403, code: 'registration_closed' });
  });

  it('maps registration codes to auth.error.<code> catalog keys', () => {
    const codes = [
      'username_required',
      'email_required',
      'email_invalid',
      'password_required',
      'password_too_short',
      'password_too_long',
      'password_too_common',
      'email_domain_not_allowed',
      'invite_required',
      'invite_invalid',
      'registration_closed',
      'create_failed',
    ];
    for (const code of codes) {
      const err = new ApiClientError(`HTTP 400: ${code}`, { status: 400, code });
      expect(getApiErrorMessageKey(err)).toBe(`auth.error.${code}`);
    }
  });

  it('exposes register/setup helpers that delegate to the singleton client', async () => {
    vi.spyOn(apiClient, 'register').mockResolvedValueOnce({ created: true });
    vi.spyOn(apiClient, 'setup').mockResolvedValueOnce({ created: true });
    await expect(register({ username: 'a', email: 'a@example.com', password: 'Sup3rSecret!' })).resolves.toEqual({
      created: true,
    });
    await expect(setup({ username: 'root', password: 'Sup3rSecret!' })).resolves.toEqual({ created: true });
  });
});

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import { ApiClient, ApiClientError } from '../lib/api';
import { getAccountErrorMessageKey } from '../lib/api/account-errors';
import { matchRoute } from '../lib/router';
import ForgotPasswordView from '../lib/views/ForgotPasswordView.svelte';
import ResetPasswordView from '../lib/views/ResetPasswordView.svelte';
import EmailSettingsView from '../lib/views/EmailSettingsView.svelte';
import VerifyEmailView from '../lib/views/VerifyEmailView.svelte';
import ConfirmEmailChangeView from '../lib/views/ConfirmEmailChangeView.svelte';
import { setLocale } from '../lib/i18n';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

// 账号安全与邮件流程前端契约：同源 JSON、写请求自动带内存 CSRF、稳定 code 映射本地化、
// 新路由可解析、视图文案来自语言包。服务端行为由 internal/web/account_test.go 覆盖。
describe('SPA account security and email client flows', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('loads session CSRF then posts the forgot-password body', async () => {
    mockFetch
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ authenticated: false, user: null, csrf_token: 'forgot-csrf' }), { status: 200 })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ mail_ready: true }), { status: 200 }));

    const res = await client.requestPasswordReset({ email: 'a@example.com' });

    expect(res).toEqual({ mail_ready: true });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/auth/forgot-password');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('forgot-csrf');
    expect(JSON.parse(String(init?.body))).toEqual({ email: 'a@example.com' });
  });

  it('posts the reset token and password to /api/v1/auth/reset-password', async () => {
    client.setCsrfToken('reset-csrf');
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ reset: true }), { status: 200 }));

    const res = await client.resetPassword({ token: 'tok-1', password: 'N3wSup3rSecret!' });

    expect(res).toEqual({ reset: true });
    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/auth/reset-password');
    expect(JSON.parse(String(init?.body))).toEqual({ token: 'tok-1', password: 'N3wSup3rSecret!' });
  });

  it('posts the verify and confirm-change tokens to their auth endpoints', async () => {
    client.setCsrfToken('tok-csrf');
    mockFetch
      .mockResolvedValueOnce(new Response(JSON.stringify({ verified: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ changed: true }), { status: 200 }));

    await expect(client.verifyEmail('v-token')).resolves.toEqual({ verified: true });
    await expect(client.confirmEmailChange('c-token')).resolves.toEqual({ changed: true });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/verify-email');
    expect(JSON.parse(String(mockFetch.mock.calls[0]![1]?.body))).toEqual({ token: 'v-token' });
    expect(mockFetch.mock.calls[1]?.[0]).toBe('/api/v1/auth/confirm-email-change');
    expect(JSON.parse(String(mockFetch.mock.calls[1]![1]?.body))).toEqual({ token: 'c-token' });
  });

  it('reads and writes the account email over the settings/email endpoints', async () => {
    client.setCsrfToken('email-csrf');
    mockFetch
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ email: 'a@example.com', email_verified: false, mail_ready: true }), { status: 200 })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ sent: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ sent: true }), { status: 200 }));

    const settings = await client.getEmailSettings();
    expect(settings).toEqual({ email: 'a@example.com', email_verified: false, mail_ready: true });
    await expect(client.requestEmailChange({ email: 'b@example.com' })).resolves.toEqual({ sent: true });
    await expect(client.resendVerification()).resolves.toEqual({ sent: true });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/settings/email');
    const [changeUrl, changeInit] = mockFetch.mock.calls[1]!;
    expect(changeUrl).toBe('/api/v1/settings/email');
    expect(JSON.parse(String(changeInit?.body))).toEqual({ email: 'b@example.com' });
    expect(mockFetch.mock.calls[2]?.[0]).toBe('/api/v1/settings/verify-email');
  });

  it('maps account codes to account.error.<code> catalog keys', () => {
    const codes = [
      'token_expired',
      'token_used',
      'token_invalid',
      'mail_not_configured',
      'email_invalid',
      'email_same',
      'email_taken',
      'email_conflict',
      'password_required',
      'password_too_short',
      'password_too_long',
      'password_too_common',
      'rate_limited',
    ];
    for (const code of codes) {
      const err = new ApiClientError(`HTTP 400: ${code}`, { status: 400, code });
      expect(getAccountErrorMessageKey(err)).toBe(`account.error.${code}`);
    }
  });

  it('falls back to a 401 key for a rejected session and never exposes the server message', () => {
    const err = new ApiClientError('HTTP 401: unauthorized', { status: 401, code: 'unauthorized' });
    expect(getAccountErrorMessageKey(err)).toBe('error.unauthorized');
  });

  it('resolves the account and email routes', () => {
    expect(matchRoute('/forgot-password').route?.name).toBe('forgot-password');
    expect(matchRoute('/reset-password').route?.name).toBe('reset-password');
    expect(matchRoute('/settings/email').route?.name).toBe('email-settings');
    expect(matchRoute('/verify-email').route?.name).toBe('verify-email');
    expect(matchRoute('/confirm-email-change').route?.name).toBe('confirm-email-change');
  });
});

describe('account views render localized copy from the catalog', () => {
  beforeEach(() => {
    setLocale('en');
  });

  it('renders the forgot-password form from the catalog', () => {
    const { html } = render(ForgotPasswordView);
    expect(html).toContain(en['account.forgot.heading']);
    expect(html).toContain(en['account.forgot.submit']);
    expect(html).toContain('name="email"');
  });

  it('renders the reset-password invalid state when no token is present', () => {
    const { html } = render(ResetPasswordView);
    expect(html).toContain(en['account.error.token_invalid']);
  });

  it('renders the email settings loading state before data arrives', () => {
    const { html } = render(EmailSettingsView);
    expect(html).toContain(en['settings.mail.heading']);
    expect(html).toContain(en['account.email.loading']);
  });

  it('renders the verify and confirm-change checking states', () => {
    expect(render(VerifyEmailView).html).toContain(en['account.verify.checking']);
    expect(render(ConfirmEmailChangeView).html).toContain(en['account.confirm_change.checking']);
  });

  it('has both catalogues covering every account.* key used by the views', () => {
    const accountKeys = Object.keys(en).filter((k) => k.startsWith('account.'));
    expect(accountKeys.length).toBeGreaterThan(40);
    for (const key of accountKeys) {
      expect(zhCN[key]).toBeTruthy();
    }
  });
});

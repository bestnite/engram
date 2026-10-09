import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import TOTPView from '../lib/views/TOTPView.svelte';
import { ApiClient, ApiClientError } from '../lib/api';
import type { TOTPStatus } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const disabled: TOTPStatus = { enabled: false, pending: false, recovery_remaining: 0 };
const enabled: TOTPStatus = { enabled: true, pending: false, recovery_remaining: 7 };

describe('TOTPView renders the account-security states truthfully', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the disabled state with the start button only', () => {
    const { html } = render(TOTPView, {
      props: { initialLoading: false, initialStatus: disabled },
    });
    expect(html).toContain('data-testid="totp-begin"');
    expect(html).toContain('未启用');
    expect(html).not.toContain('data-testid="totp-disable"');
    expect(html).not.toContain('data-testid="totp-recovery"');
  });

  it('shows the restart hint when an unconfirmed setup already exists', () => {
    const { html } = render(TOTPView, {
      props: { initialLoading: false, initialStatus: { ...disabled, pending: true } },
    });
    expect(html).toContain('data-testid="totp-pending-hint"');
    expect(html).not.toContain('data-testid="totp-pending"');
  });

  it('renders the pending secret and otpauth link exactly once, from props', () => {
    const { html } = render(TOTPView, {
      props: {
        initialLoading: false,
        initialStatus: { ...disabled, pending: true },
        initialSecret: 'JBSWY3DPEHPK3PXP',
        initialOtpauthUrl: 'otpauth://totp/Engram:owner?secret=JBSWY3DPEHPK3PXP',
      },
    });
    expect(html).toContain('data-testid="totp-pending"');
    expect(html).toContain('data-testid="totp-secret"');
    expect(html).toContain('JBSWY3DPEHPK3PXP');
    expect(html).toContain('data-testid="totp-otpauth"');
    expect(html).toContain('data-testid="totp-confirm-form"');
    expect(html).toContain('data-testid="totp-confirm-code"');
  });

  it('renders the enabled state with disable and recovery controls', () => {
    const { html } = render(TOTPView, {
      props: { initialLoading: false, initialStatus: enabled },
    });
    expect(html).toContain('data-testid="totp-disable"');
    expect(html).toContain('data-testid="totp-disable-password"');
    expect(html).toContain('data-testid="totp-recovery"');
    expect(html).toContain('data-testid="totp-recovery-remaining"');
    // 剩余恢复码是服务端计数，不是猜出来的常量。
    expect(html).toContain('剩余恢复码：7');
    expect(html).toContain('data-testid="totp-recovery-submit"');
    expect(html).not.toContain('data-testid="totp-begin"');
  });

  it('shows generated recovery codes only when present', () => {
    const { html } = render(TOTPView, {
      props: {
        initialLoading: false,
        initialStatus: enabled,
        initialRecoveryCodes: ['ABCDE-FGHIJ', 'KLMNO-PQRST'],
      },
    });
    expect(html).toContain('data-testid="totp-recovery-codes"');
    expect(html).toContain('ABCDE-FGHIJ');
    expect(html).toContain('KLMNO-PQRST');
  });

  it('renders the loading state before any data arrives', () => {
    const { html } = render(TOTPView, { props: { initialLoading: true } });
    expect(html).toContain('data-testid="totp-loading"');
    expect(html).not.toContain('data-testid="totp-begin"');
  });

  it('renders an unauthorized state for an anonymous session', () => {
    const err = new ApiClientError('HTTP 401', { status: 401, code: 'unauthorized' });
    const { html } = render(TOTPView, {
      props: { initialLoading: false, initialError: err },
    });
    expect(html).toContain('data-testid="totp-failed"');
    expect(html).toContain('data-testid="totp-retry"');
    expect(html).not.toContain('data-testid="totp-begin"');
  });
});

describe('TOTP API client uses session-only, CSRF-protected endpoints', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs the status endpoint without secret material', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(enabled), { status: 200 }));
    await expect(client.getTOTPStatus()).resolves.toEqual(enabled);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/settings/totp');
    expect(mockFetch.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' });
  });

  it('POSTs begin with the session CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'totp-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ secret: 'ABC', otpauth_url: 'otpauth://totp/x', pending: true }), { status: 200 })
    );
    const res = await client.beginTOTP();
    expect(res.secret).toBe('ABC');
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/settings/totp/begin');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('totp-csrf');
  });

  it('POSTs confirm with the code and returns one-time recovery codes', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'totp-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ enabled: true, recovery_codes: ['A-B'], recovery_remaining: 1 }), { status: 200 })
    );
    const res = await client.confirmTOTP('123456');
    expect(res.recovery_codes).toEqual(['A-B']);
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/settings/totp/confirm');
    expect(JSON.parse(String(init?.body))).toEqual({ code: '123456' });
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('totp-csrf');
  });

  it('POSTs disable and recovery with the password and CSRF header', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'totp-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ enabled: false }), { status: 200 }));
    await client.disableTOTP('Sup3rSecret!');
    const [disableUrl, disableInit] = mockFetch.mock.calls[1]!;
    expect(disableUrl).toBe('/api/v1/settings/totp/disable');
    expect(JSON.parse(String(disableInit?.body))).toEqual({ password: 'Sup3rSecret!' });
    expect(new Headers(disableInit?.headers).get('X-CSRF-Token')).toBe('totp-csrf');

    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ recovery_codes: ['C-D'], recovery_remaining: 1 }), { status: 200 })
    );
    await client.regenerateTOTPRecovery('Sup3rSecret!');
    const [recoveryUrl, recoveryInit] = mockFetch.mock.calls[2]!;
    expect(recoveryUrl).toBe('/api/v1/settings/totp/recovery');
    expect(JSON.parse(String(recoveryInit?.body))).toEqual({ password: 'Sup3rSecret!' });
    expect(new Headers(recoveryInit?.headers).get('X-CSRF-Token')).toBe('totp-csrf');
  });
});

describe('TOTP lives on the security page', () => {
  it('resolves /settings/security and drops the old /settings/totp page', () => {
    expect(matchRoute('/settings/security', routes).route?.name).toBe('security-settings');
    expect(matchRoute('/settings/totp', routes).route).toBeNull();
    // 个人设置页本身不能被两步验证子页的路径吃掉。
    expect(matchRoute('/settings', routes).route?.name).toBe('settings');
  });
});

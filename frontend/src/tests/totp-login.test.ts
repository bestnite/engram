import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import TOTPLoginView from '../lib/views/TOTPLoginView.svelte';
import { setLocale } from '../lib/i18n';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

/**
 * The SPA view and route for login step two (TOTP): /login/totp is the canonical path (the
 * server registers no GET route for it and NoRoute falls back to the app shell), and the
 * protocol is GET/POST /api/v1/auth/totp.
 */
describe('TOTPLoginView renders the second-factor states truthfully', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the code form only when a challenge is pending', () => {
    const { html } = render(TOTPLoginView, { props: { initialPending: true } });
    expect(html).toContain('data-testid="totp-login-code"');
    expect(html).toContain('data-testid="totp-login-submit"');
    expect(html).toContain('两步验证');
    expect(html).toContain('验证码');
    expect(html).not.toContain('data-testid="totp-login-expired"');
    // The recovery-code hint shares one input with the verification code, so the copy has to say so.
    expect(html).toContain('恢复码');
  });

  it('renders the expired notice and a way back to sign in when no challenge exists', () => {
    const { html } = render(TOTPLoginView, { props: { initialPending: false } });
    expect(html).toContain('data-testid="totp-login-expired"');
    expect(html).toContain('href="/login"');
    expect(html).toContain('重新登录');
    expect(html).not.toContain('data-testid="totp-login-code"');
  });

  it('shows the checking state while the challenge is being queried', () => {
    const { html } = render(TOTPLoginView, { props: { initialPending: null } });
    expect(html).toContain('data-testid="totp-login-checking"');
    expect(html).not.toContain('data-testid="totp-login-code"');
  });
});

describe('the second-factor route table', () => {
  it('serves /login/totp', () => {
    const canonical = matchRoute('/login/totp', routes);
    expect(canonical.route?.name).toBe('totp-login');

    // The step-one login route must not be shadowed by step two.
    expect(matchRoute('/login', routes).route?.name).toBe('login');
  });
});

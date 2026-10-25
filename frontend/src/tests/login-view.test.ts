import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import LoginView from '../lib/views/LoginView.svelte';
import { setLocale } from '../lib/i18n';

// The login page's entry point to self-service registration, decided by the site policy.
// The policy probe itself (GET /api/v1/auth/registration) is covered by register.test.ts and the Go handler tests;
// this file only verifies "which policies render the link" on the first paint.
describe('LoginView register link', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('links to registration when the policy is open', () => {
    const { html } = render(LoginView, { props: { initialPolicy: 'open' } });

    expect(html).toContain('data-testid="login-to-register"');
    expect(html).toContain('href="/register"');
    expect(html).toContain('还没有账号？立即注册');
    // The login form itself is unaffected by the policy.
    expect(html).toContain('data-testid="login-submit"');
  });

  it('renders no link when the policy is closed', () => {
    const { html } = render(LoginView, { props: { initialPolicy: 'closed' } });

    expect(html).not.toContain('data-testid="login-to-register"');
    expect(html).toContain('data-testid="login-submit"');
  });

  it('renders no link when the policy is invite', () => {
    // An invite-only site has no usable generic register link: the only way in is a token the visitor already holds.
    const { html } = render(LoginView, { props: { initialPolicy: 'invite' } });

    expect(html).not.toContain('data-testid="login-to-register"');
    expect(html).toContain('data-testid="login-submit"');
  });

  it('links to registration when the probe failed and the policy is unknown', () => {
    const { html } = render(LoginView, { props: { initialPolicy: 'unknown' } });

    expect(html).toContain('data-testid="login-to-register"');
    expect(html).toContain('href="/register"');
    expect(html).toContain('data-testid="login-submit"');
  });

  it('renders no link on the first frame while the policy is still being probed', () => {
    // Hiding the link until the probe settles stops a closed site from flashing a link that leads to a blocked page.
    const { html } = render(LoginView, { props: { initialPolicy: 'probing' } });

    expect(html).not.toContain('data-testid="login-to-register"');
    expect(html).toContain('data-testid="login-submit"');
  });
});
import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import TOTPLoginView from '../lib/views/TOTPLoginView.svelte';
import { setLocale } from '../lib/i18n';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

/**
 * 登录第二步（TOTP）的 SPA 视图与路由：/login/totp 是规范路径（服务端没有为它注册 GET 路由，
 * 由 NoRoute 回退到应用壳），协议为 GET/POST /api/v1/auth/totp。
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
    // 恢复码提示与验证码共用同一输入框，文案必须说出来。
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

    // 第一步登录路由不能被第二步遮蔽。
    expect(matchRoute('/login', routes).route?.name).toBe('login');
  });
});

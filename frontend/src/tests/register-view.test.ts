import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import RegisterView from '../lib/views/RegisterView.svelte';
import SetupView from '../lib/views/SetupView.svelte';
import { setLocale } from '../lib/i18n';

// 注册视图的两种首屏形态与确认密码字段。
// 策略探测本身（GET /api/v1/auth/registration）由 register.test.ts 的客户端契约与
// internal/web/auth_register_test.go 的 TestRegistrationInfoEndpoint 分别覆盖；
// 这里只验证「拿到策略后渲染成什么」与「拿不到策略时不拦人」。
describe('RegisterView first paint states', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('shows the closed notice instead of the form when the policy is closed', () => {
    const { html } = render(RegisterView, { props: { initialPolicy: 'closed' } });

    expect(html).toContain('data-testid="register-blocked"');
    expect(html).toContain('自助注册已关闭，请联系管理员开通账号。');
    expect(html).not.toContain('data-testid="register-submit"');
    expect(html).not.toContain('data-testid="register-password-confirm"');
  });

  it('asks for an invite link when the policy is invite and this visit carries no token', () => {
    const { html } = render(RegisterView, { props: { initialPolicy: 'invite' } });

    expect(html).toContain('data-testid="register-blocked"');
    expect(html).toContain('注册需要邀请链接，请联系管理员获取。');
    expect(html).not.toContain('data-testid="register-submit"');
  });

  it('renders the form with the password confirmation field when registration is open', () => {
    const { html } = render(RegisterView, { props: { initialPolicy: 'open' } });

    expect(html).toContain('data-testid="register-password-confirm"');
    expect(html).toContain('data-testid="register-submit"');
    expect(html).not.toContain('data-testid="register-blocked"');
  });

  it('treats a failed probe as unknown and keeps the form available', () => {
    const { html } = render(RegisterView, { props: { initialPolicy: 'unknown' } });

    expect(html).toContain('data-testid="register-submit"');
    expect(html).toContain('data-testid="register-password-confirm"');
    expect(html).not.toContain('data-testid="register-blocked"');
  });

  it('renders neither the form nor the notice while the policy is still being probed', () => {
    // 首帧不渲染表单：closed 的站点否则会先闪一下表单再被拦截面板替换。
    const { html } = render(RegisterView, { props: { initialPolicy: 'probing' } });

    expect(html).toContain('data-testid="register-probing"');
    expect(html).not.toContain('data-testid="register-submit"');
    expect(html).not.toContain('data-testid="register-blocked"');
  });
});

describe('SetupView form', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the password confirmation field next to the password field', () => {
    const { html } = render(SetupView, {});

    expect(html).toContain('data-testid="setup-password"');
    expect(html).toContain('data-testid="setup-password-confirm"');
    expect(html).toContain('确认密码');
  });
});

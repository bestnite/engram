import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import RegisterView from '../lib/views/RegisterView.svelte';
import SetupView from '../lib/views/SetupView.svelte';
import { setLocale } from '../lib/i18n';

// The register view's two first-paint shapes and the confirm-password field.
// The policy probe itself (GET /api/v1/auth/registration) is covered separately by register.test.ts's client contract and
// internal/web/auth_register_test.go's TestRegistrationInfoEndpoint;
// this file only verifies "what renders once the policy is known" and "no blocking when it cannot be fetched".
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
    // The form is not rendered on the first frame: otherwise a closed site would flash the form before the block panel replaces it.
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

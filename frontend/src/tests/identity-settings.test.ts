import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import IdentitySection from '../lib/components/settings/IdentitySection.svelte';
import { authStore } from '../lib/auth';
import { setLocale } from '../lib/i18n';
import type { IdentitiesResponse, User } from '../lib/api';

const twoIdentities: IdentitiesResponse = {
  oidc_enabled: true,
  identities: [
    { id: '1', provider: 'Example SSO', email: 'alice@example.com', linked_at: '2026-01-05T09:00:00Z' },
    { id: '2', provider: 'Other IdP', email: '', linked_at: '2025-12-01T00:00:00Z' },
  ],
};

const oneIdentity: IdentitiesResponse = {
  oidc_enabled: true,
  identities: [twoIdentities.identities[0]!],
};

function signIn(hasPassword: boolean): void {
  const user: User = {
    id: 'u1',
    username: 'alice',
    email: 'alice@example.com',
    role: 'user',
    locale: 'zh-CN',
    has_password: hasPassword,
  };
  authStore.set({ initialized: true, loading: false, authenticated: true, user, error: null });
}

/**
 * The section renders from the `initial` prop here: SSR never runs onMount, so this suite covers the states the
 * page can be in, while identity-settings.client.ts covers the fetch, bind and unlink paths.
 */
describe('IdentitySection renders the linked-identity states', () => {
  beforeEach(() => {
    setLocale('zh-CN');
    signIn(true);
  });

  it('lists every identity with provider, email or a dash, and the linked date', () => {
    const { html } = render(IdentitySection, { props: { initial: twoIdentities } });
    expect(html).toContain('data-testid="settings-identities"');
    expect(html).toContain('已绑定的登录身份');
    expect(html).toContain('data-testid="identity-row-1"');
    expect(html).toContain('data-testid="identity-row-2"');
    expect(html).toContain('Example SSO');
    expect(html).toContain('alice@example.com');
    expect(html).toContain('—');
    expect(html).toContain('2026');
    expect(html).toContain('data-testid="identity-bind"');
  });

  it('keeps the list and unlinking but hides the bind button when OIDC is disabled', () => {
    const { html } = render(IdentitySection, {
      props: { initial: { ...twoIdentities, oidc_enabled: false } },
    });
    expect(html).toContain('data-testid="identity-row-1"');
    expect(html).toContain('data-testid="identity-unlink-1"');
    expect(html).not.toContain('data-testid="identity-bind"');
    expect(html).toContain('data-testid="identity-oidc-disabled"');
    expect(html).toContain('当前实例未启用 OIDC 登录');
  });

  it('hides the whole section when OIDC is disabled and nothing is linked', () => {
    const { html } = render(IdentitySection, {
      props: { initial: { oidc_enabled: false, identities: [] } },
    });
    expect(html).not.toContain('data-testid="settings-identities"');
  });

  it('disables the unlink control and explains why for the only identity of a passwordless account', () => {
    signIn(false);
    const { html } = render(IdentitySection, { props: { initial: oneIdentity } });
    expect(html).toMatch(/disabled=""[^>]*data-testid="identity-unlink-1"/);
    expect(html).toContain('data-testid="identity-last-credential"');
    expect(html).toContain('请先设置密码再解绑');
  });

  it('leaves the unlink control enabled for a passwordless account with another identity', () => {
    signIn(false);
    const { html } = render(IdentitySection, { props: { initial: twoIdentities } });
    expect(html).not.toMatch(/disabled=""[^>]*data-testid="identity-unlink-1"/);
    expect(html).not.toContain('data-testid="identity-last-credential"');
  });
});

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import { ApiClient } from '../lib/api';
import OIDCLoginEntry from '../lib/components/OIDCLoginEntry.svelte';
import { setLocale } from '../lib/i18n';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

// SPA 的 OIDC 登录入口契约：探测端点只含 enabled 与稳定地址；组件仅在可用时渲染入口。
// 服务端判定与凭据不外泄由 internal/web/spa_oidc_test.go 覆盖。
describe('SPA OIDC login entry', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    setLocale('en');
  });

  it('probes the OIDC entry over GET /api/v1/auth/oidc', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ enabled: true, start_url: '/auth/oidc/start' }), { status: 200 })
    );

    const res = await client.getOIDC();

    expect(res).toEqual({ enabled: true, start_url: '/auth/oidc/start' });
    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/auth/oidc');
    expect(init?.credentials).toBe('same-origin');
  });

  it('renders the entry link when the probe says the provider is enabled', () => {
    const { html } = render(OIDCLoginEntry, {
      props: { initial: { enabled: true, start_url: '/auth/oidc/start' } },
    });
    expect(html).toContain('data-testid="oidc-entry"');
    expect(html).toContain('href="/auth/oidc/start"');
    expect(html).toContain(en['account.oidc.button']);
  });

  it('renders nothing when the provider is disabled', () => {
    const { html } = render(OIDCLoginEntry, {
      props: { initial: { enabled: false, start_url: '/auth/oidc/start' } },
    });
    expect(html).not.toContain('data-testid="oidc-entry"');
    expect(html).not.toContain('/auth/oidc/start');
  });

  it('has the OIDC label in both catalogues', () => {
    expect(zhCN['account.oidc.button']).toBeTruthy();
    expect(en['account.oidc.button']).toBe(
      // The SSR login page uses the same wording; keep the two in step.
      'Sign in with OIDC'
    );
  });
});

import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import AdminOIDCView from '../lib/views/admin/AdminOIDCView.svelte';
import { apiClient, ApiClientError } from '../lib/api';
import type { AdminOIDCResponse } from '../lib/api';
import { setLocale, formatMessage } from '../lib/i18n';
import { confirmRequest } from '../lib/components/ui/confirm';
import { toast } from '../lib/components/ui/toast';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

const oidc: AdminOIDCResponse = {
  enabled: true,
  issuer: 'https://idp.example.com',
  client_id: 'engram',
  secret_configured: true,
  redirect_uri: 'https://app.example.com/auth/oidc/callback',
  scopes: 'openid email',
  claim_subject: 'sub',
  claim_email: 'email',
  claim_name: 'name',
  claim_email_verified: 'email_verified',
  identities: [
    {
      id: '4',
      provider: 'https://idp.example.com',
      subject: 'abc',
      email: 'alice@example.com',
      username: 'alice',
      linked_at: '2026-03-10T12:00:00Z',
    },
  ],
};

/**
 * The admin page renders under SSR in admin-panel-config.test.ts, which never runs the click handlers.
 * A refused unlink only shows its explanation on a mounted client, so the mapping from the server's
 * last_credential code to the copy that tells the admin to act elsewhere is covered here.
 */
describe('AdminOIDCView unlink refusals', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function setup(): void {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(AdminOIDCView, { target, props: { initialLoading: false, initialData: oidc } });
    flushSync();
  }

  async function confirmUnlink(): Promise<void> {
    (target.querySelector('[data-testid="admin-oidc-unlink-4"]') as HTMLButtonElement).click();
    await flush();
    get(confirmRequest)?.resolve(true);
    await flush();
  }

  beforeEach(() => {
    setLocale('zh-CN');
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    confirmRequest.set(null);
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it('explains that the identity is the only way to sign in when the unlink is refused', async () => {
    vi.spyOn(apiClient, 'unlinkAdminOIDCIdentity').mockRejectedValue(
      new ApiClientError('HTTP 409', { status: 409, code: 'last_credential' })
    );

    setup();
    await confirmUnlink();

    expect(apiClient.unlinkAdminOIDCIdentity).toHaveBeenCalledWith('4');
    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'admin.oidc.notice.last_credential'));
    expect(toast.error).not.toHaveBeenCalledWith(formatMessage('zh-CN', 'admin.oidc.notice.failed'));
  });

  it('still falls back to the generic message for an unmapped code', async () => {
    vi.spyOn(apiClient, 'unlinkAdminOIDCIdentity').mockRejectedValue(
      new ApiClientError('HTTP 500', { status: 500, code: 'internal_error' })
    );

    setup();
    await confirmUnlink();

    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'admin.oidc.notice.failed'));
  });
});

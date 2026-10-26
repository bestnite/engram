import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import type { Mock } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import IdentitySection from '../lib/components/settings/IdentitySection.svelte';
import { apiClient } from '../lib/api';
import type { IdentitiesResponse, User } from '../lib/api';
import { authStore } from '../lib/auth';
import { setLocale, formatMessage } from '../lib/i18n';
import { confirmRequest } from '../lib/components/ui/confirm';
import { toast } from '../lib/components/ui/toast';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

const linked: IdentitiesResponse = {
  oidc_enabled: true,
  identities: [{ id: '1', provider: 'Example SSO', email: 'alice@example.com', linked_at: '2026-01-05T09:00:00Z' }],
};

const afterUnlink: IdentitiesResponse = { oidc_enabled: true, identities: [] };

/**
 * The bind and unlink paths only happen on a mounted client: the first leaves the SPA for the provider, the second
 * waits on the site confirm. Navigation is injected as a prop, because happy-dom's location.assign is not a
 * spyable function; the component's only navigation is this single callback.
 */
describe('IdentitySection mount behaviour', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;
  let navigate: Mock<(url: string) => void>;

  function signIn(hasPassword = true): void {
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

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function setup(): void {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(IdentitySection, { target, props: { navigate } });
    flushSync();
  }

  beforeEach(() => {
    setLocale('zh-CN');
    signIn();
    navigate = vi.fn<(url: string) => void>();
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    confirmRequest.set(null);
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it('loads the list from the API on mount', async () => {
    vi.spyOn(apiClient, 'getIdentities').mockResolvedValue(linked);

    setup();
    await flush();

    expect(apiClient.getIdentities).toHaveBeenCalledTimes(1);
    expect(target.querySelector('[data-testid="identity-row-1"]')?.textContent).toContain('Example SSO');
    expect(target.querySelector('[data-testid="identity-bind"]')).not.toBeNull();
  });

  it('starts a bind and sends the browser to the returned authorize URL', async () => {
    vi.spyOn(apiClient, 'getIdentities').mockResolvedValue(linked);
    vi.spyOn(apiClient, 'beginIdentityBind').mockResolvedValue({ authorize_url: 'https://idp.example.com/authorize' });

    setup();
    await flush();

    (target.querySelector('[data-testid="identity-bind"]') as HTMLButtonElement).click();
    await flush();

    expect(apiClient.beginIdentityBind).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith('https://idp.example.com/authorize');
  });

  it('unlinks after confirmation, then refetches the list', async () => {
    const getIdentities = vi
      .spyOn(apiClient, 'getIdentities')
      .mockResolvedValueOnce(linked)
      .mockResolvedValueOnce(afterUnlink);
    vi.spyOn(apiClient, 'unlinkIdentity').mockResolvedValue(undefined);

    setup();
    await flush();

    (target.querySelector('[data-testid="identity-unlink-1"]') as HTMLButtonElement).click();
    await flush();

    const pending = get(confirmRequest);
    expect(pending?.title).toBe(formatMessage('zh-CN', 'settings.identity.confirm.unlink_title'));
    expect(pending?.destructive).toBe(true);
    pending?.resolve(true);
    await flush();

    expect(apiClient.unlinkIdentity).toHaveBeenCalledWith('1');
    expect(getIdentities).toHaveBeenCalledTimes(2);
    expect(toast.success).toHaveBeenCalledWith(formatMessage('zh-CN', 'settings.identity.toast.unlinked'));
    expect(target.querySelector('[data-testid="identity-row-1"]')).toBeNull();
  });

  it('does not unlink when the confirmation is cancelled', async () => {
    vi.spyOn(apiClient, 'getIdentities').mockResolvedValue(linked);
    vi.spyOn(apiClient, 'unlinkIdentity').mockResolvedValue(undefined);

    setup();
    await flush();

    (target.querySelector('[data-testid="identity-unlink-1"]') as HTMLButtonElement).click();
    await flush();

    get(confirmRequest)?.resolve(false);
    await flush();

    expect(apiClient.unlinkIdentity).not.toHaveBeenCalled();
  });

  it('maps the server last-credential refusal to the same explanation', async () => {
    vi.spyOn(apiClient, 'getIdentities').mockResolvedValue(linked);
    const { ApiClientError } = await import('../lib/api');
    vi.spyOn(apiClient, 'unlinkIdentity').mockRejectedValue(
      new ApiClientError('HTTP 409', { status: 409, code: 'last_credential' })
    );

    setup();
    await flush();

    (target.querySelector('[data-testid="identity-unlink-1"]') as HTMLButtonElement).click();
    await flush();
    get(confirmRequest)?.resolve(true);
    await flush();

    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'settings.identity.last_credential'));
  });

  it('surfaces a load failure without breaking the section', async () => {
    vi.spyOn(apiClient, 'getIdentities').mockRejectedValue(new Error('offline'));

    setup();
    await flush();

    expect(target.querySelector('[data-testid="identities-failed"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="identities-retry"]')).not.toBeNull();
  });
});

import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import RegisterView from '../lib/views/RegisterView.svelte';
import { apiClient } from '../lib/api';
import { setLocale } from '../lib/i18n';

/** Let pending promise callbacks and the render effect scheduled after them run. */
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

/**
 * The registration page's OIDC entry is offered only where self-registration is allowed, and it probes the provider on mount,
 * so this suite is the only place that covers "the entry appears in the form branch" and "a blocked branch never mounts it".
 * Rendering RegisterView does not call initAuth, so the redirect effect never fires here: the auth store stays unauthenticated.
 */
describe('RegisterView OIDC entry after mount', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  beforeEach(() => {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getOIDC').mockResolvedValue({
      enabled: true,
      start_url: '/auth/oidc/start',
    });
  });

  function setup(policy: 'open' | 'closed' | 'invite'): HTMLElement {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(RegisterView, { target, props: { initialPolicy: policy } });
    flushSync();
    return target;
  }

  function entry(): Element | null {
    return target.querySelector('[data-testid="oidc-entry"]');
  }

  /** data-testid="oidc-entry" sits on the wrapper; the link itself is the anchor inside it. */
  function entryLink(): Element | null {
    return entry()?.querySelector('a') ?? null;
  }

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('offers the OIDC entry in the form branch when the policy is open', async () => {
    vi.spyOn(apiClient, 'registrationInfo').mockResolvedValue({ policy: 'open' });

    setup('open');
    await settle();
    flushSync();

    expect(entry()).not.toBeNull();
    expect(entryLink()?.getAttribute('href')).toBe('/auth/oidc/start');
    expect(entryLink()?.textContent?.trim()).toBe('使用 OIDC 注册');
  });

  it('never mounts the entry, and never probes, when the policy is closed', async () => {
    vi.spyOn(apiClient, 'registrationInfo').mockResolvedValue({ policy: 'closed' });

    setup('closed');
    await settle();
    flushSync();

    expect(entry()).toBeNull();
    expect(apiClient.getOIDC).not.toHaveBeenCalled();
  });

  it('never mounts the entry, and never probes, when the policy is invite and this visit carries no token', async () => {
    vi.spyOn(apiClient, 'registrationInfo').mockResolvedValue({ policy: 'invite' });

    setup('invite');
    await settle();
    flushSync();

    expect(entry()).toBeNull();
    expect(apiClient.getOIDC).not.toHaveBeenCalled();
  });
});

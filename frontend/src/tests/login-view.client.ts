import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import LoginView from '../lib/views/LoginView.svelte';
import { apiClient } from '../lib/api';
import { setLocale } from '../lib/i18n';

/** Let pending promise callbacks and the render effect scheduled after them run. */
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

/**
 * The login page's register link is decided by a probe that only runs on mount, so this suite is the only place
 * that covers the transition from the first frame (no link, while probing) to the settled state.
 * Rendering LoginView does not call initAuth, so the redirect effect never fires here: the auth store stays unauthenticated.
 */
describe('LoginView register link after the policy probe', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  beforeEach(() => {
    setLocale('zh-CN');
    // The OIDC entry has its own probe; stub it so this suite never issues a relative request from happy-dom.
    vi.spyOn(apiClient, 'getOIDC').mockResolvedValue({ enabled: false, start_url: '' });
  });

  function setup(): HTMLElement {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(LoginView, { target, props: {} });
    flushSync();
    return target;
  }

  function link(): Element | null {
    return target.querySelector('[data-testid="login-to-register"]');
  }

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('renders no link on the first frame and shows it once the probe reports open', async () => {
    vi.spyOn(apiClient, 'registrationInfo').mockResolvedValue({ policy: 'open' });

    setup();
    // The probe has not resolved yet, so the first frame must not contain the link.
    expect(link()).toBeNull();

    await settle();
    flushSync();

    expect(link()).not.toBeNull();
    expect(link()?.getAttribute('href')).toBe('/register');
    expect(link()?.textContent?.trim()).toBe('还没有账号？立即注册');
  });

  it('keeps the link absent when the probe reports closed', async () => {
    vi.spyOn(apiClient, 'registrationInfo').mockResolvedValue({ policy: 'closed' });

    setup();
    await settle();
    flushSync();

    expect(link()).toBeNull();
  });

  it('shows the link when the probe fails, so the visitor is not left without a way in', async () => {
    vi.spyOn(apiClient, 'registrationInfo').mockRejectedValue(new Error('offline'));

    setup();
    await settle();
    flushSync();

    expect(link()).not.toBeNull();
    expect(link()?.getAttribute('href')).toBe('/register');
  });
});
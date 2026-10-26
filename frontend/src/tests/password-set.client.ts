import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import PasswordSection from '../lib/components/settings/PasswordSection.svelte';
import { apiClient, ApiClientError } from '../lib/api';
import type { User } from '../lib/api';
import { authStore } from '../lib/auth';
import { setLocale, formatMessage } from '../lib/i18n';
import { toast } from '../lib/components/ui/toast';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

function signIn(hasPassword: boolean | undefined): void {
  const user: User = {
    id: 'u1',
    username: 'alice',
    email: 'alice@example.com',
    role: 'user',
    locale: 'zh-CN',
    has_password: true,
  };
  if (hasPassword === undefined) {
    // Simulate a session payload from before the field existed.
    delete (user as Partial<User>).has_password;
  } else {
    user.has_password = hasPassword;
  }
  authStore.set({ initialized: true, loading: false, authenticated: true, user, error: null });
}

/**
 * The passwordless account has no current password, so the form must ask for the new one only and must update the
 * store after the write: a stale has_password would keep showing "set a password" on a page that already has one.
 */
describe('PasswordSection first-password variant', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  beforeEach(() => {
    setLocale('zh-CN');
    signIn(false);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(PasswordSection, { target, props: {} });
    flushSync();
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it('asks only for the new password and submits it without an old one', async () => {
    expect(target.querySelector('input[autocomplete="current-password"]')).toBeNull();
    const input = target.querySelector('input[autocomplete="new-password"]') as HTMLInputElement;
    expect(input).not.toBeNull();
    expect(target.textContent).toContain(formatMessage('zh-CN', 'settings.password.set_heading'));

    const changePassword = vi.spyOn(apiClient, 'changePassword').mockResolvedValue(undefined);

    input.value = 'Sup3rSecret!';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();

    (target.querySelector('form') as HTMLFormElement).dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true })
    );
    await flush();

    expect(changePassword).toHaveBeenCalledWith({ new_password: 'Sup3rSecret!' });
    expect(get(authStore).user?.has_password).toBe(true);
    expect(toast.success).toHaveBeenCalledWith(formatMessage('zh-CN', 'settings.password.set_done'));
  });

  it('switches to the change form when another request already set the password', async () => {
    vi.spyOn(apiClient, 'changePassword').mockRejectedValue(
      new ApiClientError('HTTP 409', { status: 409, code: 'password_already_set' })
    );

    const input = target.querySelector('input[autocomplete="new-password"]') as HTMLInputElement;
    input.value = 'Sup3rSecret!';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();

    (target.querySelector('form') as HTMLFormElement).dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true })
    );
    await flush();

    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'settings.password.already_set'));
    // The stale store would keep offering the first-password form, so it is corrected to match the server.
    expect(get(authStore).user?.has_password).toBe(true);
    expect(target.querySelector('input[autocomplete="current-password"]')).not.toBeNull();
  });

  it('keeps the change form when the payload has no has_password flag', () => {
    signIn(undefined);
    unmount(instance!);
    instance = null;
    document.body.innerHTML = '';
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(PasswordSection, { target, props: {} });
    flushSync();

    expect(target.querySelector('input[autocomplete="current-password"]')).not.toBeNull();
    expect(target.querySelectorAll('input[autocomplete="new-password"]')).toHaveLength(1);
  });
});

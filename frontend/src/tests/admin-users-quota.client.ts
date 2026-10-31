import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import AdminUsersView from '../lib/views/admin/AdminUsersView.svelte';
import { apiClient, ApiClientError, type AdminUsersResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

const data: AdminUsersResponse = {
  users: [
    {
      id: 'u2', username: 'alice', email: 'alice@example.com', display_name: 'Alice', role: 'user', status: 'active',
      decks: 0, cards: 0, reviews: 0, is_self: false, media_quota_bytes: 10485760, media_used_bytes: 1048576,
    },
  ],
  page: 1,
  pages: 1,
  total: 1,
  query: '',
  default_media_quota_bytes: 0,
};

let instance: ReturnType<typeof mount> | null = null;

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

async function flush(): Promise<void> {
  for (let i = 0; i < 20; i++) await Promise.resolve();
  flushSync();
}

async function openQuotaDialog(): Promise<void> {
  setLocale('zh-CN');
  const target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(AdminUsersView, { target, props: { initialLoading: false, initialData: structuredClone(data) } });
  await flush();
  const trigger = document.body.querySelector('[data-testid="admin-users-menu-u2"]') as HTMLElement;
  trigger.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, pointerType: 'mouse' }));
  await flush();
  const item = document.body.querySelector('[data-testid="admin-users-quota-u2"]') as HTMLElement;
  if (!item) throw new Error('quota menu item did not open');
  item.click();
  await flush();
}

function dialog(): HTMLElement {
  const element = document.body.querySelector('[data-testid="admin-users-quota-dialog"]') as HTMLElement | null;
  if (!element) throw new Error('quota dialog is not open');
  return element;
}

function setMiB(value: string): void {
  const input = document.body.querySelector('[data-testid="admin-users-quota-mib"]') as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function submit(): void {
  (document.body.querySelector('[data-testid="admin-users-quota-form"]') as HTMLFormElement).dispatchEvent(
    new Event('submit', { bubbles: true, cancelable: true }),
  );
}

describe('AdminUsersView media quota dialog', () => {
  it('opens with the stored limit in MiB and the usage, and saves a new limit in bytes', async () => {
    const save = vi.spyOn(apiClient, 'setAdminUserMediaQuota').mockResolvedValue();
    const reload = vi.spyOn(apiClient, 'getAdminUsers').mockResolvedValue(structuredClone(data));
    await openQuotaDialog();
    expect(dialog().textContent).toContain('alice');
    expect(dialog().textContent).toContain('1 MiB');
    expect((document.body.querySelector('[data-testid="admin-users-quota-mib"]') as HTMLInputElement).value).toBe('10');

    setMiB('2.5');
    await flush();
    submit();
    await flush();
    expect(save).toHaveBeenCalledWith('u2', { mode: 'limit', bytes: 2621440 });
    expect(reload).toHaveBeenCalled();
  });

  it('refuses a limit that is not above zero without calling the server', async () => {
    const save = vi.spyOn(apiClient, 'setAdminUserMediaQuota').mockResolvedValue();
    await openQuotaDialog();
    for (const value of ['0', '-3', '']) {
      setMiB(value);
      await flush();
      submit();
      await flush();
      expect(document.body.querySelector('[data-testid="admin-users-quota-error"]')?.textContent).toContain('大于 0');
    }
    expect(save).not.toHaveBeenCalled();
  });

  it('shows the server refusal inside the dialog', async () => {
    vi.spyOn(apiClient, 'setAdminUserMediaQuota').mockRejectedValue(
      new ApiClientError('HTTP 400: invalid_quota', { status: 400, code: 'invalid_quota' }),
    );
    await openQuotaDialog();
    setMiB('5');
    await flush();
    submit();
    await flush();
    expect(document.body.querySelector('[data-testid="admin-users-quota-error"]')?.textContent).toContain('大于 0');
    expect(document.body.querySelector('[data-testid="admin-users-quota-dialog"]')).not.toBeNull();
  });
});

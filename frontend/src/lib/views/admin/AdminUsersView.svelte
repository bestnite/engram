<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminUsersResponse, AdminUser } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Select from '../../components/ui/Select.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminUsersResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminUsersResponse | null>(initialData);
  // svelte-ignore state_referenced_locally
  let query = $state(initialData?.query ?? '');
  let notice = $state('');
  let actionError = $state('');
  let tempPassword = $state('');
  let creating = $state(false);

  let form = $state({ username: '', email: '', display_name: '', password: '', role: 'user' });

  /** 稳定错误 code → 本地化 key；未知 code 回落到通用提示。 */
  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      self_forbidden: 'admin.users.notice.self_forbidden',
      last_admin: 'admin.users.notice.last_admin',
      confirm_required: 'admin.users.notice.confirm_required',
      invalid_role: 'admin.users.notice.invalid_role',
      invalid_user: 'admin.users.notice.invalid_user',
      save_failed: 'admin.users.notice.save_failed',
      create_failed: 'admin.users.notice.create_failed',
    };
    return map[code] || 'admin.users.notice.save_failed';
  }

  function roleLabel(role: string): string {
    return $t(role === 'admin' ? 'admin.users.role.admin' : 'admin.users.role.user');
  }

  function statusLabel(status: string): string {
    return $t(status === 'disabled' ? 'admin.users.status.disabled' : 'admin.users.status.active');
  }

  async function load(page = 1): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminUsers({ q: query, page });
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.users.load_failed';
  }

  /** 执行一个动作；成功后重载列表，失败时把稳定 code 映射成本地化提示。 */
  async function run(fn: () => Promise<void>): Promise<void> {
    notice = '';
    actionError = '';
    try {
      await fn();
    } catch (err) {
      actionError = actionErrorKey(err);
      return;
    }
    await load(data?.page ?? 1);
  }

  /** 危险动作统一走一次确认；服务端在收不到 confirm 时也会拒绝。 */
  async function confirmThen(messageKey: string, fn: () => Promise<void>): Promise<void> {
    if (typeof window !== 'undefined' && !window.confirm($t(messageKey))) return;
    await run(fn);
  }

  async function create(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    notice = '';
    actionError = '';
    creating = true;
    try {
      await apiClient.createAdminUser(form);
      form = { username: '', email: '', display_name: '', password: '', role: 'user' };
      notice = 'admin.users.notice.created';
      await load(1);
    } catch (err) {
      // 创建校验错误是 registerInputErrorCode 的稳定 code，复用注册语言包的提示。
      const code = err instanceof ApiClientError ? err.code : '';
      actionError = ['username_required', 'email_required', 'email_invalid', 'password_required', 'password_too_short', 'password_too_long', 'password_too_common'].includes(code)
        ? 'auth.error.' + code
        : 'admin.users.notice.create_failed';
    } finally {
      creating = false;
    }
  }

  async function toggleStatus(u: AdminUser): Promise<void> {
    if (u.status === 'disabled') {
      await run(async () => {
        await apiClient.setAdminUserStatus(u.id, 'enable');
        notice = 'admin.users.notice.updated';
      });
      return;
    }
    await confirmThen('admin.users.confirm_disable', async () => {
      await apiClient.setAdminUserStatus(u.id, 'disable');
      notice = 'admin.users.notice.updated';
    });
  }

  async function changeRole(u: AdminUser, role: string): Promise<void> {
    await confirmThen('admin.users.confirm_role', async () => {
      await apiClient.setAdminUserRole(u.id, role);
      notice = 'admin.users.notice.updated';
    });
  }

  async function resetPassword(u: AdminUser): Promise<void> {
    notice = '';
    actionError = '';
    tempPassword = '';
    if (typeof window !== 'undefined' && !window.confirm($t('admin.users.confirm_reset'))) return;
    try {
      const res = await apiClient.resetAdminUserPassword(u.id);
      tempPassword = res.temp_password;
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  async function forceLogout(u: AdminUser): Promise<void> {
    await confirmThen('admin.users.confirm_logout', async () => {
      await apiClient.forceLogoutAdminUser(u.id);
      notice = 'admin.users.notice.updated';
    });
  }

  async function remove(u: AdminUser): Promise<void> {
    await confirmThen('admin.users.confirm_delete', async () => {
      await apiClient.deleteAdminUser(u.id);
      notice = 'admin.users.notice.deleted';
    });
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load(1);
    }
  });
</script>

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-users">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="admin-users-title">
      {$t('admin.users.heading')}
    </h1>
  </header>

  <form onsubmit={(e) => { e.preventDefault(); load(1); }} data-testid="admin-users-search" class="flex items-end gap-3">
    <label class="block flex-1">
      <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.search_label')}</span>
      <input
        data-testid="admin-users-search-input"
        bind:value={query}
        placeholder={$t('admin.users.search_placeholder')}
        class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100"
      />
    </label>
    <button type="submit" data-testid="admin-users-search-submit" class="btn-press cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-semibold text-white dark:bg-zinc-100 dark:text-zinc-900">
      {$t('admin.users.search_submit')}
    </button>
  </form>

  {#if notice}<div data-testid="admin-users-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-users-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}
  {#if tempPassword}
    <div data-testid="admin-users-temp-password" class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
      {$t('admin.users.temp_password_label')}: <span class="font-mono font-semibold">{tempPassword}</span>
    </div>
  {/if}

  <form onsubmit={create} data-testid="admin-users-create" class="card-elevated grid gap-3 rounded-xl p-5 sm:grid-cols-3">
    <h2 class="sm:col-span-3 text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.create_heading')}</h2>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.field.username')}</span>
      <input data-testid="admin-users-create-username" bind:value={form.username} class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.field.email')}</span>
      <input data-testid="admin-users-create-email" bind:value={form.email} class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.field.display_name')}</span>
      <input data-testid="admin-users-create-display" bind:value={form.display_name} class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.field.password')}</span>
      <input data-testid="admin-users-create-password" type="password" bind:value={form.password} class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.users.field.role')}</span>
      <Select
        class="mt-1.5"
        bind:value={form.role}
        testId="admin-users-create-role"
        options={[{ value: 'user', label: $t('admin.users.role.user') }, { value: 'admin', label: $t('admin.users.role.admin') }]}
      />
    </label>
    <div class="sm:col-span-3">
      <button type="submit" data-testid="admin-users-create-submit" disabled={creating} class="btn-press cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900">
        {$t('admin.users.create_submit')}
      </button>
    </div>
  </form>

  {#if loading}
    <div data-testid="admin-users-loading" class="py-12 text-center text-sm text-zinc-500 dark:text-zinc-400">{$t('common.loading')}</div>
  {:else if loadError}
    <div data-testid="admin-users-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button type="button" data-testid="admin-users-retry" onclick={() => load(1)} class="btn-press mt-4 cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-medium text-white dark:bg-zinc-100 dark:text-zinc-900">{$t('common.retry')}</button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.users.length === 0}
      <p data-testid="admin-users-empty" class="py-8 text-center text-sm text-zinc-500 dark:text-zinc-400">{$t('admin.users.empty')}</p>
    {:else}
      <div class="card-elevated overflow-x-auto rounded-xl">
        <table class="w-full text-left text-sm">
          <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
            <tr>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.user')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.email')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.role')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.status')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.decks')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.cards')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.reviews')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.users.col.actions')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
            {#each view.users as u (u.id)}
              <tr data-testid="admin-users-row-{u.id}">
                <td class="px-4 py-2.5 font-medium text-zinc-900 dark:text-zinc-100">{u.username}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{u.email}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{roleLabel(u.role)}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{statusLabel(u.status)}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{u.decks}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{u.cards}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{u.reviews}</td>
                <td class="px-4 py-2.5">
                  <div class="flex flex-wrap gap-1.5">
                    <button type="button" data-testid="admin-users-status-{u.id}" onclick={() => toggleStatus(u)} class="cursor-pointer rounded border border-zinc-200 px-2 py-1 text-xs dark:border-zinc-700">
                      {u.status === 'disabled' ? $t('admin.users.enable') : $t('admin.users.disable')}
                    </button>
                    <Select
                      class="w-24"
                      size="sm"
                      value={u.role}
                      onValueChange={(role) => changeRole(u, role)}
                      testId="admin-users-role-{u.id}"
                      options={[{ value: 'user', label: $t('admin.users.role.user') }, { value: 'admin', label: $t('admin.users.role.admin') }]}
                    />
                    <button type="button" data-testid="admin-users-password-{u.id}" onclick={() => resetPassword(u)} class="cursor-pointer rounded border border-zinc-200 px-2 py-1 text-xs dark:border-zinc-700">{$t('admin.users.reset_password')}</button>
                    <button type="button" data-testid="admin-users-logout-{u.id}" onclick={() => forceLogout(u)} class="cursor-pointer rounded border border-zinc-200 px-2 py-1 text-xs dark:border-zinc-700">{$t('admin.users.force_logout')}</button>
                    <button type="button" data-testid="admin-users-delete-{u.id}" onclick={() => remove(u)} class="cursor-pointer rounded border border-rose-200 px-2 py-1 text-xs text-rose-600 dark:border-rose-900">{$t('admin.users.delete')}</button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="flex items-center justify-between" data-testid="admin-users-pager">
        <button type="button" data-testid="admin-users-prev" disabled={view.page <= 1} onclick={() => load(view.page - 1)} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm disabled:opacity-40 dark:border-zinc-700">{$t('admin.common.prev')}</button>
        <span class="text-sm text-zinc-500 dark:text-zinc-400">{view.page} / {view.pages}</span>
        <button type="button" data-testid="admin-users-next" disabled={view.page >= view.pages} onclick={() => load(view.page + 1)} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm disabled:opacity-40 dark:border-zinc-700">{$t('admin.common.next')}</button>
      </div>
    {/if}
  {/if}
</div>

<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminUsersResponse, AdminUser } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Select from '../../components/ui/Select.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import Dialog from '../../components/ui/Dialog.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import { listClasses, menuClasses } from '../../components/ui/variants';
  import { toast } from '../../components/ui/toast';
  import { DropdownMenu } from 'bits-ui';
  import { Ban, CheckCircle, Copy, KeyRound, LogOut, MoreHorizontal, Plus, Search, Shield, Trash2, User, X } from '@lucide/svelte';

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
  let createOpen = $state(false);

  // 操作结果用 toast 报告，不在页面顶部插横幅。新建用户的错误显示在对话框里，对话框关着时才走 toast。
  $effect(() => {
    if (notice) {
      toast.success($t(notice));
      notice = '';
    }
  });
  $effect(() => {
    if (actionError && !createOpen) {
      toast.error($t(actionError));
      actionError = '';
    }
  });

  async function copyTempPassword(): Promise<void> {
    try {
      await navigator.clipboard.writeText(tempPassword);
      toast.success($t('admin.users.temp_password_copied'));
    } catch {
      toast.error($t('notes.copy_failed'));
    }
  }

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
    if (!(await askConfirm({ title: $t(messageKey), destructive: true }))) return;
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
      createOpen = false;
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
    if (!(await askConfirm({ title: $t('admin.users.confirm_reset') }))) return;
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

<Page testId="admin-users">
  <AdminNav />

  <PageHeader title={$t('admin.users.heading')} testId="admin-users-title">
    {#snippet actions()}
      <Button testId="admin-users-create-open" variant="primary" size="lg" onclick={() => { actionError = ''; createOpen = true; }}>
        <Plus class="size-4" aria-hidden="true" />
        {$t('admin.users.create_heading')}
      </Button>
    {/snippet}
  </PageHeader>

  <form onsubmit={(e) => { e.preventDefault(); load(1); }} data-testid="admin-users-search" class="mb-3 flex items-center gap-2">
    <label class="relative flex w-full items-center sm:w-80">
      <Search class="pointer-events-none absolute left-2.5 size-4 text-muted-foreground" aria-hidden="true" />
      <input
        type="search"
        data-testid="admin-users-search-input"
        bind:value={query}
        aria-label={$t('admin.users.search_label')}
        placeholder={$t('admin.users.search_placeholder')}
        class="field-input w-full pl-8 text-sm"
      />
    </label>
    <Button type="submit" testId="admin-users-search-submit" variant="outline" size="lg">{$t('admin.users.search_submit')}</Button>
  </form>

  {#if tempPassword}
    <!-- 临时密码只显示这一次：贴在列表上方，带复制按钮。 -->
    <div data-testid="admin-users-temp-password" class="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-warning/40 bg-warning/5 px-4 py-2.5 text-sm animate-in fade-in-0 slide-in-from-top-1 duration-200" role="status">
      <span class="text-muted-foreground">{$t('admin.users.temp_password_label')}</span>
      <span class="select-all font-mono font-semibold text-foreground">{tempPassword}</span>
      <span class="flex-1"></span>
      <Button variant="outline" size="sm" onclick={copyTempPassword}><Copy class="size-3.5" aria-hidden="true" />{$t('deck.sharing.copy')}</Button>
      <Button variant="ghost" size="icon" label={$t('deck.sharing.dismiss')} onclick={() => (tempPassword = '')}><X class="size-4" aria-hidden="true" /></Button>
    </div>
  {/if}

  {#if loading}
    <Skeleton testId="admin-users-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-users-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-users-retry" onclick={() => load(1)} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.users.length === 0}
      <p data-testid="admin-users-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">{$t('admin.users.empty')}</p>
    {:else}
      <div class="{listClasses.root} overflow-x-auto">
        <table class="w-full min-w-[760px] text-left text-sm">
          <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
            <tr>
              <th class="px-4 py-2.5 font-medium">{$t('admin.users.col.user')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.users.col.role')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.users.col.status')}</th>
              <th class="px-4 py-2.5 text-right font-medium">{$t('admin.users.col.decks')}</th>
              <th class="px-4 py-2.5 text-right font-medium">{$t('admin.users.col.cards')}</th>
              <th class="px-4 py-2.5 text-right font-medium">{$t('admin.users.col.reviews')}</th>
              <th class="w-14 px-4 py-2.5"><span class="sr-only">{$t('admin.users.col.actions')}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            {#each view.users as u (u.id)}
              <tr data-testid="admin-users-row-{u.id}" class="transition-colors hover:bg-muted/40">
                <td class="px-4 py-2.5">
                  <div class="font-medium text-foreground">{u.username}</div>
                  <div class="text-[13px] text-muted-foreground">{u.email}</div>
                </td>
                <td class="px-4 py-2.5"><Badge variant={u.role === 'admin' ? 'info' : 'neutral'}>{roleLabel(u.role)}</Badge></td>
                <td class="px-4 py-2.5"><Badge variant={u.status === 'disabled' ? 'danger' : 'success'}>{statusLabel(u.status)}</Badge></td>
                <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{u.decks}</td>
                <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{u.cards}</td>
                <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{u.reviews}</td>
                <td class="px-4 py-2.5 text-right">
                  <DropdownMenu.Root>
                    <DropdownMenu.Trigger
                      data-testid="admin-users-menu-{u.id}"
                      class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground data-[state=open]:bg-muted cursor-pointer"
                      aria-label={$t('admin.users.col.actions')}
                    >
                      <MoreHorizontal class="size-4" aria-hidden="true" />
                    </DropdownMenu.Trigger>
                    <DropdownMenu.Portal>
                      <DropdownMenu.Content class={menuClasses.content} align="end" sideOffset={4}>
                        <DropdownMenu.Item class={menuClasses.item} data-testid="admin-users-status-{u.id}" onSelect={() => toggleStatus(u)}>
                          {#if u.status === 'disabled'}<CheckCircle aria-hidden="true" />{$t('admin.users.enable')}{:else}<Ban aria-hidden="true" />{$t('admin.users.disable')}{/if}
                        </DropdownMenu.Item>
                        <DropdownMenu.Item class={menuClasses.item} data-testid="admin-users-role-{u.id}" onSelect={() => changeRole(u, u.role === 'admin' ? 'user' : 'admin')}>
                          {#if u.role === 'admin'}<User aria-hidden="true" />{$t('admin.users.make_user')}{:else}<Shield aria-hidden="true" />{$t('admin.users.make_admin')}{/if}
                        </DropdownMenu.Item>
                        <DropdownMenu.Item class={menuClasses.item} data-testid="admin-users-password-{u.id}" onSelect={() => resetPassword(u)}>
                          <KeyRound aria-hidden="true" />{$t('admin.users.reset_password')}
                        </DropdownMenu.Item>
                        <DropdownMenu.Item class={menuClasses.item} data-testid="admin-users-logout-{u.id}" onSelect={() => forceLogout(u)}>
                          <LogOut aria-hidden="true" />{$t('admin.users.force_logout')}
                        </DropdownMenu.Item>
                        <DropdownMenu.Separator class={menuClasses.separator} />
                        <DropdownMenu.Item class={menuClasses.destructiveItem} data-testid="admin-users-delete-{u.id}" onSelect={() => remove(u)}>
                          <Trash2 aria-hidden="true" />{$t('admin.users.delete')}…
                        </DropdownMenu.Item>
                      </DropdownMenu.Content>
                    </DropdownMenu.Portal>
                  </DropdownMenu.Root>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="mt-3 flex items-center justify-end gap-2 text-[13px] text-muted-foreground" data-testid="admin-users-pager">
        <span class="tabular-nums">{view.page} / {view.pages}</span>
        <Button testId="admin-users-prev" variant="outline" size="sm" disabled={view.page <= 1} onclick={() => load(view.page - 1)}>{$t('admin.common.prev')}</Button>
        <Button testId="admin-users-next" variant="outline" size="sm" disabled={view.page >= view.pages} onclick={() => load(view.page + 1)}>{$t('admin.common.next')}</Button>
      </div>
    {/if}
  {/if}
</Page>

<Dialog bind:open={createOpen} title={$t('admin.users.create_heading')} size="lg" testId="admin-users-create-dialog">
  <form onsubmit={create} data-testid="admin-users-create" class="grid gap-4 sm:grid-cols-2">
    <label class="block text-sm font-medium text-foreground">{$t('admin.users.field.username')}
      <input data-testid="admin-users-create-username" bind:value={form.username} class="field-input mt-1.5 w-full text-sm font-normal" />
    </label>
    <label class="block text-sm font-medium text-foreground">{$t('admin.users.field.display_name')}
      <input data-testid="admin-users-create-display" bind:value={form.display_name} class="field-input mt-1.5 w-full text-sm font-normal" />
    </label>
    <label class="block text-sm font-medium text-foreground sm:col-span-2">{$t('admin.users.field.email')}
      <input data-testid="admin-users-create-email" type="email" bind:value={form.email} class="field-input mt-1.5 w-full text-sm font-normal" />
    </label>
    <label class="block text-sm font-medium text-foreground">{$t('admin.users.field.password')}
      <input data-testid="admin-users-create-password" type="password" bind:value={form.password} class="field-input mt-1.5 w-full text-sm font-normal" />
    </label>
    <div>
      <span class="block text-sm font-medium text-foreground">{$t('admin.users.field.role')}</span>
      <Select
        class="mt-1.5"
        bind:value={form.role}
        testId="admin-users-create-role"
        ariaLabel={$t('admin.users.field.role')}
        options={[{ value: 'user', label: $t('admin.users.role.user') }, { value: 'admin', label: $t('admin.users.role.admin') }]}
      />
    </div>
    {#if actionError}
      <p data-testid="admin-users-error" role="alert" class="text-sm text-destructive-foreground sm:col-span-2">{$t(actionError)}</p>
    {/if}
    <div class="mt-2 flex justify-end gap-2 sm:col-span-2">
      <Button variant="outline" size="lg" onclick={() => (createOpen = false)}>{$t('common.cancel')}</Button>
      <Button type="submit" testId="admin-users-create-submit" disabled={creating} variant="primary" size="lg">
        {$t('admin.users.create_submit')}
      </Button>
    </div>
  </form>
</Dialog>

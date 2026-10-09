<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminAPIKeysResponse, AdminAPIKey } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminAPIKeysResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminAPIKeysResponse | null>(initialData);
  let notice = $state('');
  let actionError = $state('');

  function stateLabel(state: string): string {
    return $t('admin.keys.state.' + state);
  }

  function ownerLabel(k: AdminAPIKey): string {
    return k.owner || '#' + k.user_id;
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.keys.load_failed';
  }

  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      invalid_key: 'admin.keys.notice.invalid_key',
      revoke_failed: 'admin.keys.notice.revoke_failed',
    };
    return map[code] || 'admin.keys.notice.revoke_failed';
  }

  async function load(page = 1): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminAPIKeys({ page });
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  async function revoke(k: AdminAPIKey): Promise<void> {
    notice = '';
    actionError = '';
    if (!(await askConfirm({ title: $t('admin.keys.confirm_revoke'), destructive: true }))) return;
    try {
      await apiClient.revokeAdminAPIKey(k.id);
      notice = 'admin.keys.notice.revoked';
      await load(data?.page ?? 1);
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load(1);
    }
  });
</script>

<Page class="space-y-6" testId="admin-api-keys">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-semibold tracking-tight text-foreground" data-testid="admin-api-keys-title">
      {$t('admin.keys.heading')}
    </h1>
    <p class="text-sm leading-relaxed text-muted-foreground">{$t('admin.keys.intro')}</p>
  </header>

  {#if notice}<div data-testid="admin-api-keys-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-api-keys-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}

  {#if loading}
    <Skeleton testId="admin-api-keys-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-api-keys-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-api-keys-retry" onclick={() => load(1)} variant="primary" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.keys.length === 0}
      <p data-testid="admin-api-keys-empty" class="py-8 text-center text-sm text-muted-foreground">{$t('admin.keys.empty')}</p>
    {:else}
      <div class="card-elevated overflow-x-auto rounded-xl">
        <table class="w-full text-left text-sm">
          <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
            <tr>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.owner')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.name')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.prefix')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.scopes')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.last_used')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.expires')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.state')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.keys.col.actions')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
            {#each view.keys as k (k.id)}
              <tr data-testid="admin-api-keys-row-{k.id}">
                <td class="px-4 py-2.5 text-foreground/80">{ownerLabel(k)}</td>
                <td class="px-4 py-2.5 text-foreground">{k.name}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-muted-foreground">{k.prefix}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{k.scopes.join(', ')}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{k.last_used_at || $t('admin.keys.last_used_never')}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{k.expires_at || $t('admin.keys.expires_never')}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{stateLabel(k.state)}</td>
                <td class="px-4 py-2.5">
                  {#if k.state === 'active'}
                    <button type="button" data-testid="admin-api-keys-revoke-{k.id}" onclick={() => revoke(k)} class="cursor-pointer rounded border border-rose-200 px-2 py-1 text-xs text-rose-600 dark:border-rose-900">{$t('admin.keys.revoke')}</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="flex items-center justify-between" data-testid="admin-api-keys-pager">
        <button type="button" data-testid="admin-api-keys-prev" disabled={view.page <= 1} onclick={() => load(view.page - 1)} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm disabled:opacity-40 dark:border-zinc-700">{$t('admin.common.prev')}</button>
        <span class="text-sm text-muted-foreground">{view.page} / {view.pages}</span>
        <button type="button" data-testid="admin-api-keys-next" disabled={view.page >= view.pages} onclick={() => load(view.page + 1)} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm disabled:opacity-40 dark:border-zinc-700">{$t('admin.common.next')}</button>
      </div>
    {/if}
  {/if}
</Page>

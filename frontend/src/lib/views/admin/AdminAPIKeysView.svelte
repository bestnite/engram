<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminAPIKeysResponse, AdminAPIKey } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import Pager from '../../components/ui/Pager.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import { toast } from '../../components/ui/toast';
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

  // 操作结果用 toast 报告，不在页面顶部插横幅。
  $effect(() => {
    if (notice) {
      toast.success($t(notice));
      notice = '';
    }
  });
  $effect(() => {
    if (actionError) {
      toast.error($t(actionError));
      actionError = '';
    }
  });
</script>

<Page testId="admin-api-keys">
  <AdminNav />
  <PageHeader title={$t('admin.keys.heading')} testId="admin-api-keys-title" description={$t('admin.keys.intro')} />

  {#if loading}
    <Skeleton testId="admin-api-keys-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-api-keys-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-api-keys-retry" onclick={() => load(1)} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.keys.length === 0}
      <p data-testid="admin-api-keys-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">{$t('admin.keys.empty')}</p>
    {:else}
      <div class="overflow-x-auto rounded-lg border border-border">
        <table class="w-full min-w-[760px] text-left text-sm">
          <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
            <tr>
              <th class="px-4 py-2.5 font-medium">{$t('admin.keys.col.name')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.keys.col.owner')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.keys.col.scopes')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.keys.col.last_used')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.keys.col.expires')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.keys.col.state')}</th>
              <th class="w-20 px-4 py-2.5"><span class="sr-only">{$t('admin.keys.col.actions')}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            {#each view.keys as k (k.id)}
              <tr data-testid="admin-api-keys-row-{k.id}">
                <td class="px-4 py-2.5">
                  <div class="text-foreground">{k.name}</div>
                  <div class="font-mono text-xs text-muted-foreground">{k.prefix}</div>
                </td>
                <td class="px-4 py-2.5 text-muted-foreground">{ownerLabel(k)}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-muted-foreground">{k.scopes.join(', ')}</td>
                <td class="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{k.last_used_at || $t('admin.keys.last_used_never')}</td>
                <td class="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{k.expires_at || $t('admin.keys.expires_never')}</td>
                <td class="px-4 py-2.5"><Badge variant={k.state === 'active' ? 'success' : 'neutral'}>{stateLabel(k.state)}</Badge></td>
                <td class="px-4 py-2.5 text-right">
                  {#if k.state === 'active'}
                    <Button variant="ghost" size="sm" class="hover:bg-destructive-soft hover:text-destructive-foreground" testId="admin-api-keys-revoke-{k.id}" onclick={() => revoke(k)}>{$t('admin.keys.revoke')}</Button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <Pager page={view.page} pages={view.pages} onPage={(n) => load(n)} testIdPrefix="admin-api-keys" />
    {/if}
  {/if}
</Page>

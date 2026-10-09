<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminHealth } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminHealth | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminHealth | null>(initialData);

  /** 与 Go 侧 humanBytes 同口径：字节数转易读单位（B/KiB/MiB/GiB/TiB）。 */
  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    const units = ['KiB', 'MiB', 'GiB', 'TiB'];
    let value = n;
    for (let i = 0; i < units.length; i++) {
      value /= 1024;
      if (value < 1024 || i === units.length - 1) {
        return `${value.toFixed(2)} ${units[i]}`;
      }
    }
    return `${n} B`;
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminHealth();
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
    return 'admin.health.load_failed';
  }

  const rows = $derived(
    data
      ? [
          {
            key: 'database',
            label: $t('admin.health.database.label'),
            value: data.database === 'ok' ? $t('admin.health.database.ok') : $t('admin.health.database.error'),
          },
          {
            key: 'schema',
            label: $t('admin.health.schema.label'),
            value: data.schema_version === null ? $t('admin.health.schema.unknown') : String(data.schema_version),
          },
          {
            key: 'media',
            label: $t('admin.health.media.label'),
            value: data.media_truncated ? `${formatBytes(data.media_bytes)} ${$t('admin.health.media.truncated')}` : formatBytes(data.media_bytes),
          },
          {
            key: 'due',
            label: $t('admin.health.due.label'),
            value: data.due === null ? $t('admin.health.due.unknown') : String(data.due),
          },
        ]
      : []
  );

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });
</script>

<Page testId="admin-health">
  <AdminNav />
  <PageHeader title={$t('admin.health.heading')} testId="admin-health-title" />

  {#if loading}
    <Skeleton testId="admin-health-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-health-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-health-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    <dl class="divide-y divide-border rounded-lg border border-border" data-testid="admin-health-rows">
      {#each rows as row (row.key)}
        <div class="flex items-center justify-between gap-4 px-4 py-3" data-testid="admin-health-row-{row.key}">
          <dt class="text-sm text-muted-foreground">{row.label}</dt>
          <dd class="text-sm font-medium tabular-nums text-foreground">{row.value}</dd>
        </div>
      {/each}
    </dl>
  {/if}
</Page>

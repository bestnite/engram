<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminSummary } from '../../api';
  import AdminNav from './AdminNav.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminSummary | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminSummary | null>(initialData);

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminSummary();
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
    return 'admin.dashboard.load_failed';
  }

  const cards = $derived(
    data
      ? [
          {
            label: $t('admin.dashboard.users'),
            value: $t('admin.dashboard.users_value', { total: data.users_total, active: data.users_active }),
            href: '/admin/users',
          },
          { label: $t('admin.dashboard.decks'), value: String(data.decks), href: '/decks' },
          {
            label: $t('admin.dashboard.notes_cards'),
            value: $t('admin.dashboard.notes_cards_value', { notes: data.notes, cards: data.cards }),
            href: '/decks',
          },
          { label: $t('admin.dashboard.due'), value: String(data.due), href: '/admin/health' },
          {
            label: $t('admin.dashboard.jobs'),
            value: $t('admin.dashboard.jobs_value', { running: data.jobs_running, failed: data.jobs_failed }),
            href: '/admin/jobs',
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

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-dashboard">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="admin-dashboard-title">
      {$t('admin.dashboard.heading')}
    </h1>
  </header>

  {#if loading}
    <div data-testid="admin-dashboard-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
      <p class="text-sm">{$t('common.loading')}</p>
    </div>
  {:else if loadError}
    <div data-testid="admin-dashboard-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="text-base font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button
        type="button"
        data-testid="admin-dashboard-retry"
        class="btn-press mt-4 cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200"
        onclick={() => load()}
      >
        {$t('common.retry')}
      </button>
    </div>
  {:else if data}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" data-testid="admin-dashboard-cards">
      {#each cards as card (card.label)}
        <a
          href={card.href}
          class="card-elevated block rounded-xl p-5 transition-colors hover:border-zinc-300 dark:hover:border-zinc-700"
        >
          <p class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{card.label}</p>
          <p class="mt-2 text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{card.value}</p>
        </a>
      {/each}
    </div>
  {/if}
</div>

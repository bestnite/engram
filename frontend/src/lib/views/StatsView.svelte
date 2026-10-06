<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, StatsSummary } from '../api';

  interface Props {
    client?: ApiClient;
    initialSummary?: StatsSummary | null;
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
  }

  let {
    client = apiClient,
    initialSummary = null,
    initialLoading = true,
    initialError = null,
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let error = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let summary = $state<StatsSummary | null>(initialSummary);

  function formatNumber(val: number): string {
    return new Intl.NumberFormat($localeStore).format(val);
  }

  function formatPercent(rate: number): string {
    return new Intl.NumberFormat($localeStore, {
      style: 'percent',
      minimumFractionDigits: 1,
      maximumFractionDigits: 1,
    }).format(rate);
  }

  /**
   * 加载当前用户的统计概要（GET /api/v1/stats/summary）
   * 接口契约说明（Gap 记录）：
   * 按照 DESIGN.md §9，服务端原计划提供卡组细分、标签细分、记忆留存分桶直方图、学习曲线等指标。
   * 目前 REST API 仅暴露了 GET /api/v1/stats/summary，返回聚合级 decks, due, reviews_today, reviews_total, retention, notes, cards。
   * 本页面严守数据真实性原则，完整呈现接口提供的全部指标，在后续 API 扩充前绝不捏造任何分桶或明细图表数据。
   */
  async function fetchStats(): Promise<void> {
    loading = true;
    error = null;
    try {
      summary = await client.getStatsSummary();
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (initialSummary === null && initialError === null) {
      fetchStats();
    }
  });

  const isEmpty = $derived(
    summary !== null &&
    summary.decks === 0 &&
    summary.reviews_total === 0 &&
    summary.cards === 0
  );
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-6 sm:p-8 rounded-xl space-y-8">
    <div class="flex items-center justify-between pb-4 border-b border-zinc-200 dark:border-zinc-800">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
          {$t('stats.title')}
        </h1>
      </div>
      {#if !loading && !error && !isEmpty}
        <button
          type="button"
          class="text-xs px-2.5 py-1.5 rounded-md font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors btn-press cursor-pointer"
          onclick={fetchStats}
        >
          {$t('stats.retry')}
        </button>
      {/if}
    </div>

    {#if loading}
      <div data-testid="stats-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
        <p class="text-sm">{$t('stats.loading')}</p>
      </div>
    {:else if error}
      <div
        data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'stats-unauthorized' : 'stats-failed'}
        class="py-10 text-center"
      >
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400 mb-3">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <p class="text-base font-medium text-zinc-900 dark:text-zinc-100 mb-2">
          {#if error instanceof ApiClientError && error.isUnauthorized}
            {$t('stats.unauthorized')}
          {:else}
            {$t('stats.failed')}
          {/if}
        </p>
        <div class="mt-4">
          <button
            data-testid="stats-retry"
            type="button"
            class="px-4 py-2 text-sm font-medium rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
            onclick={fetchStats}
          >
            {$t('stats.retry')}
          </button>
        </div>
      </div>
    {:else if isEmpty}
      <div data-testid="stats-empty" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-zinc-100 dark:bg-zinc-800 text-zinc-400 dark:text-zinc-500 mb-3">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
          </svg>
        </div>
        <p class="text-base font-medium text-zinc-700 dark:text-zinc-300">
          {$t('stats.empty')}
        </p>
      </div>
    {:else if summary}
      <div data-testid="stats-data" class="space-y-8">
        <!-- 概览指标行 -->
        <section class="space-y-3">
          <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
            {$t('stats.summary_heading')}
          </h2>
          <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_due')}</div>
              <div data-testid="stats-metric-due" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {formatNumber(summary.due)}
              </div>
            </div>
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_today')}</div>
              <div data-testid="stats-metric-today" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {formatNumber(summary.reviews_today)}
              </div>
            </div>
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_total')}</div>
              <div data-testid="stats-metric-total" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {formatNumber(summary.reviews_total)}
              </div>
            </div>
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_retention')}</div>
              <div data-testid="stats-metric-retention" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {summary.reviews_total > 0 ? formatPercent(summary.retention) : $t('stats.retention_na')}
              </div>
            </div>
          </div>
        </section>

        <!-- 记忆留存率进度条 -->
        <section class="card-subtle p-5 rounded-lg space-y-3">
          <div class="flex items-center justify-between text-sm">
            <span class="font-semibold text-zinc-900 dark:text-zinc-100">
              {$t('stats.retention_heading')}
            </span>
            <span class="font-mono font-bold text-emerald-600 dark:text-emerald-400 text-base">
              {summary.reviews_total > 0 ? formatPercent(summary.retention) : $t('stats.retention_na')}
            </span>
          </div>
          <div class="h-3 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden">
            <div
              class="h-full rounded-full bg-emerald-600 dark:bg-emerald-500 transition-all"
              style="width: {summary.reviews_total > 0 ? Math.min(100, Math.max(0, summary.retention * 100)) : 0}%"
            ></div>
          </div>
        </section>

        <!-- 内容体量 -->
        <section class="space-y-3">
          <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
            {$t('stats.content_heading')}
          </h2>
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_decks')}</div>
              <div data-testid="stats-metric-decks" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {formatNumber(summary.decks)}
              </div>
            </div>
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_notes')}</div>
              <div data-testid="stats-metric-notes" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {formatNumber(summary.notes)}
              </div>
            </div>
            <div class="card-subtle p-4 rounded-lg">
              <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_cards')}</div>
              <div data-testid="stats-metric-cards" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
                {formatNumber(summary.cards)}
              </div>
            </div>
          </div>
        </section>
      </div>
    {/if}
  </div>
</div>

<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, StatsSummary, Deck } from '../api';

  interface Props {
    client?: ApiClient;
    initialSummary?: StatsSummary | null;
    initialDecks?: Deck[];
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
  }

  let {
    client = apiClient,
    initialSummary = null,
    initialDecks = [],
    initialLoading = true,
    initialError = null,
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let error = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let summary = $state<StatsSummary | null>(initialSummary);
  // svelte-ignore state_referenced_locally
  let decks = $state<Deck[]>(initialDecks);

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
   * 加载首页所需数据：并发获取统计概要与卡组列表
   * 接口契约与数据口径说明（Gap 记录）：
   * 1. 当前 REST API (GET /api/v1/decks) 仅提供卡组配置（new_per_day, reviews_per_day），
   *    尚未提供卡组维度的今日剩余可刷卡数（schedule.DeckCounts 仅用于服务端 templ 列表）；
   * 2. 当前 REST API (GET /api/v1/stats/summary) 提供全库聚合的 due, reviews_today, reviews_total, retention, decks, notes, cards，
   *    但未提供连续打卡天数（streak）。
   * 本视图严守数据真实性原则，仅展示接口实际提供的指标，绝不在前端伪造或编造今日剩余卡数或打卡天数。
   */
  async function loadHomeData(): Promise<void> {
    loading = true;
    error = null;
    try {
      const [statsRes, decksRes] = await Promise.all([
        client.getStatsSummary(),
        client.getDecks(),
      ]);
      summary = statsRes;
      decks = decksRes.decks;
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (initialSummary === null && initialDecks.length === 0 && initialError === null) {
      loadHomeData();
    }
  });
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-6 sm:p-8 rounded-xl space-y-6">
    {#if loading}
      <div data-testid="home-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
        <p class="text-sm">{$t('home.loading')}</p>
      </div>
    {:else if error}
      <div
        data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'home-unauthorized' : 'home-failed'}
        class="py-10 text-center"
      >
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400 mb-3">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <p class="text-base font-medium text-zinc-900 dark:text-zinc-100 mb-2">
          {#if error instanceof ApiClientError && error.isUnauthorized}
            {$t('home.unauthorized')}
          {:else}
            {$t('home.failed')}
          {/if}
        </p>
        <div class="mt-4">
          <button
            data-testid="home-retry"
            type="button"
            class="px-4 py-2 text-sm font-medium rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
            onclick={loadHomeData}
          >
            {$t('home.retry')}
          </button>
        </div>
      </div>
    {:else if summary}
      <div data-testid="home-data" class="space-y-6">
        <!-- 顶部操作区 -->
        <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 pb-4 border-b border-zinc-200 dark:border-zinc-800">
          <div>
            <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
              {$t('home.title')}
            </h1>
            <p class="text-sm text-zinc-600 dark:text-zinc-400 mt-1">
              {$t('shell.subtitle')}
            </p>
          </div>
          <div class="flex items-center gap-3 shrink-0">
            {#if summary.due > 0}
              <a
                href="/review"
                data-testid="home-start-review"
                class="inline-flex items-center justify-center rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white active:scale-[0.98] transition-all btn-press"
              >
                <svg class="mr-2 h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <polygon points="5 3 19 12 5 21 5 3"></polygon>
                </svg>
                {$t('home.start_review')} ({formatNumber(summary.due)})
              </a>
            {:else}
              <span class="inline-flex items-center px-3 py-1.5 rounded-full text-xs font-medium bg-emerald-100 text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300">
                {$t('home.all_caught_up')}
              </span>
            {/if}
          </div>
        </div>

        <!-- 聚合指标卡片 -->
        <div data-testid="home-summary" class="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <div class="card-elevated p-4 rounded-lg">
            <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('home.due_count')}</div>
            <div data-testid="home-due-count" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
              {formatNumber(summary.due)}
            </div>
          </div>
          <div class="card-elevated p-4 rounded-lg">
            <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('home.reviews_today')}</div>
            <div data-testid="home-reviews-today" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
              {formatNumber(summary.reviews_today)}
            </div>
          </div>
          <div class="card-elevated p-4 rounded-lg">
            <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('home.retention')}</div>
            <div data-testid="home-retention" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
              {summary.reviews_total > 0 ? formatPercent(summary.retention) : $t('stats.retention_na')}
            </div>
          </div>
          <div class="card-elevated p-4 rounded-lg">
            <div class="text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.metric_decks')}</div>
            <div data-testid="home-decks-count" class="text-2xl font-bold text-zinc-900 dark:text-zinc-100 mt-1">
              {formatNumber(summary.decks)}
            </div>
          </div>
        </div>

        <!-- 卡组列表区 -->
        <div class="space-y-4 pt-2">
          <div class="flex items-center justify-between">
            <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">
              {$t('home.decks_title')}
            </h2>
            <a
              href="/decks"
              class="text-xs font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
            >
              {$t('home.view_all_decks')}
            </a>
          </div>

          {#if decks.length === 0}
            <div data-testid="home-empty-decks" class="card-elevated py-8 text-center text-zinc-500 dark:text-zinc-400 rounded-lg">
              <p class="text-sm">{$t('home.no_decks')}</p>
            </div>
          {:else}
            <div data-testid="home-decks-list" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {#each decks as deck (deck.id)}
                <div class="card-elevated p-4 rounded-lg flex flex-col justify-between hover:border-zinc-300 dark:hover:border-zinc-700 transition-colors">
                  <div>
                    <div class="flex items-start justify-between gap-2 mb-1">
                      <h3 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
                        {deck.name}
                      </h3>
                      {#if deck.visibility}
                        <span class="text-xs px-2 py-0.5 rounded bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 font-mono">
                          {deck.visibility}
                        </span>
                      {/if}
                    </div>
                    {#if deck.description}
                      <p class="text-xs text-zinc-600 dark:text-zinc-400 line-clamp-2 mb-2">
                        {deck.description}
                      </p>
                    {/if}
                  </div>
                  <div class="pt-3 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-between text-xs">
                    <span class="text-zinc-400 dark:text-zinc-500" title={$t('home.deck_limits')}>
                      {deck.new_per_day} / {deck.reviews_per_day}
                    </span>
                    <a
                      href="/review?deck={deck.id}"
                      class="px-2.5 py-1 rounded bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-medium hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press"
                    >
                      {$t('home.deck_review')}
                    </a>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </div>
    {/if}
  </div>
</div>

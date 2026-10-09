<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, StatsSummary, Deck } from '../api';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import { listClasses } from '../components/ui/variants';

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
  // 每个卡组今日还能刷的新卡/复习数；取不到时为空，列表只显示名称。
  let queueCounts = $state<Record<string, { new_count: number; review_count: number }>>({});

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
      const [statsRes, decksRes, countsRes] = await Promise.all([
        client.getStatsSummary(),
        client.getDecks(),
        // 队列计数只是列表上的补充数字，失败不应该让整个首页报错。
        client.getDeckQueueCounts().catch(() => null),
      ]);
      summary = statsRes;
      decks = decksRes.decks;
      queueCounts = Object.fromEntries((countsRes?.decks ?? []).map((count) => [count.deck_id, count]));
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

<Page>
  {#if loading}
    <Skeleton testId="home-loading" label={$t('home.loading')} />
  {:else if error}
    <div
      data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'home-unauthorized' : 'home-failed'}
      class="py-16 text-center"
    >
      <p class="text-base font-medium text-foreground">
        {#if error instanceof ApiClientError && error.isUnauthorized}
          {$t('home.unauthorized')}
        {:else}
          {$t('home.failed')}
        {/if}
      </p>
      <div class="mt-4">
        <Button testId="home-retry" type="button" onclick={loadHomeData} variant="outline" size="lg">
          {$t('home.retry')}
        </Button>
      </div>
    </div>
  {:else if summary}
    <div data-testid="home-data">
      <PageHeader title={$t('home.title')}>
        {#snippet actions()}
          {#if summary && summary.due > 0}
            <Button href="/review" testId="home-start-review" variant="primary" size="lg">
              {$t('home.start_review')}
              <span class="rounded bg-primary-foreground/15 px-1.5 text-xs tabular-nums">{formatNumber(summary.due)}</span>
            </Button>
          {:else}
            <Badge variant="success" class="px-2.5 py-1">{$t('home.all_caught_up')}</Badge>
          {/if}
        {/snippet}
      </PageHeader>

      <!-- 聚合指标：一条横向指标带，四项之间只用竖线分隔，不再是四张卡片。 -->
      <dl data-testid="home-summary" class="grid grid-cols-2 border-y border-border sm:grid-cols-4">
        <div class="border-border py-4 pr-4 max-sm:border-b sm:border-r">
          <dt class="text-xs text-muted-foreground">{$t('home.due_count')}</dt>
          <dd data-testid="home-due-count" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{formatNumber(summary.due)}</dd>
        </div>
        <div class="border-border py-4 pl-4 max-sm:border-b sm:border-r sm:pr-4">
          <dt class="text-xs text-muted-foreground">{$t('home.reviews_today')}</dt>
          <dd data-testid="home-reviews-today" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{formatNumber(summary.reviews_today)}</dd>
        </div>
        <div class="border-border py-4 pr-4 sm:border-r sm:pl-4">
          <dt class="text-xs text-muted-foreground">{$t('home.retention')}</dt>
          <dd data-testid="home-retention" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{summary.reviews_total > 0 ? formatPercent(summary.retention) : $t('stats.retention_na')}</dd>
        </div>
        <div class="py-4 pl-4">
          <dt class="text-xs text-muted-foreground">{$t('stats.metric_decks')}</dt>
          <dd data-testid="home-decks-count" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{formatNumber(summary.decks)}</dd>
        </div>
      </dl>

      <section class="mt-10">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-base font-semibold text-foreground">{$t('home.decks_title')}</h2>
          <a href="/decks" class="text-[13px] text-muted-foreground transition-colors hover:text-foreground">
            {$t('home.view_all_decks')}
          </a>
        </div>

        {#if decks.length === 0}
          <div data-testid="home-empty-decks" class="rounded-lg border border-dashed border-border py-10 text-center text-sm text-muted-foreground">
            {$t('home.no_decks')}
          </div>
        {:else}
          <div data-testid="home-decks-list" class={listClasses.root}>
            {#each decks as deck (deck.id)}
              {@const counts = queueCounts[deck.id]}
              <div class={listClasses.row}>
                <div class="flex min-w-0 flex-1 flex-col">
                  <a href="/decks/{encodeURIComponent(deck.id)}" class={listClasses.rowLink}>{deck.name}</a>
                  {#if deck.description}
                    <span class="truncate text-[13px] text-muted-foreground">{deck.description}</span>
                  {/if}
                </div>
                {#if counts}
                  <span class="shrink-0 text-[13px] tabular-nums text-muted-foreground">
                    {$t('decks.queue_counts', { new: counts.new_count, review: counts.review_count })}
                  </span>
                {/if}
                <Button
                  href="/review?deck={encodeURIComponent(deck.id)}"
                  variant="outline"
                  size="sm"
                  class={listClasses.rowAction}
                >
                  {$t('home.deck_review')}
                </Button>
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</Page>

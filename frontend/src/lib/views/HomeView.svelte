<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { formatNumber, formatPercent } from '../format';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, StatsSummary, Deck, DeckQueueCount } from '../api';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import HelpTip from '../components/ui/HelpTip.svelte';
  import DeckProgressBar from '../components/DeckProgressBar.svelte';
  import { listClasses } from '../components/ui/variants';
  import { cn } from '../components/ui/utils';
  import { ArrowRight, CalendarClock, CheckCheck, CircleCheck, CirclePause, Layers, Play, RefreshCw, Target } from '@lucide/svelte';
  import EmptyState from '../components/ui/EmptyState.svelte';

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
  // How many new cards/reviews each deck can still do today; empty when unavailable, in which case the list only shows names.
  let queueCounts = $state<Record<string, DeckQueueCount>>({});
  // The home list shows what the default review queue draws on, so the decks the user paused stay on the deck list page only.
  let activeDecks = $derived(decks.filter((deck) => !deck.paused));

  // A count above zero is today's work and reads in the foreground colour; a zero fades to grey.
  function countClass(count: number): string {
    return count > 0 ? 'font-medium text-foreground' : 'text-muted-foreground';
  }

  /**
   * Load the data the home page needs: fetch the stats summary and the deck list concurrently
   * API contract and metric definitions (Gap notes):
   * 1. GET /api/v1/decks only provides deck configuration (new_per_day, reviews_per_day); the per-deck count of cards still available today
   *    comes from GET /api/v1/decks/queue-counts (getDeckQueueCounts), which is supplementary and may fail without failing the page;
   * 2. GET /api/v1/stats/summary provides the whole-library aggregates due, reviews_today, reviews_total, retention (plus its denominator
   *    retention_total / numerator retention_passed), decks, notes, cards and the consecutive check-in streak (streak).
   * This view strictly follows the principle of data truthfulness: it only shows the metrics the API actually provides, and never fabricates
   * or invents today's remaining card count or streak on the frontend.
   */
  async function loadHomeData(): Promise<void> {
    loading = true;
    error = null;
    try {
      const [statsRes, decksRes, countsRes] = await Promise.all([
        client.getStatsSummary(),
        client.getDecks(),
        // Queue counts are just supplementary numbers on the list; a failure should not make the whole home page error out.
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
          <RefreshCw class="size-4" aria-hidden="true" />
          <span>{$t('home.retry')}</span>
        </Button>
      </div>
    </div>
  {:else if summary}
    <div data-testid="home-data">
      <PageHeader title={$t('home.title')}>
        {#snippet actions()}
          {#if summary && summary.due > 0}
            <Button href="/review" testId="home-start-review" variant="primary" size="lg">
              <Play class="size-4" aria-hidden="true" />
              <span>{$t('home.start_review')}</span>
              <span class="rounded bg-primary-foreground/15 px-1.5 text-xs tabular-nums">{formatNumber(summary.due, $localeStore)}</span>
            </Button>
          {:else}
            <Badge variant="success" class="px-2.5 py-1"><CircleCheck class="size-3.5" aria-hidden="true" />{$t('home.all_caught_up')}</Badge>
          {/if}
        {/snippet}
      </PageHeader>

      <!-- Aggregate metrics: one horizontal metric band, the four items separated only by vertical bars, no longer four cards. -->
      <dl data-testid="home-summary" class="grid grid-cols-2 border-y border-border sm:grid-cols-4">
        <div class="border-border px-4 py-4 text-center max-sm:border-b max-sm:border-r sm:border-r">
          <dt class="flex items-center justify-center gap-0.5 text-xs text-muted-foreground">
            <CalendarClock class="mr-0.5 size-3.5" aria-hidden="true" />
            {$t('home.due_count')}
            <HelpTip testId="home-due-help" label={$t('home.due_count')} text={$t('help.home.due')} />
          </dt>
          <dd data-testid="home-due-count" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{formatNumber(summary.due, $localeStore)}</dd>
        </div>
        <div class="border-border px-4 py-4 text-center max-sm:border-b sm:border-r">
          <dt class="flex items-center justify-center gap-0.5 text-xs text-muted-foreground">
            <CheckCheck class="mr-0.5 size-3.5" aria-hidden="true" />
            {$t('home.reviews_today')}
            <HelpTip testId="home-reviews-today-help" label={$t('home.reviews_today')} text={$t('help.home.reviews_today')} />
          </dt>
          <dd data-testid="home-reviews-today" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{formatNumber(summary.reviews_today, $localeStore)}</dd>
        </div>
        <div class="border-border px-4 py-4 text-center max-sm:border-r sm:border-r">
          <dt class="flex items-center justify-center gap-0.5 text-xs text-muted-foreground">
            <Target class="mr-0.5 size-3.5" aria-hidden="true" />
            {$t('home.retention')}
            <HelpTip testId="home-retention-help" label={$t('home.retention')} text={$t('help.retention')} />
          </dt>
          <!-- The denominator only counts due reviews: it is 0 when only new cards and learning steps have been done, and there is no readable retention rate then, so "no data" is shown. -->
          <dd data-testid="home-retention" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{summary.retention_total > 0 ? formatPercent(summary.retention, $localeStore) : $t('stats.retention_na')}</dd>
        </div>
        <div class="px-4 py-4 text-center">
          <dt class="flex items-center justify-center gap-0.5 text-xs text-muted-foreground">
            <Layers class="mr-0.5 size-3.5" aria-hidden="true" />
            {$t('stats.metric_decks')}
          </dt>
          <dd data-testid="home-decks-count" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{formatNumber(summary.decks, $localeStore)}</dd>
        </div>
      </dl>

      <section class="mt-10">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-base font-semibold text-foreground">{$t('home.decks_title')}</h2>
          <a href="/decks" class="inline-flex items-center gap-0.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground">
            <span>{$t('home.view_all_decks')}</span>
            <ArrowRight class="size-3.5" aria-hidden="true" />
          </a>
        </div>

        {#if activeDecks.length === 0}
          <EmptyState
            testId="home-empty-decks"
            class="py-10"
            icon={decks.length === 0 ? Layers : CirclePause}
            text={$t(decks.length === 0 ? 'home.no_decks' : 'home.all_paused')}
          />
        {:else}
          <!-- A table: every row is a subgrid of the list, so each column takes the width of its widest cell and the counts and
               the bars line up across rows whatever the numbers are. From sm up the name is the first of four columns; below sm it
               spans the row and the three count columns sit under it, labelled inline because the header is hidden there.
               The whole row is the review entry for that deck; the deck detail stays one click away in the sidebar. -->
          <div
            data-testid="home-decks-list"
            class={cn(listClasses.root, 'grid grid-cols-[auto_auto_minmax(0,1fr)] sm:grid-cols-[minmax(0,1fr)_auto_auto_auto]')}
          >
            <div class={cn(listClasses.header, 'hidden sm:col-span-full sm:grid sm:grid-cols-subgrid sm:gap-x-8')}>
              <span>{$t('list.col.name')}</span>
              <span class="text-right">{$t('home.col.new')}</span>
              <span class="text-right">{$t('home.col.review')}</span>
              <span title={$t('decks.progress_hint')}>{$t('list.col.progress')}</span>
            </div>
            {#each activeDecks as deck (deck.id)}
              {@const counts = queueCounts[deck.id]}
              <a
                href="/review?deck={encodeURIComponent(deck.id)}"
                data-testid={`home-deck-row-${deck.id}`}
                class={cn(
                  listClasses.row,
                  'col-span-full grid grid-cols-subgrid gap-x-5 gap-y-1.5 outline-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring sm:gap-x-8'
                )}
              >
                <div class="col-span-full flex min-w-0 flex-col sm:col-span-1">
                  <span class="truncate font-medium text-foreground">{deck.name}</span>
                  {#if deck.description}
                    <span class="truncate text-[13px] text-muted-foreground">{deck.description}</span>
                  {/if}
                </div>
                {#if counts}
                  <!-- Counts above zero are in the foreground colour and zeros are grey, so only decks with work left draw the eye. -->
                  <div data-testid={`home-deck-stats-${deck.id}`} class="contents">
                    <span data-testid={`home-deck-new-${deck.id}`} class="text-[13px] tabular-nums sm:text-right {countClass(counts.new_count)}">
                      <span class="font-normal text-muted-foreground sm:hidden">{$t('home.col.new')}</span>
                      {formatNumber(counts.new_count, $localeStore)}
                    </span>
                    <span data-testid={`home-deck-review-${deck.id}`} class="text-[13px] tabular-nums sm:text-right {countClass(counts.review_count)}">
                      <span class="font-normal text-muted-foreground sm:hidden">{$t('home.col.review')}</span>
                      {formatNumber(counts.review_count, $localeStore)}
                    </span>
                    <span class="flex items-center gap-2">
                      <DeckProgressBar
                        studied={counts.studied_count}
                        remaining={counts.remaining_count}
                        testId={`home-deck-progress-${deck.id}`}
                        class="w-20 sm:w-24"
                      />
                      <span class="text-xs tabular-nums text-muted-foreground sm:hidden">
                        {$t('deck_progress.value', { studied: counts.studied_count, total: counts.studied_count + counts.remaining_count })}
                      </span>
                    </span>
                  </div>
                {/if}
              </a>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</Page>

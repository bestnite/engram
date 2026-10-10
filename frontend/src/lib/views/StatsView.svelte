<script lang="ts">
  import LearningCurveChart from '../components/LearningCurveChart.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import { RefreshCw } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, StatsDetail } from '../api';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import HelpTip from '../components/ui/HelpTip.svelte';

  interface Props {
    client?: ApiClient;
    initialDetail?: StatsDetail | null;
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
  }

  let {
    client = apiClient,
    initialDetail = null,
    initialLoading = true,
    initialError = null,
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let error = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let detail = $state<StatsDetail | null>(initialDetail);

  function formatNumber(val: number): string {
    return new Intl.NumberFormat($localeStore).format(val);
  }

  function formatPercentValue(rate: number): string {
    return (rate * 100).toFixed(1);
  }

  /** 表格行的留存率：分母（到期复习数）为 0 时没有可读的比例，显示「暂无数据」而不是 0.0%。 */
  function rowRetention(rate: number, total: number): string {
    return total > 0 ? $t('stats.table.rate', { rate: formatPercentValue(rate) }) : $t('stats.retention_na');
  }

  /** 学习曲线/柱状条共用最大值基准，柱长因此可以直接互相比较。 */
  function maxOf(values: number[]): number {
    return values.reduce((max, v) => Math.max(max, v), 0);
  }

  /** 柱宽：value/max 的百分比；value 或 max 非正时返回 0（该行不画柱）。 */
  function barWidth(value: number, max: number): number {
    if (value <= 0 || max <= 0) return 0;
    return Math.min(100, (value / max) * 100);
  }

  /** 比例柱宽：0–1 的比例直接转成百分比。 */
  function percentWidth(rate: number): number {
    if (rate <= 0) return 0;
    return Math.min(100, rate * 100);
  }

  function round1(v: number): number {
    return Math.round(v * 10) / 10;
  }

  /** 整值省掉 .0，与 SSR 的 decimalText 口径一致。 */
  function decimalText(v: number): string {
    return Number.isInteger(v) ? String(v) : v.toFixed(1);
  }

  /**
   * 耗时按量级显示（与 internal/web/elapsed.go 的 elapsedLabel 同口径）：
   * < 1 秒毫秒、< 1 分钟秒（一位小数）、< 1 小时分钟（一位小数）、更长「N 小时 M 分」。
   */
  function formatDuration(ms: number): string {
    const value = ms < 0 ? 0 : ms;
    if (value < 1000) {
      return $t('stats.duration.milliseconds', { value: String(value) });
    }
    const secs = round1(value / 1000);
    if (secs < 60) {
      return $t('stats.duration.seconds', { value: decimalText(secs) });
    }
    const mins = round1(value / 60000);
    if (mins < 60) {
      return $t('stats.duration.minutes', { value: decimalText(mins) });
    }
    const total = Math.round(round1(value / 60000));
    const hours = Math.floor(total / 60);
    const rest = total % 60;
    if (rest === 0) {
      return $t('stats.duration.hours', { hours: String(hours) });
    }
    return $t('stats.duration.hours_minutes', { hours: String(hours), minutes: String(rest) });
  }

  /** 稳定性桶标签映射到语言包 key；未知标签回退到「总体」，不出现裸标识符。 */
  function retentionBucketKey(label: string): string {
    switch (label) {
      case '<1d':
        return 'stats.retention.bucket.lt1';
      case '1-7d':
        return 'stats.retention.bucket.d1_7';
      case '7-30d':
        return 'stats.retention.bucket.d7_30';
      case '30-90d':
        return 'stats.retention.bucket.d30_90';
      case '90-180d':
        return 'stats.retention.bucket.d90_180';
      case '180-365d':
        return 'stats.retention.bucket.d180_365';
      case '365d+':
        return 'stats.retention.bucket.d365plus';
      default:
        return 'stats.retention.overall';
    }
  }

  /** 判分来源映射到语言包 key；未知来源显示「其它」。 */
  function gradeSourceKey(source: string): string {
    switch (source) {
      case 'self':
        return 'stats.grade.self';
      case 'typed':
        return 'stats.grade.typed';
      case 'llm':
        return 'stats.grade.llm';
      default:
        return 'stats.grade.other';
    }
  }

  /**
   * 加载当前用户统计明细（GET /api/v1/stats/detail）。
   * 数值口径与 SSR 统计页同源（internal/web/stats_api.go）；本页只做本地化与柱宽，
   * 不自行聚合、不捏造任何分桶或图表数据。
   */
  async function fetchStats(): Promise<void> {
    loading = true;
    error = null;
    try {
      detail = await client.getStatsDetail();
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (initialDetail === null && initialError === null) {
      fetchStats();
    }
  });

  const dueMax = $derived(
    detail
      ? maxOf([
          detail.due.today,
          detail.due.tomorrow,
          detail.due.within_7_days,
          detail.due.within_30_days,
          detail.due.later,
          detail.due.new_not_due,
        ])
      : 0
  );
  const gradeMax = $derived(detail ? maxOf(detail.grades.map((g) => g.count)) : 0);
</script>

<Page>
  <PageHeader title={$t('stats.title')}>
    {#snippet actions()}
      {#if !loading && !error && detail && !detail.empty}
        <Button variant="ghost" size="icon" label={$t('stats.retry')} title={$t('stats.retry')} onclick={fetchStats}>
          <RefreshCw class="size-4" aria-hidden="true" />
        </Button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loading}
    <Skeleton testId="stats-loading" label={$t('stats.loading')} />
  {:else if error}
    <div
      data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'stats-unauthorized' : 'stats-failed'}
      class="py-16 text-center"
    >
      <p class="text-base font-medium text-foreground">
        {#if error instanceof ApiClientError && error.isUnauthorized}
          {$t('stats.unauthorized')}
        {:else}
          {$t('stats.failed')}
        {/if}
      </p>
      <Button testId="stats-retry" type="button" onclick={fetchStats} variant="outline" size="lg" class="mt-4">
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('stats.retry')}</span>
      </Button>
    </div>
  {:else if detail && detail.empty}
    <div data-testid="stats-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">
      {$t('stats.empty')}
    </div>
  {:else if detail}
    <div data-testid="stats-data" class="divide-y divide-border">
      <!-- 指标带：复习量与连续打卡，一眼看到最常看的五个数。 -->
      <div class="grid grid-cols-2 pb-8 sm:grid-cols-5 sm:divide-x sm:divide-border">
        <div data-testid="stats-volume" class="contents">
          {#each [
            { key: 'stats.volume.today', value: detail.volume.today },
            { key: 'stats.volume.last7', value: detail.volume.last_7_days },
            { key: 'stats.volume.last30', value: detail.volume.last_30_days },
          ] as row, i}
            <dl class="px-4 py-2 first:pl-0">
              <dt class="flex items-center gap-0.5 text-xs text-muted-foreground">
                {$t('stats.volume.heading')} · {$t(row.key)}
                {#if i === 0}
                  <HelpTip testId="stats-volume-help" label={$t('stats.volume.heading')} text={$t('help.stats.volume')} />
                {/if}
              </dt>
              <dd class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{$t('stats.volume.value', { count: formatNumber(row.value) })}</dd>
            </dl>
          {/each}
        </div>
        <div data-testid="stats-streak" class="contents">
          <dl class="px-4 py-2">
            <dt class="flex items-center gap-0.5 text-xs text-muted-foreground">
              {$t('stats.streak.current_label')}
              <HelpTip testId="stats-streak-help" label={$t('stats.streak.heading')} text={$t('help.stats.streak')} />
            </dt>
            <dd data-testid="stats-streak-current" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{$t('stats.streak.days', { days: formatNumber(detail.streak.current) })}</dd>
          </dl>
          <dl class="px-4 py-2">
            <dt class="text-xs text-muted-foreground">{$t('stats.streak.longest_label')}</dt>
            <dd data-testid="stats-streak-longest" class="mt-1 text-2xl font-semibold tabular-nums text-foreground">{$t('stats.streak.days', { days: formatNumber(detail.streak.longest) })}</dd>
          </dl>
        </div>
      </div>

      <!-- 学习曲线 -->
      <section data-testid="stats-curve" class="py-8">
        <div class="flex items-center gap-1">
          <h2 class="text-sm font-semibold text-foreground">{$t('stats.curve.heading')}</h2>
          <HelpTip testId="stats-curve-help" label={$t('stats.curve.heading')} text={$t('help.stats.curve')} />
        </div>
        <p class="mt-0.5 text-xs text-muted-foreground">{$t('stats.curve.intro')}</p>
        <div class="mt-4">
          {#if detail.curve.length === 0}
            <p class="text-xs text-muted-foreground">{$t('stats.curve.empty')}</p>
          {:else}
            <LearningCurveChart points={detail.curve} from={detail.curve_from} to={detail.curve_to} />
          {/if}
        </div>
      </section>

      <!-- 到期预测 / 留存率 -->
      <div class="grid gap-y-8 py-8 lg:grid-cols-2 lg:gap-x-12">
        <section data-testid="stats-due">
          <div class="flex items-center gap-1">
            <h2 class="text-sm font-semibold text-foreground">{$t('stats.due.heading')}</h2>
            <HelpTip testId="stats-due-help" label={$t('stats.due.heading')} text={$t('help.stats.due')} />
          </div>
          <ul class="mt-4 space-y-3">
            {#each [
              { key: 'stats.due.today', value: detail.due.today },
              { key: 'stats.due.tomorrow', value: detail.due.tomorrow },
              { key: 'stats.due.within7', value: detail.due.within_7_days },
              { key: 'stats.due.within30', value: detail.due.within_30_days },
              { key: 'stats.due.later', value: detail.due.later },
              { key: 'stats.due.new_not_due', value: detail.due.new_not_due },
            ] as row}
              <li>
                <div class="flex items-center justify-between text-[13px]">
                  <span class="text-muted-foreground">{$t(row.key)}</span>
                  <span class="font-medium tabular-nums text-foreground">{$t('stats.due.value', { count: formatNumber(row.value) })}</span>
                </div>
                  {#if barWidth(row.value, dueMax) > 0}
                    <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                      <div class="h-full rounded-full bg-brand" style="width: {barWidth(row.value, dueMax)}%"></div>
                    </div>
                  {/if}
              </li>
            {/each}
          </ul>
        </section>

        <section data-testid="stats-retention">
          <div class="flex items-center gap-1">
            <h2 class="text-sm font-semibold text-foreground">{$t('stats.retention.heading')}</h2>
            <HelpTip testId="stats-retention-help" label={$t('stats.retention.heading')} text={$t('help.retention')} />
          </div>
          <p class="mt-0.5 text-xs text-muted-foreground">
            {$t('stats.retention.intro')}
            <HelpTip testId="stats-retention-buckets-help" label={$t('help.stats.retention_buckets_label')} text={$t('help.stats.retention_buckets')} />
          </p>
          <ul class="mt-4 space-y-3">
            <li data-testid="stats-retention-overall">
              <div class="flex items-center justify-between text-[13px]">
                <span class="font-medium text-foreground">{$t('stats.retention.overall')}</span>
                <span class="font-medium tabular-nums text-foreground">
                  {$t('stats.retention.rate', { rate: formatPercentValue(detail.retention.rate), passed: detail.retention.passed, total: detail.retention.total })}
                </span>
              </div>
                  {#if percentWidth(detail.retention.rate) > 0}
                    <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                      <div class="h-full rounded-full bg-brand" style="width: {percentWidth(detail.retention.rate)}%"></div>
                    </div>
                  {/if}
            </li>
            {#each detail.retention.buckets as bucket}
              <li>
                <div class="flex items-center justify-between text-[13px]">
                  <span class="text-muted-foreground">{$t(retentionBucketKey(bucket.label))}</span>
                  <span class="tabular-nums text-foreground">
                    {$t('stats.retention.rate', { rate: formatPercentValue(bucket.rate), passed: bucket.passed, total: bucket.total })}
                  </span>
                </div>
                  {#if percentWidth(bucket.rate) > 0}
                    <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                      <div class="h-full rounded-full bg-brand" style="width: {percentWidth(bucket.rate)}%"></div>
                    </div>
                  {/if}
              </li>
            {/each}
          </ul>
        </section>
      </div>

      <!-- 时间投入 / 判分来源 -->
      <div class="grid gap-y-8 py-8 lg:grid-cols-2 lg:gap-x-12">
        <section data-testid="stats-time">
          <div class="flex items-center gap-1">
            <h2 class="text-sm font-semibold text-foreground">{$t('stats.time.heading')}</h2>
            <HelpTip testId="stats-time-help" label={$t('stats.time.heading')} text={$t('help.stats.time')} />
          </div>
          <dl class="mt-4 space-y-2.5">
            {#each [
              { key: 'stats.time.total_label', value: formatDuration(detail.time_spent.total_ms) },
              { key: 'stats.time.avg_label', value: formatDuration(Math.trunc(detail.time_spent.avg_ms)) },
              { key: 'stats.time.median_label', value: formatDuration(detail.time_spent.median_ms) },
              { key: 'stats.time.count_label', value: $t('stats.time.count', { count: formatNumber(detail.time_spent.count) }) },
            ] as row}
              <div class="flex items-center justify-between text-[13px]">
                <dt class="text-muted-foreground">{$t(row.key)}</dt>
                <dd class="font-medium tabular-nums text-foreground">{row.value}</dd>
              </div>
            {/each}
          </dl>
        </section>

        <section data-testid="stats-grade">
          <div class="flex items-center gap-1">
            <h2 class="text-sm font-semibold text-foreground">{$t('stats.grade.heading')}</h2>
            <HelpTip testId="stats-grade-help" label={$t('stats.grade.heading')} text={$t('help.stats.grade')} />
          </div>
          <ul class="mt-4 space-y-3">
            {#each detail.grades as grade}
              <li>
                <div class="flex items-center justify-between text-[13px]">
                  <span class="text-muted-foreground">{$t(gradeSourceKey(grade.source))}</span>
                  <span class="font-medium tabular-nums text-foreground">{$t('stats.grade.value', { count: formatNumber(grade.count) })}</span>
                </div>
                  {#if barWidth(grade.count, gradeMax) > 0}
                    <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                      <div class="h-full rounded-full bg-brand" style="width: {barWidth(grade.count, gradeMax)}%"></div>
                    </div>
                  {/if}
              </li>
            {/each}
          </ul>
        </section>
      </div>

      <!-- 卡组维度 / 标签维度 -->
      <div class="grid gap-y-8 pt-8 lg:grid-cols-2 lg:gap-x-12">
        <section data-testid="stats-deck">
          <h2 class="text-sm font-semibold text-foreground">{$t('stats.deck.heading')}</h2>
          {#if detail.decks.length === 0}
            <p class="mt-4 text-xs text-muted-foreground">{$t('stats.deck.empty')}</p>
          {:else}
            <div class="mt-4 overflow-x-auto rounded-lg border border-border">
              <table class="w-full text-left text-[13px]">
                <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
                  <tr>
                    <th class="px-4 py-2.5 font-medium">{$t('stats.deck.col.name')}</th>
                    {#each [
                      { key: 'stats.deck.col.due', help: 'help.stats.deck.due', id: 'due' },
                      { key: 'stats.deck.col.reviews', help: 'help.stats.deck.reviews', id: 'reviews' },
                      { key: 'stats.deck.col.retention', help: 'help.stats.deck.retention', id: 'retention' },
                      { key: 'stats.deck.col.elapsed', help: 'help.stats.deck.elapsed', id: 'elapsed' },
                    ] as col}
                      <th class="px-4 py-2.5 text-right font-medium">
                        <span class="inline-flex items-center gap-0.5 whitespace-nowrap">
                          {$t(col.key)}
                          <HelpTip testId="stats-deck-{col.id}-help" label={$t(col.key)} text={$t(col.help)} />
                        </span>
                      </th>
                    {/each}
                  </tr>
                </thead>
                <tbody class="divide-y divide-border">
                  {#each detail.decks as deck}
                    <tr>
                      <td class="px-4 py-2.5 font-medium text-foreground">{deck.name}</td>
                      <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{$t('stats.due.value', { count: formatNumber(deck.due_count) })}</td>
                      <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{$t('stats.volume.value', { count: formatNumber(deck.reviews) })}</td>
                      <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{rowRetention(deck.retention, deck.retention_total)}</td>
                      <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{formatDuration(deck.elapsed_ms)}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>

        <section data-testid="stats-tag">
          <div class="flex items-center gap-1">
            <h2 class="text-sm font-semibold text-foreground">{$t('stats.tag.heading')}</h2>
            <HelpTip testId="stats-tag-help" label={$t('stats.tag.heading')} text={$t('help.stats.tag')} />
          </div>
          <!-- 口径写在标题下：本维度只数「已复习卡片上的标签」，不写清楚时它会看起来像
               在重复卡组维度（没复习过的大标签一个都不出现）。 -->
          <p data-testid="stats-tag-scope" class="mt-0.5 text-xs text-muted-foreground">{$t('stats.tag.scope')}</p>
          {#if detail.tags.length === 0}
            <p class="mt-4 text-xs text-muted-foreground">{$t('stats.tag.empty')}</p>
          {:else}
            <div class="mt-4 overflow-x-auto rounded-lg border border-border">
              <table class="w-full text-left text-[13px]">
                <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
                  <tr>
                    <th class="px-4 py-2.5 font-medium">{$t('stats.tag.col.tag')}</th>
                    <th class="px-4 py-2.5 text-right font-medium">{$t('stats.tag.col.reviews')}</th>
                    <th class="px-4 py-2.5 text-right font-medium">
                      <span class="inline-flex items-center gap-0.5 whitespace-nowrap">
                        {$t('stats.tag.col.retention')}
                        <HelpTip testId="stats-tag-retention-help" label={$t('stats.tag.col.retention')} text={$t('help.stats.tag.retention')} />
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border">
                  {#each detail.tags as tag}
                    <tr>
                      <td class="px-4 py-2.5 font-medium text-foreground">{tag.tag}</td>
                      <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{$t('stats.volume.value', { count: formatNumber(tag.reviews) })}</td>
                      <td class="px-4 py-2.5 text-right tabular-nums text-muted-foreground">{rowRetention(tag.retention, tag.retention_total)}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>
      </div>
    </div>
  {/if}
</Page>

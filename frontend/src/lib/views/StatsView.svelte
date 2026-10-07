<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, StatsDetail } from '../api';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';

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
   * 数值口径与 SSR 统计页同源（internal/web/spa_stats.go）；本页只做本地化与柱宽，
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

  const volumeMax = $derived(
    detail
      ? maxOf([detail.volume.today, detail.volume.last_7_days, detail.volume.last_30_days])
      : 0
  );
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
  const curveMax = $derived(
    detail ? maxOf(detail.curve.flatMap((p) => [p.new, p.review])) : 0
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
      {#if !loading && !error && detail && !detail.empty}
        <button
          type="button"
          title={$t('stats.retry')}
          aria-label={$t('stats.retry')}
          class="p-2 rounded-xl text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors btn-press cursor-pointer border border-zinc-200 dark:border-zinc-700/80"
          onclick={fetchStats}
        >
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
            <path d="M21 3v5h-5" />
            <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            <path d="M3 21v-5h5" />
          </svg>
        </button>
      {/if}
    </div>

    {#if loading}
      <Skeleton testId="stats-loading" label={$t('stats.loading')} />
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
          <Button testId="stats-retry" type="button" onclick={fetchStats} variant="primary" size="lg">
            <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" />
            </svg>
            <span>{$t('stats.retry')}</span>
          </Button>
        </div>
      </div>
    {:else if detail && detail.empty}
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
    {:else if detail}
      <div data-testid="stats-data" class="space-y-8">
        <!-- 复习量 + 到期预测 -->
        <div class="grid grid-cols-1 gap-6 sm:grid-cols-2">
          <section data-testid="stats-volume" class="card-elevated p-5 rounded-lg space-y-4">
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.volume.heading')}
            </h2>
            <ul class="space-y-3">
              {#each [
                { key: 'stats.volume.today', value: detail.volume.today },
                { key: 'stats.volume.last7', value: detail.volume.last_7_days },
                { key: 'stats.volume.last30', value: detail.volume.last_30_days },
              ] as row}
                <li>
                  <div class="flex items-center justify-between text-xs sm:text-sm">
                    <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t(row.key)}</span>
                    <span class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                      {$t('stats.volume.value', { count: formatNumber(row.value) })}
                    </span>
                  </div>
                  {#if barWidth(row.value, volumeMax) > 0}
                    <div class="mt-1.5 h-2 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
                      <div class="h-full rounded-full bg-indigo-600" style="width: {barWidth(row.value, volumeMax)}%"></div>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          </section>

          <section data-testid="stats-due" class="card-elevated p-5 rounded-lg space-y-4">
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.due.heading')}
            </h2>
            <ul class="space-y-3">
              {#each [
                { key: 'stats.due.today', value: detail.due.today },
                { key: 'stats.due.tomorrow', value: detail.due.tomorrow },
                { key: 'stats.due.within7', value: detail.due.within_7_days },
                { key: 'stats.due.within30', value: detail.due.within_30_days },
                { key: 'stats.due.later', value: detail.due.later },
                { key: 'stats.due.new_not_due', value: detail.due.new_not_due },
              ] as row}
                <li>
                  <div class="flex items-center justify-between text-xs sm:text-sm">
                    <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t(row.key)}</span>
                    <span class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                      {$t('stats.due.value', { count: formatNumber(row.value) })}
                    </span>
                  </div>
                  {#if barWidth(row.value, dueMax) > 0}
                    <div class="mt-1.5 h-2 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
                      <div class="h-full rounded-full bg-indigo-600" style="width: {barWidth(row.value, dueMax)}%"></div>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          </section>
        </div>

        <!-- 留存率：总体 + 每个稳定性桶 -->
        <section data-testid="stats-retention" class="card-elevated p-5 rounded-lg space-y-4">
          <div>
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.retention.heading')}
            </h2>
            <p class="mt-0.5 text-xs text-zinc-400 dark:text-zinc-500">{$t('stats.retention.intro')}</p>
          </div>
          <ul class="space-y-3">
            <li data-testid="stats-retention-overall">
              <div class="flex items-center justify-between text-xs sm:text-sm">
                <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t('stats.retention.overall')}</span>
                <span class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                  {$t('stats.retention.rate', {
                    rate: formatPercentValue(detail.retention.rate),
                    passed: detail.retention.passed,
                    total: detail.retention.total,
                  })}
                </span>
              </div>
              {#if percentWidth(detail.retention.rate) > 0}
                <div class="mt-1.5 h-2 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
                  <div class="h-full rounded-full bg-indigo-600" style="width: {percentWidth(detail.retention.rate)}%"></div>
                </div>
              {/if}
            </li>
            {#each detail.retention.buckets as bucket}
              <li>
                <div class="flex items-center justify-between text-xs sm:text-sm">
                  <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t(retentionBucketKey(bucket.label))}</span>
                  <span class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                    {$t('stats.retention.rate', {
                      rate: formatPercentValue(bucket.rate),
                      passed: bucket.passed,
                      total: bucket.total,
                    })}
                  </span>
                </div>
                {#if percentWidth(bucket.rate) > 0}
                  <div class="mt-1.5 h-2 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
                    <div class="h-full rounded-full bg-indigo-600" style="width: {percentWidth(bucket.rate)}%"></div>
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        </section>

        <!-- 时间投入 / 连续打卡 / 判分来源 -->
        <div class="grid grid-cols-1 gap-6 sm:grid-cols-3">
          <section data-testid="stats-time" class="card-elevated p-5 rounded-lg space-y-4">
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.time.heading')}
            </h2>
            <ul class="space-y-3">
              {#each [
                { key: 'stats.time.total_label', value: formatDuration(detail.time_spent.total_ms) },
                { key: 'stats.time.avg_label', value: formatDuration(Math.trunc(detail.time_spent.avg_ms)) },
                { key: 'stats.time.median_label', value: formatDuration(detail.time_spent.median_ms) },
                { key: 'stats.time.count_label', value: $t('stats.time.count', { count: formatNumber(detail.time_spent.count) }) },
              ] as row}
                <li class="flex items-center justify-between text-xs sm:text-sm">
                  <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t(row.key)}</span>
                  <span class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">{row.value}</span>
                </li>
              {/each}
            </ul>
          </section>

          <section data-testid="stats-streak" class="card-elevated p-5 rounded-lg space-y-4">
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.streak.heading')}
            </h2>
            <ul class="space-y-3">
              <li class="flex items-center justify-between text-xs sm:text-sm">
                <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t('stats.streak.current_label')}</span>
                <span data-testid="stats-streak-current" class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                  {$t('stats.streak.days', { days: formatNumber(detail.streak.current) })}
                </span>
              </li>
              <li class="flex items-center justify-between text-xs sm:text-sm">
                <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t('stats.streak.longest_label')}</span>
                <span data-testid="stats-streak-longest" class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                  {$t('stats.streak.days', { days: formatNumber(detail.streak.longest) })}
                </span>
              </li>
            </ul>
          </section>

          <section data-testid="stats-grade" class="card-elevated p-5 rounded-lg space-y-4">
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.grade.heading')}
            </h2>
            <ul class="space-y-3">
              {#each detail.grades as grade}
                <li>
                  <div class="flex items-center justify-between text-xs sm:text-sm">
                    <span class="font-medium text-zinc-700 dark:text-zinc-300">{$t(gradeSourceKey(grade.source))}</span>
                    <span class="font-mono font-semibold text-zinc-950 dark:text-zinc-100">
                      {$t('stats.grade.value', { count: formatNumber(grade.count) })}
                    </span>
                  </div>
                  {#if barWidth(grade.count, gradeMax) > 0}
                    <div class="mt-1.5 h-2 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
                      <div class="h-full rounded-full bg-indigo-600" style="width: {barWidth(grade.count, gradeMax)}%"></div>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          </section>
        </div>

        <!-- 学习曲线 -->
        <section data-testid="stats-curve" class="card-elevated p-5 rounded-lg space-y-4">
          <div>
            <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('stats.curve.heading')}
            </h2>
            <p class="mt-0.5 text-xs text-zinc-400 dark:text-zinc-500">{$t('stats.curve.intro')}</p>
          </div>
          {#if detail.curve.length === 0}
            <p class="text-xs text-zinc-400 dark:text-zinc-500">{$t('stats.curve.empty')}</p>
          {:else}
            <ul class="space-y-3">
              {#each detail.curve as point}
                <li class="rounded-xl border border-zinc-100 bg-zinc-50/50 dark:border-zinc-800 dark:bg-zinc-800/40 p-3 space-y-2">
                  <div class="text-xs font-semibold font-mono text-zinc-700 dark:text-zinc-300">{point.day}</div>
                  <div class="flex items-center gap-3">
                    <span class="w-16 shrink-0 text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.curve.new')}</span>
                    <div class="h-2 flex-1 rounded-full bg-zinc-200/80 dark:bg-zinc-700 overflow-hidden">
                      <div class="h-full rounded-full bg-indigo-600" style="width: {barWidth(point.new, curveMax)}%"></div>
                    </div>
                    <span class="w-12 shrink-0 text-right font-mono text-xs font-semibold text-zinc-900 dark:text-zinc-100">
                      {$t('stats.curve.value', { count: formatNumber(point.new) })}
                    </span>
                  </div>
                  <div class="flex items-center gap-3">
                    <span class="w-16 shrink-0 text-xs font-medium text-zinc-500 dark:text-zinc-400">{$t('stats.curve.review')}</span>
                    <div class="h-2 flex-1 rounded-full bg-zinc-200/80 dark:bg-zinc-700 overflow-hidden">
                      <div class="h-full rounded-full bg-emerald-600" style="width: {barWidth(point.review, curveMax)}%"></div>
                    </div>
                    <span class="w-12 shrink-0 text-right font-mono text-xs font-semibold text-zinc-900 dark:text-zinc-100">
                      {$t('stats.curve.value', { count: formatNumber(point.review) })}
                    </span>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        <!-- 卡组维度 / 标签维度 -->
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <section data-testid="stats-deck" class="card-elevated rounded-lg overflow-hidden">
            <div class="border-b border-zinc-100 dark:border-zinc-800 p-5">
              <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
                {$t('stats.deck.heading')}
              </h2>
            </div>
            {#if detail.decks.length === 0}
              <div class="p-8 text-center text-xs text-zinc-400 dark:text-zinc-500">{$t('stats.deck.empty')}</div>
            {:else}
              <div class="overflow-x-auto">
                <table class="w-full text-left text-sm">
                  <thead class="border-b border-zinc-100 dark:border-zinc-800 bg-zinc-50/70 dark:bg-zinc-800/60 text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
                    <tr>
                      <th class="px-5 py-3">{$t('stats.deck.col.name')}</th>
                      <th class="px-5 py-3">{$t('stats.deck.col.due')}</th>
                      <th class="px-5 py-3">{$t('stats.deck.col.reviews')}</th>
                      <th class="px-5 py-3">{$t('stats.deck.col.retention')}</th>
                      <th class="px-5 py-3">{$t('stats.deck.col.elapsed')}</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
                    {#each detail.decks as deck}
                      <tr>
                        <td class="px-5 py-3 font-medium text-zinc-900 dark:text-zinc-100">{deck.name}</td>
                        <td class="px-5 py-3 font-mono text-zinc-700 dark:text-zinc-300">{$t('stats.due.value', { count: formatNumber(deck.due_count) })}</td>
                        <td class="px-5 py-3 font-mono text-zinc-700 dark:text-zinc-300">{$t('stats.volume.value', { count: formatNumber(deck.reviews) })}</td>
                        <td class="px-5 py-3 font-mono text-zinc-700 dark:text-zinc-300">{$t('stats.table.rate', { rate: formatPercentValue(deck.retention) })}</td>
                        <td class="px-5 py-3 font-mono text-zinc-700 dark:text-zinc-300">{formatDuration(deck.elapsed_ms)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </section>

          <section data-testid="stats-tag" class="card-elevated rounded-lg overflow-hidden">
            <div class="border-b border-zinc-100 dark:border-zinc-800 p-5">
              <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
                {$t('stats.tag.heading')}
              </h2>
              <!-- 口径写在标题下：本维度只数「已复习卡片上的标签」，不写清楚时它会看起来像
                   在重复卡组维度（没复习过的大标签一个都不出现）。 -->
              <p data-testid="stats-tag-scope" class="mt-2 text-xs leading-relaxed text-zinc-400 dark:text-zinc-500">{$t('stats.tag.scope')}</p>
            </div>
            {#if detail.tags.length === 0}
              <div class="p-8 text-center text-xs text-zinc-400 dark:text-zinc-500">{$t('stats.tag.empty')}</div>
            {:else}
              <div class="overflow-x-auto">
                <table class="w-full text-left text-sm">
                  <thead class="border-b border-zinc-100 dark:border-zinc-800 bg-zinc-50/70 dark:bg-zinc-800/60 text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
                    <tr>
                      <th class="px-5 py-3">{$t('stats.tag.col.tag')}</th>
                      <th class="px-5 py-3">{$t('stats.tag.col.reviews')}</th>
                      <th class="px-5 py-3">{$t('stats.tag.col.retention')}</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
                    {#each detail.tags as tag}
                      <tr>
                        <td class="px-5 py-3 font-medium text-zinc-900 dark:text-zinc-100">{tag.tag}</td>
                        <td class="px-5 py-3 font-mono text-zinc-700 dark:text-zinc-300">{$t('stats.volume.value', { count: formatNumber(tag.reviews) })}</td>
                        <td class="px-5 py-3 font-mono text-zinc-700 dark:text-zinc-300">{$t('stats.table.rate', { rate: formatPercentValue(tag.retention) })}</td>
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
  </div>
</div>

<script lang="ts">
  import { BarController, BarElement, CategoryScale, Chart, Legend, LinearScale, Tooltip } from 'chart.js';
  import { t, localeStore } from '../i18n';
  import { formatNumber } from '../format';
  import type { StatsCurvePoint } from '../api';

  /**
   * Learning curve: daily review volume in the window, stacked into bars by "reviews / new" (Chart.js, registering only the modules the bar chart uses).
   *
   * The server returns only days with reviews, so here they are padded into a continuous date axis over from..to (missing days as 0),
   * otherwise blank days would be squeezed out and the distance between bars would no longer represent time. Colors come from --chart-1 / --chart-2 in app.css
   * (already checked for color-vision deficiency); the legend is always shown, tooltips name the series, and a data table is provided for screen readers.
   */
  Chart.register(BarController, BarElement, CategoryScale, LinearScale, Legend, Tooltip);

  interface Props {
    points: StatsCurvePoint[];
    from: string;
    to: string;
  }

  let { points, from, to }: Props = $props();
  let canvas = $state<HTMLCanvasElement | null>(null);

  const DAY = 86_400_000;
  const toMs = (day: string) => Date.parse(`${day}T00:00:00Z`);

  // Continuous date axis: one slot per day from from to to; when a bound is missing it falls back to the data's own first and last day.
  const days = $derived.by(() => {
    const byDay = new Map(points.map((p) => [p.day, p]));
    const start = from || points[0]?.day;
    const end = to || points[points.length - 1]?.day;
    if (!start || !end) return [] as StatsCurvePoint[];
    const out: StatsCurvePoint[] = [];
    for (let ms = toMs(start); ms <= toMs(end); ms += DAY) {
      const day = new Date(ms).toISOString().slice(0, 10);
      out.push(byDay.get(day) ?? { day, new: 0, review: 0 });
    }
    return out;
  });

  const token = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

  function render(): Chart {
    const muted = token('--muted-foreground');
    const bar = { borderSkipped: 'start' as const, maxBarThickness: 24, categoryPercentage: 0.7 };
    return new Chart(canvas!, {
      type: 'bar',
      data: {
        labels: days.map((d) => d.day.slice(5)),
        datasets: [
          { ...bar, label: $t('stats.curve.review'), data: days.map((d) => d.review), backgroundColor: token('--chart-1') },
          // The top segment has 4px rounded corners and the bottom one is square: the bar grows out of the baseline with only its top rounded.
          { ...bar, label: $t('stats.curve.new'), data: days.map((d) => d.new), backgroundColor: token('--chart-2'), borderRadius: { topLeft: 4, topRight: 4 } },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: { stacked: true, grid: { display: false }, border: { display: false }, ticks: { color: muted, maxTicksLimit: 8, maxRotation: 0 } },
          y: { stacked: true, beginAtZero: true, grid: { color: token('--border') }, border: { display: false }, ticks: { color: muted, precision: 0, maxTicksLimit: 4 } },
        },
        plugins: {
          legend: { position: 'top', align: 'end', labels: { color: token('--foreground'), boxWidth: 10, boxHeight: 10, useBorderRadius: true, borderRadius: 2 } },
          tooltip: {
            backgroundColor: token('--popover'),
            borderColor: token('--border'),
            borderWidth: 1,
            titleColor: token('--foreground'),
            bodyColor: token('--foreground'),
            footerColor: muted,
            callbacks: {
              title: (items) => days[items[0]?.dataIndex ?? 0]?.day ?? '',
              footer: (items) => $t('stats.curve.total', { count: formatNumber(items.reduce((sum, i) => sum + Number(i.raw), 0), $localeStore) }),
            },
          },
        },
      },
    });
  }

  // render() reads days and $t: when the data or UI language changes the effect re-runs, destroying the old chart and building a new one.
  $effect(() => {
    if (!canvas) return;
    let chart = render();
    // Switching light/dark changes the color tokens and the canvas does not follow automatically, so it is rebuilt once whenever the html class changes.
    const observer = new MutationObserver(() => {
      chart.destroy();
      chart = render();
    });
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => {
      observer.disconnect();
      chart.destroy();
    };
  });
</script>

<div class="space-y-2" data-testid="stats-curve-chart">
  <p class="text-xs text-muted-foreground" data-testid="stats-curve-summary">
    {$t('stats.curve.summary', {
      new: formatNumber(points.reduce((sum, p) => sum + p.new, 0), $localeStore),
      review: formatNumber(points.reduce((sum, p) => sum + p.review, 0), $localeStore),
    })}
  </p>
  <div class="relative h-56">
    <!-- The canvas is hidden from screen readers: the table below carries the same data. -->
    <canvas bind:this={canvas} aria-hidden="true"></canvas>
  </div>
  <!-- The same data as a table, for screen readers and for readers who cannot tell the colours apart. -->
  <table class="sr-only" data-testid="stats-curve-table">
    <caption>{$t('stats.curve.table')}</caption>
    <thead>
      <tr><th scope="col">{$t('stats.curve.col_day')}</th><th scope="col">{$t('stats.curve.review')}</th><th scope="col">{$t('stats.curve.new')}</th></tr>
    </thead>
    <tbody>
      {#each points as p (p.day)}
        <tr><th scope="row">{p.day}</th><td>{p.review}</td><td>{p.new}</td></tr>
      {/each}
    </tbody>
  </table>
</div>

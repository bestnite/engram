<script lang="ts">
  import { BarController, BarElement, CategoryScale, Chart, Legend, LinearScale, Tooltip } from 'chart.js';
  import { t, localeStore } from '../i18n';
  import type { StatsCurvePoint } from '../api';

  /**
   * 学习曲线：窗口内每天的复习量，按「复习 / 新学」堆叠成柱（Chart.js，只注册柱状图用到的模块）。
   *
   * 服务端只返回有复习的日子，这里按 from..to 补成连续的日期轴（缺的日子为 0），
   * 否则空白的日子会被挤掉，柱间距离不再代表时间。颜色取 app.css 的 --chart-1 / --chart-2
   * （已做色觉缺陷校验）；图例常驻、提示写明系列名，另附一份读屏用的数据表。
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

  // 连续日期轴：from 到 to 每天一格；边界缺失时退回到数据自身的首尾。
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

  const fmt = (n: number) => new Intl.NumberFormat($localeStore).format(n);
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
          // 顶段带 4px 圆角，底段是直角：整根柱子从基线长出、只有顶端是圆的。
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
              footer: (items) => $t('stats.curve.total', { count: fmt(items.reduce((sum, i) => sum + Number(i.raw), 0)) }),
            },
          },
        },
      },
    });
  }

  // render() 读取 days 与 $t：数据或界面语言变化时 effect 重跑，旧图销毁、新图生成。
  $effect(() => {
    if (!canvas) return;
    let chart = render();
    // 切换深浅主题时颜色令牌变了，canvas 不会自动跟随，所以按 html 的 class 变化重建一次。
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
      new: fmt(points.reduce((sum, p) => sum + p.new, 0)),
      review: fmt(points.reduce((sum, p) => sum + p.review, 0)),
    })}
  </p>
  <div class="relative h-56">
    <!-- 画布对读屏隐藏：同样的数据由下面的表格给出。 -->
    <canvas bind:this={canvas} aria-hidden="true"></canvas>
  </div>
  <!-- 读屏与无法分辨颜色的读者：同一份数据的表格形式。 -->
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

import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { render } from 'svelte/server';
import HomeView from '../lib/views/HomeView.svelte';
import StatsView from '../lib/views/StatsView.svelte';
import { ApiClientError } from '../lib/api';
import type { StatsSummary, StatsDetail, Deck } from '../lib/api';
import { setLocale } from '../lib/i18n';

describe('NoteEditView sanitized preview sink', () => {
  it('binds HTML sinks only to preview API response properties, never editor fields', () => {
    const source = readFileSync(new URL('../lib/views/NoteEditView.svelte', import.meta.url), 'utf8');
    expect(source).toContain('{@html card.front_html}');
    expect(source).toContain('{@html card.back_html}');
    expect(source).toContain('const response = await apiClient.previewNote(deckId, note.kind, fields);');
    expect(source).toContain('previewCards = response.cards;');
    expect(source).not.toContain('{@html fields');
    expect(source).not.toContain('{@html fieldsText');
  });
});

describe('HomeView component response states and truthful rendering', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders loading state with spinner and localized text', () => {
    const { html } = render(HomeView, {
      props: { initialLoading: true },
    });

    expect(html).toContain('data-testid="home-loading"');
    expect(html).toContain('正在加载概览...');
    expect(html).not.toContain('data-testid="home-data"');
    expect(html).not.toContain('data-testid="home-summary"');
  });

  it('renders unauthorized error state (401) with retry button', () => {
    const err = new ApiClientError('HTTP 401', {
      status: 401,
      code: 'unauthorized',
    });
    const { html } = render(HomeView, {
      props: { initialLoading: false, initialError: err },
    });

    expect(html).toContain('data-testid="home-unauthorized"');
    expect(html).toContain('请先登录以查看概览');
    expect(html).toContain('data-testid="home-retry"');
    expect(html).not.toContain('data-testid="home-data"');
  });

  it('renders generic error state with retry button', () => {
    const err = new Error('Network failure');
    const { html } = render(HomeView, {
      props: { initialLoading: false, initialError: err },
    });

    expect(html).toContain('data-testid="home-failed"');
    expect(html).toContain('加载概览失败');
    expect(html).toContain('data-testid="home-retry"');
  });

  it('renders empty decks state truthfully when user has no decks', () => {
    const summary: StatsSummary = {
      decks: 0,
      notes: 0,
      cards: 0,
      due: 0,
      reviews_today: 0,
      reviews_total: 0,
      retention: 0,
    };
    const { html } = render(HomeView, {
      props: {
        initialLoading: false,
        initialSummary: summary,
        initialDecks: [],
      },
    });

    expect(html).toContain('data-testid="home-data"');
    expect(html).toContain('data-testid="home-empty-decks"');
    expect(html).toContain('暂无卡组');
    expect(html).toContain('当前没有到期卡片');
    expect(html).not.toContain('data-testid="home-start-review"');
  });

  it('renders truthful metrics from Go test fixture (visible_scope_test.go)', () => {
    // 真实响应来自 internal/api/visible_scope_test.go:TestStatsVisibleContentButFullReviewHistory
    const goSummary: StatsSummary = {
      decks: 1,
      notes: 1,
      cards: 1,
      due: 1,
      reviews_today: 2,
      reviews_total: 2,
      retention: 0.5,
    };
    const goDeck: Deck = {
      id: 10,
      name: '测试卡组 <安全测试>',
      description: '卡组描述 & 详情',
      visibility: 'private',
      new_per_day: 20,
      reviews_per_day: 100,
      preset_id: 1,
      created_at: '2026-10-06T00:00:00Z',
    };

    const { html } = render(HomeView, {
      props: {
        initialLoading: false,
        initialSummary: goSummary,
        initialDecks: [goDeck],
      },
    });

    expect(html).toContain('data-testid="home-data"');

    // 验证到期数、今日复习数、留存率、卡组数精准渲染
    expect(html).toContain('data-testid="home-due-count"');
    expect(html).toMatch(/data-testid="home-due-count"[^>]*>\s*1\s*</);
    expect(html).toContain('data-testid="home-reviews-today"');
    expect(html).toMatch(/data-testid="home-reviews-today"[^>]*>\s*2\s*</);
    expect(html).toContain('data-testid="home-retention"');
    expect(html).toMatch(/data-testid="home-retention"[^>]*>\s*50\.0%\s*</);
    expect(html).toContain('data-testid="home-decks-count"');
    expect(html).toMatch(/data-testid="home-decks-count"[^>]*>\s*1\s*</);

    // 验证到期卡片大于 0 时，显示「开始复习」链接并携带准确计数
    expect(html).toContain('data-testid="home-start-review"');
    expect(html).toContain('href="/review"');
    expect(html).toContain('开始复习 (1)');

    // 验证卡组列表与 XSS 安全转义
    expect(html).toContain('data-testid="home-decks-list"');
    expect(html).toContain('href="/review?deck=10"');
    expect(html).toContain('20 / 100');
    expect(html).toContain('&lt;安全测试>');
    expect(html).toContain('卡组描述 &amp; 详情');
    expect(html).not.toContain('<安全测试>');
  });
});

describe('StatsView component response states and truthful rendering', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders loading state with spinner and localized text', () => {
    const { html } = render(StatsView, {
      props: { initialLoading: true },
    });

    expect(html).toContain('data-testid="stats-loading"');
    expect(html).toContain('正在加载统计数据...');
    expect(html).not.toContain('data-testid="stats-data"');
  });

  it('renders unauthorized error state (401) with retry button', () => {
    const err = new ApiClientError('HTTP 401', {
      status: 401,
      code: 'unauthorized',
    });
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialError: err },
    });

    expect(html).toContain('data-testid="stats-unauthorized"');
    expect(html).toContain('请先登录以查看统计');
    expect(html).toContain('data-testid="stats-retry"');
  });

  it('renders generic error state with retry button', () => {
    const err = new Error('Database error');
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialError: err },
    });

    expect(html).toContain('data-testid="stats-failed"');
    expect(html).toContain('加载统计失败');
    expect(html).toContain('data-testid="stats-retry"');
  });

  // 与 internal/web/stats_test.go 的 seedStatsFixture 同源：2 条今日复习 + 1 条昨日复习、
  // 一个稳定性 5 天的到期卡、一个带 algebra 标签的卡组。数字全部可在 Go 侧独立复算。
  const detailFixture: StatsDetail = {
    generated_at: '2026-10-06T12:00:00Z',
    empty: false,
    volume: { today: 2, last_7_days: 3, last_30_days: 3 },
    due: { today: 1, tomorrow: 0, within_7_days: 0, within_30_days: 0, later: 0, new_not_due: 0 },
    retention: {
      total: 2,
      passed: 2,
      rate: 1,
      buckets: [{ label: '1-7d', total: 2, passed: 2, rate: 1 }],
    },
    time_spent: { total_ms: 3000, count: 2, avg_ms: 1500, median_ms: 1500 },
    streak: { current: 2, longest: 2 },
    curve: [
      { day: '2026-10-05', new: 1, review: 0 },
      { day: '2026-10-06', new: 0, review: 2 },
    ],
    decks: [
      { deck_id: 1, name: 'Stats deck', due_count: 1, reviews: 3, retention: 2 / 3, elapsed_ms: 3000 },
    ],
    tags: [{ tag: 'algebra', reviews: 3, retention: 2 / 3 }],
    grades: [
      { source: 'self', count: 2 },
      { source: 'typed', count: 1 },
    ],
  };

  it('renders truthful empty state when the detail snapshot reports no data', () => {
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: { ...detailFixture, empty: true } },
    });

    expect(html).toContain('data-testid="stats-empty"');
    expect(html).toContain('暂无学习与复习统计数据');
    expect(html).not.toContain('data-testid="stats-data"');
  });

  it('renders exact detailed figures from the Go stats fixture', () => {
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: detailFixture },
    });

    expect(html).toContain('data-testid="stats-data"');

    // 复习量 / 到期预测：原始计数经语言包单位渲染，不四舍五入、不补零。
    expect(html).toContain('2 次');
    expect(html).toContain('1 张');

    // 留存率：总体与 1–7 天桶都是 2/2 = 100.0%。
    expect(html).toContain('100.0%（2/2）');
    expect(html).toContain('1–7 天');

    // 时间投入：累计 3000ms → 3 秒；中位数 1500ms → 1.5 秒。
    expect(html).toContain('3 秒');
    expect(html).toContain('1.5 秒');

    // 连续打卡。
    expect(html).toMatch(/data-testid="stats-streak-current"[^>]*>\s*2 天\s*</);

    // 卡组维度与标签维度。
    expect(html).toContain('Stats deck');
    expect(html).toContain('66.7%');
    expect(html).toContain('algebra');

    // 判分来源分布。
    expect(html).toContain('自评');
    expect(html).toContain('机器判分');

    // 学习曲线日期。
    expect(html).toContain('2026-10-06');
  });

  it('renders zero retention as 0.0% (0/0) without fabricating a rate', () => {
    const noRetention: StatsDetail = {
      ...detailFixture,
      retention: { total: 0, passed: 0, rate: 0, buckets: [] },
    };

    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: noRetention },
    });

    expect(html).toContain('data-testid="stats-data"');
    expect(html).toContain('0.0%（0/0）');
  });

  it('localizes detail labels into English when the locale is en', () => {
    setLocale('en');
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: detailFixture },
    });

    expect(html).toContain('Review volume');
    expect(html).toContain('Due forecast');
    expect(html).toContain('Grading source');
    expect(html).not.toContain('复习量');
  });
});

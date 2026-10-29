import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import HomeView from '../lib/views/HomeView.svelte';
import StatsView from '../lib/views/StatsView.svelte';
import { ApiClientError } from '../lib/api';
import type { StatsSummary, StatsDetail, Deck } from '../lib/api';
import { setLocale } from '../lib/i18n';

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
      retention_total: 0,
      retention_passed: 0,
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
    // The real response comes from internal/api/visible_scope_test.go:TestStatsVisibleContentButFullReviewHistory
    // Two of the three reviews are due reviews (one Again), and the new-card one does not count toward retention: 1/2 = 50%.
    const goSummary: StatsSummary = {
      decks: 1,
      notes: 1,
      cards: 1,
      due: 1,
      reviews_today: 3,
      reviews_total: 3,
      retention: 0.5,
      retention_total: 2,
      retention_passed: 1,
    };
    const goDeck: Deck = {
      id: '10',
      name: '测试卡组 <安全测试>',
      description: '卡组描述 & 详情',
      new_per_day: 20,
      reviews_per_day: 100,
      preset_id: '1',
      created_at: '2026-10-06T00:00:00Z',
      role: 'owner',
    };

    const { html } = render(HomeView, {
      props: {
        initialLoading: false,
        initialSummary: goSummary,
        initialDecks: [goDeck],
      },
    });

    expect(html).toContain('data-testid="home-data"');

    // Verify due count, reviews today, retention rate and deck count all render exactly
    expect(html).toContain('data-testid="home-due-count"');
    expect(html).toMatch(/data-testid="home-due-count"[^>]*>\s*1\s*</);
    expect(html).toContain('data-testid="home-reviews-today"');
    expect(html).toMatch(/data-testid="home-reviews-today"[^>]*>\s*3\s*</);
    expect(html).toContain('data-testid="home-retention"');
    expect(html).toMatch(/data-testid="home-retention"[^>]*>\s*50\.0%\s*</);
    expect(html).toContain('data-testid="home-decks-count"');
    expect(html).toMatch(/data-testid="home-decks-count"[^>]*>\s*1\s*</);

    // Verify that with more than 0 due cards the "start review" link shows and carries the exact count
    expect(html).toContain('data-testid="home-start-review"');
    expect(html).toContain('href="/review"');
    // The count sits in its own numeric badge inside the button.
    expect(html).toMatch(/data-testid="home-start-review"[^>]*>[\s\S]*?开始复习[\s\S]*?<span[^>]*>\s*1\s*<\/span>/);

    // Verify the deck list and XSS-safe escaping
    expect(html).toContain('data-testid="home-decks-list"');
    expect(html).toContain('href="/review?deck=10"');
    // The home page's deck rows show today's queue count, not the daily cap; the cap only shows in the /decks list.
    expect(html).not.toContain('20 / 100');
    expect(html).toContain('&lt;安全测试>');
    expect(html).toContain('卡组描述 &amp; 详情');
    expect(html).not.toContain('<安全测试>');
  });

  it('shows no-data retention when the user has only learning-step reviews', () => {
    // Same shape as internal/api/stats_summary_test.go's "learning steps only" case: reviews exist, but no due reviews.
    const learningOnly: StatsSummary = {
      decks: 1,
      notes: 1,
      cards: 1,
      due: 0,
      reviews_today: 3,
      reviews_total: 3,
      retention: 0,
      retention_total: 0,
      retention_passed: 0,
    };
    const { html } = render(HomeView, {
      props: { initialLoading: false, initialSummary: learningOnly, initialDecks: [] },
    });

    expect(html).toMatch(/data-testid="home-retention"[^>]*>\s*暂无数据\s*</);
    expect(html).not.toMatch(/data-testid="home-retention"[^>]*>\s*0\.0%/);
  });

  it('puts a help button with a localized label next to each explained metric', () => {
    const summary: StatsSummary = {
      decks: 1,
      notes: 1,
      cards: 1,
      due: 0,
      reviews_today: 0,
      reviews_total: 0,
      retention: 0,
      retention_total: 0,
      retention_passed: 0,
    };
    const { html } = render(HomeView, {
      props: { initialLoading: false, initialSummary: summary, initialDecks: [] },
    });

    expect(html).toMatch(/data-testid="home-due-help"[^>]*aria-label="待复习是什么意思？"|aria-label="待复习是什么意思？"[^>]*data-testid="home-due-help"/);
    expect(html).toContain('aria-label="今日已复习是什么意思？"');
    expect(html).toContain('aria-label="留存率是什么意思？"');
    // The deck count speaks for itself, so no caption.
    expect(html).not.toContain('aria-label="卡组数是什么意思？"');
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

  // Same source as internal/web/stats_test.go's seedStatsFixture: 2 reviews today + 1 yesterday,
  // one due card with 5 days of stability, and one deck tagged algebra. Every number can be recomputed independently on the Go side.
  const detailFixture: StatsDetail = {
    generated_at: '2026-10-06T12:00:00Z',
    empty: false,
    volume: { today: 2, last_7_days: 3, last_30_days: 3 },
    due: { today: 1, tomorrow: 0, within_7_days: 0, within_30_days: 0, later: 0, new: 0 },
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
    curve_from: '2026-09-07',
    curve_to: '2026-10-06',
    // Reviews for the deck and tag dimensions all total 3, and retention counts only the 2 due reviews among them (both non-Again).
    decks: [
      { deck_id: '1', name: 'Stats deck', review: 1, new: 0, reviews: 3, retention: 1, retention_total: 2, elapsed_ms: 3000 },
    ],
    tags: [{ tag: 'algebra', reviews: 3, retention: 1, retention_total: 2 }],
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

    // Review volume / due forecast: raw counts rendered with language-pack units, without rounding or zero padding.
    expect(html).toContain('2 次');
    expect(html).toContain('1 张');

    // Retention: the overall figure and the 1–7 day bucket are both 2/2 = 100.0%.
    expect(html).toContain('100.0%（2/2）');
    expect(html).toContain('1–7 天');

    // Time invested: 3000ms total → 3 seconds; 1500ms median → 1.5 seconds.
    expect(html).toContain('3 秒');
    expect(html).toContain('1.5 秒');

    // Streak.
    expect(html).toMatch(/data-testid="stats-streak-current"[^>]*>\s*2 天\s*</);

    // Deck dimension and tag dimension.
    expect(html).toContain('Stats deck');
    expect(html).toMatch(/Stats deck[\s\S]*?100\.0%<\/td>/);
    expect(html).not.toContain('66.7%');
    expect(html).toContain('algebra');
    // The tag dimension's definition must be written on the page: it only counts tags on
    // already-reviewed cards, and without saying so it looks like a duplicate of the deck dimension (a heavily used tag with no reviews never appears, and users conclude the stats only know deck names).
    expect(html).toContain('data-testid="stats-tag-scope"');
    expect(html).toContain('只列出近 30 天内复习过的卡片上的标签');

    // Average time invested is total / timed count, i.e. per attempt rather than per day.
    expect(html).toContain('平均每次');
    expect(html).not.toContain('日均');

    // Grading-source distribution.
    expect(html).toContain('自评');
    expect(html).toContain('自动判分');

    // Learning curve: the chart container, the window totals, and the data table for screen readers (dates written into the table).
    expect(html).toContain('data-testid="stats-curve-chart"');
    expect(html).toContain('近 30 天新学 1 · 复习 2');
    expect(html).toMatch(/data-testid="stats-curve-table"[\s\S]*2026-10-06/);
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

  it('shows no-data retention for deck and tag rows without due reviews', () => {
    const learningOnly: StatsDetail = {
      ...detailFixture,
      decks: [{ deck_id: '1', name: 'Fresh deck', review: 5, new: 0, reviews: 4, retention: 0, retention_total: 0, elapsed_ms: 0 }],
      tags: [{ tag: 'fresh', reviews: 4, retention: 0, retention_total: 0 }],
    };
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: learningOnly },
    });

    expect(html).toMatch(/Fresh deck[\s\S]*?暂无数据<\/td>/);
    expect(html).toMatch(/fresh<\/td>[\s\S]*?暂无数据<\/td>/);
    expect(html).not.toMatch(/0\.0%<\/td>/);
  });

  it('explains every non-obvious metric with a help button', () => {
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: detailFixture },
    });

    for (const id of [
      'stats-volume-help',
      'stats-streak-help',
      'stats-curve-help',
      'stats-due-help',
      'stats-retention-help',
      'stats-retention-buckets-help',
      'stats-time-help',
      'stats-grade-help',
      'stats-deck-review-help',
      'stats-deck-new-help',
      'stats-deck-reviews-help',
      'stats-deck-retention-help',
      'stats-deck-elapsed-help',
      'stats-tag-help',
      'stats-tag-retention-help',
    ]) {
      expect(html).toContain(`data-testid="${id}"`);
    }
    expect(html).toContain('aria-label="记忆强度是什么意思？"');
    expect(html).toContain('aria-label="今日复习是什么意思？"');
  });

  it('localizes detail labels into English when the locale is en', () => {
    setLocale('en');
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialDetail: detailFixture },
    });

    expect(html).toContain('Review volume');
    expect(html).toContain('Due forecast');
    expect(html).toContain('Grading method');
    expect(html).toContain('Only tags on cards reviewed in the last 30 days');
    expect(html).toContain('Average per review');
    expect(html).toContain('aria-label="What does “Retention” mean?"');
    expect(html).not.toContain('复习量');
  });
});

import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import HomeView from '../lib/views/HomeView.svelte';
import StatsView from '../lib/views/StatsView.svelte';
import { ApiClientError } from '../lib/api';
import type { StatsSummary, Deck } from '../lib/api';
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

  it('renders truthful empty state when user has zero activity', () => {
    const emptySummary: StatsSummary = {
      decks: 0,
      notes: 0,
      cards: 0,
      due: 0,
      reviews_today: 0,
      reviews_total: 0,
      retention: 0,
    };
    const { html } = render(StatsView, {
      props: { initialLoading: false, initialSummary: emptySummary },
    });

    expect(html).toContain('data-testid="stats-empty"');
    expect(html).toContain('暂无学习与复习统计数据');
    expect(html).not.toContain('data-testid="stats-data"');
  });

  it('renders exact summary figures from Go test fixture (visible_scope_test.go)', () => {
    const goSummary: StatsSummary = {
      decks: 1,
      notes: 1,
      cards: 1,
      due: 1,
      reviews_today: 2,
      reviews_total: 2,
      retention: 0.5,
    };

    const { html } = render(StatsView, {
      props: { initialLoading: false, initialSummary: goSummary },
    });

    expect(html).toContain('data-testid="stats-data"');
    expect(html).toMatch(/data-testid="stats-metric-due"[^>]*>\s*1\s*</);
    expect(html).toMatch(/data-testid="stats-metric-today"[^>]*>\s*2\s*</);
    expect(html).toMatch(/data-testid="stats-metric-total"[^>]*>\s*2\s*</);
    expect(html).toMatch(/data-testid="stats-metric-retention"[^>]*>\s*50\.0%\s*</);
    expect(html).toMatch(/data-testid="stats-metric-decks"[^>]*>\s*1\s*</);
    expect(html).toMatch(/data-testid="stats-metric-notes"[^>]*>\s*1\s*</);
    expect(html).toMatch(/data-testid="stats-metric-cards"[^>]*>\s*1\s*</);

    // 留存率进度条
    expect(html).toContain('style="width: 50%"');
  });

  it('renders retention rate as N/A without fabricating numbers when total reviews is zero', () => {
    const noReviewsSummary: StatsSummary = {
      decks: 3,
      notes: 15,
      cards: 30,
      due: 8,
      reviews_today: 0,
      reviews_total: 0,
      retention: 0,
    };

    const { html } = render(StatsView, {
      props: { initialLoading: false, initialSummary: noReviewsSummary },
    });

    expect(html).toContain('data-testid="stats-data"');
    expect(html).toMatch(/data-testid="stats-metric-retention"[^>]*>\s*暂无数据\s*</);
    expect(html).toMatch(/data-testid="stats-metric-due"[^>]*>\s*8\s*</);
    expect(html).toMatch(/data-testid="stats-metric-decks"[^>]*>\s*3\s*</);
    expect(html).toMatch(/data-testid="stats-metric-notes"[^>]*>\s*15\s*</);
    expect(html).toMatch(/data-testid="stats-metric-cards"[^>]*>\s*30\s*</);
  });
});

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import PresetsView from '../lib/views/PresetsView.svelte';
import { ApiClient, ApiClientError } from '../lib/api';
import type { PresetsResponse, PresetRecord } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

function preset(overrides: Partial<PresetRecord> = {}): PresetRecord {
  return {
    id: '7',
    name: 'Default',
    is_default: true,
    desired_retention: 0.9,
    learning_steps: '1m,10m',
    relearning_steps: '10m',
    maximum_interval_days: 36500,
    enable_fuzz: true,
    weights_optimized: false,
    weights_optimized_at: null,
    weights_review_count: null,
    weights_raw: null,
    job: null,
    gate: { reviews: 0, min: 500, shortfall: 500, eligible: false },
    ...overrides,
  };
}

const payload: PresetsResponse = {
  presets: [preset()],
};

describe('PresetsView renders presets, weights and the optimise gate truthfully', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the list with preset metrics, weight source and gate counts', () => {
    const { html } = render(PresetsView, {
      props: { initialLoading: false, initialData: payload },
    });
    expect(html).toContain('data-testid="preset-7-name"');
    // 默认预设的库内名是 "Default"（机器标识），界面上必须是语言包文案。
    expect(html).toMatch(/>\s*默认\s*<\/h2>/);
    expect(html).not.toMatch(/>\s*Default\s*<\/h2>/);
    expect(html).toContain('data-testid="preset-7-retention"');
    expect(html).toContain('data-testid="preset-7-max-interval"');
    expect(html).toContain('data-testid="preset-7-optimize"');
    expect(html).toContain('data-testid="preset-7-revert"');
    expect(html).toContain('data-testid="preset-7-edit"');
    // 未优化显示默认权重，且不显示权重明细。
    expect(html).toContain('默认权重');
    expect(html).not.toContain('data-testid="preset-7-weights-raw"');
    // 门槛：可用 0 条、门槛 500 条，并给出「还差 500 条」。
    expect(html).toContain('可用于优化的复习记录：0 条');
    expect(html).toContain('data-testid="preset-7-shortfall"');
    expect(html).toContain('还差 500 条');
  });

  it('labels the default preset from the catalog and offers no delete button for it', () => {
    // 默认预设不可删除：判据是服务端的 is_default，不是前端比较库内名。
    const { html } = render(PresetsView, {
      props: { initialLoading: false, initialData: payload },
    });
    expect(html).toMatch(/>\s*默认\s*<\/h2>/);
    expect(html).not.toContain('data-testid="preset-7-delete"');
  });

  it('keeps the user-chosen name and the delete button for a user-created preset', () => {
    const data: PresetsResponse = {
      presets: [preset({ id: '8', name: 'Evening', is_default: false })],
    };
    const { html } = render(PresetsView, { props: { initialLoading: false, initialData: data } });
    expect(html).toMatch(/>\s*Evening\s*<\/h2>/);
    expect(html).toContain('data-testid="preset-8-delete"');
  });

  it('shows the optimised weight source, timestamp, count and raw detail', () => {
    const optimized = payload.presets[0]!;
    const data: PresetsResponse = {
      presets: [
        {
          ...optimized,
          weights_optimized: true,
          weights_optimized_at: '2026-10-02T12:00:00Z',
          weights_review_count: 600,
          weights_raw: '[0.5,1.5]',
          gate: { reviews: 600, min: 500, shortfall: 0, eligible: true },
        },
      ],
    };
    const { html } = render(PresetsView, { props: { initialLoading: false, initialData: data } });
    expect(html).toContain('已优化');
    expect(html).toContain('data-testid="preset-7-weights-raw"');
    expect(html).toContain('[0.5,1.5]');
    expect(html).toContain('600 条');
    // 门槛达标时不再渲染「还差」段落。
    expect(html).not.toContain('data-testid="preset-7-shortfall"');
  });

  it('renders a succeeded job fit comparison and its verdict', () => {
    const withJob: PresetsResponse = {
      presets: [
        preset({
          weights_optimized: true,
          weights_raw: '[0.5]',
          job: {
            id: '12',
            status: 'succeeded',
            stage: null,
            log_tail: 'training done',
            error: null,
            result: {
              reviews_used: 600,
              weights: [0.5],
              fit_before: { log_loss: 0.51, rmse: 0.42, items: 600 },
              fit_after: { log_loss: 0.44, rmse: 0.38, items: 600 },
              optimized_at: '2026-10-02T12:00:00Z',
              verdict: 'improved',
            },
          },
          gate: { reviews: 600, min: 500, shortfall: 0, eligible: true },
        }),
      ],
    };
    const { html } = render(PresetsView, { props: { initialLoading: false, initialData: withJob } });
    expect(html).toContain('data-testid="preset-7-job"');
    expect(html).toContain('data-testid="preset-7-status"');
    expect(html).toContain('优化完成');
    expect(html).toContain('data-testid="preset-7-result"');
    expect(html).toContain('0.4400');
    expect(html).toContain('0.5100');
    expect(html).toContain('拟合改善');
    expect(html).toContain('data-testid="preset-7-log"');
  });

  it('hides the fit verdict on a small sample', () => {
    const small: PresetsResponse = {
      presets: [
        preset({
          job: {
            id: '13',
            status: 'succeeded',
            stage: null,
            log_tail: null,
            error: null,
            result: {
              reviews_used: 320,
              weights: [0.5],
              fit_before: { log_loss: 0.51, rmse: 0.42, items: 10 },
              fit_after: { log_loss: 0.51, rmse: 0.42, items: 10 },
              optimized_at: null,
              verdict: 'insufficient_sample',
            },
          },
          gate: { reviews: 320, min: 300, shortfall: 0, eligible: true },
        }),
      ],
    };
    const { html } = render(PresetsView, { props: { initialLoading: false, initialData: small } });
    expect(html).toContain('data-testid="preset-7-verdict"');
    expect(html).toContain('样本不足，无法判定');
    // 样本不足不渲染 before/after 两个数。
    expect(html).not.toContain('data-testid="preset-7-fit-before"');
  });

  it('renders the loading state before any data arrives', () => {
    const { html } = render(PresetsView, { props: { initialLoading: true } });
    expect(html).toContain('data-testid="presets-loading"');
    expect(html).not.toContain('data-testid="presets-list"');
  });

  it('renders an error state when loading fails', () => {
    const err = new ApiClientError('HTTP 500', { status: 500, code: 'internal_error' });
    const { html } = render(PresetsView, {
      props: { initialLoading: false, initialError: err },
    });
    expect(html).toContain('data-testid="presets-failed"');
    expect(html).toContain('data-testid="presets-retry"');
  });
});

describe('presets API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs the session-only presets endpoint', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(payload), { status: 200 }));
    await expect(client.listPresets()).resolves.toEqual(payload);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/presets');
    expect(mockFetch.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' });
  });

  it('POSTs a create with the session CSRF token and keeps enable_fuzz false verbatim', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'preset-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(payload), { status: 200 }));
    await client.createPreset({
      name: 'Evening',
      desired_retention: 0.85,
      learning_steps: '2m,20m',
      relearning_steps: '15m',
      maximum_interval_days: 1000,
      enable_fuzz: false,
    });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/presets');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('preset-csrf');
    expect(JSON.parse(String(init?.body)).enable_fuzz).toBe(false);
  });

  it('triggers optimisation and surfaces the shortfall gate on a 400', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'preset-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: { code: 'insufficient_reviews', message: 'not enough' },
          gate: { reviews: 0, min: 500, shortfall: 500, eligible: false },
        }),
        { status: 400 }
      )
    );
    await expect(client.optimizePreset('7')).rejects.toMatchObject({ code: 'insufficient_reviews' });
    expect(mockFetch.mock.calls[1]?.[0]).toBe('/api/v1/presets/7/optimize');
    expect(mockFetch.mock.calls[1]?.[1]?.method).toBe('POST');
  });

  it('polls the status endpoint with the job query parameter', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ job: null, gate: payload.presets[0]!.gate }), { status: 200 })
    );
    await client.getPresetOptimizeStatus('7', '12');
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/presets/7/optimize/status?job=12');
  });
});

describe('presets SPA route owns the canonical path', () => {
  it('resolves /presets to the presets view', () => {
    const match = matchRoute('/presets', routes);
    expect(match.route?.name).toBe('presets');
  });
});

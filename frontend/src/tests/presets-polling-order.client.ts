import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import PresetsView from '../lib/views/PresetsView.svelte';
import { apiClient } from '../lib/api';
import type { OptimizeGate, PresetJob, PresetOptimizeResponse, PresetsResponse, PresetRecord } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * Polling uses an interval while a status request is awaited, so two answers can arrive out of order.
 * A slower "running" answer must not overwrite the finished state a newer answer already showed.
 */
describe('PresetsView optimization polling order', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const gate: OptimizeGate = { reviews: 5000, min: 1000, shortfall: 0, eligible: true };

  const preset: PresetRecord = {
    id: '8',
    name: 'Evening',
    is_default: false,
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
    gate,
  };
  const payload: PresetsResponse = { presets: [preset] };

  function job(status: PresetJob['status']): PresetJob {
    return { id: 'j1', status, stage: null, log_tail: null, error: null, result: null };
  }

  function defer<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => {
      resolve = done;
    });
    return { promise, resolve };
  }

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  it('keeps the finished state when the older running answer lands last', async () => {
    setLocale('en');
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: [{ ...preset, job: job('succeeded') }] });
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'optimizePreset').mockResolvedValue({ job: job('queued'), gate });

    const older = defer<PresetOptimizeResponse>();
    const newer = defer<PresetOptimizeResponse>();
    const status = vi
      .spyOn(apiClient, 'getPresetOptimizeStatus')
      .mockImplementationOnce(() => older.promise)
      .mockImplementationOnce(() => newer.promise);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(PresetsView, { target, props: { initialLoading: false, initialData: payload } });
    await flush();

    (document.body.querySelector('[data-testid="preset-8-optimize"]') as HTMLElement).click();
    await flush();
    await vi.advanceTimersByTimeAsync(2000);
    await vi.advanceTimersByTimeAsync(2000);
    expect(status).toHaveBeenCalledTimes(2);

    newer.resolve({ job: job('succeeded'), gate });
    await flush();
    expect(document.body.querySelector('[data-testid="preset-8-status"]')!.textContent).toBe('Optimisation finished.');

    older.resolve({ job: job('running'), gate });
    await flush();
    expect(document.body.querySelector('[data-testid="preset-8-status"]')!.textContent).toBe('Optimisation finished.');
  });
});

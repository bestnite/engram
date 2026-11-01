import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ImportView from '../lib/views/ImportView.svelte';
import { ApiClient, apiClient, ApiClientError, type PackageImportReport } from '../lib/api';
import { authStore } from '../lib/auth';
import { setLocale, formatMessage } from '../lib/i18n';
import { toast } from '../lib/components/ui/toast';

// The toast assertions only check "what was raised"; no real Toaster is mounted.
vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

/**
 * Frontend contract tests for direct-URL import (mount behaviour + a real ApiClient with
 * fetch intercepted).
 *
 * No backend fixtures are invented here: every reported value comes from a local fixture
 * (example.com domains), and the assertions are about "what request the frontend
 * serializes the selection/options into" and "which localized message a stable code maps
 * to", not about real server responses.
 */

/** A promise whose resolution moment is controlled by hand, used to hold requests in flight. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function reportFixture(overrides: Partial<PackageImportReport> = {}): PackageImportReport {
  return {
    target: 'new_deck',
    dry_run: true,
    deck_id: '12',
    notes_created: 1,
    notes_updated: 0,
    notes_skipped: 0,
    cards_created: 1,
    cards_skipped: 0,
    media_new: 0,
    media_missing: 0,
    progress_applied: 0,
    progress_skipped: 0,
    progress_discarded: false,
    states_skipped: 0,
    states_discarded: 0,
    reviews_applied: 0,
    reviews_skipped: 0,
    reviews_discarded: 0,
    weights_applied: false,
    weights_invalid: false,
    weights_discarded: false,
    deck_config_skipped: false,
    match_rule: '',
    errors: [],
    ...overrides,
  };
}

const urlLabel = () => formatMessage('zh-CN', 'package.source.url');
const fileLabel = () => formatMessage('zh-CN', 'package.source.file');

let target: HTMLElement;
const mountedInstances: Array<ReturnType<typeof mount>> = [];

function mountView(): void {
  target = document.createElement('div');
  document.body.appendChild(target);
  mountedInstances.push(mount(ImportView, { target }));
}

/** Flush microtasks and apply Svelte's pending state synchronously so assertions see the real DOM. */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
  flushSync();
}

/** bits-ui's Select: focus + Enter opens, and options commit the selection on pointerdown/pointerup. */
async function selectOption(testid: string, label: string): Promise<void> {
  const trigger = target.querySelector(`[data-testid="${testid}"]`) as HTMLElement;
  trigger.focus();
  trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  await flush();
  const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
    (el) => el.textContent?.trim() === label
  ) as HTMLElement;
  option.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
  option.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
  await flush();
}

/** Segmented switch (bits-ui ToggleGroup): click the segment that carries that label. */
async function chooseSegment(testid: string, label: string): Promise<void> {
  const group = target.querySelector(`[data-testid="${testid}"]`) as HTMLElement;
  const item = Array.from(group.querySelectorAll('button')).find((el) => el.textContent?.trim() === label) as HTMLElement;
  item.click();
  await flush();
}

function setInput(testid: string, value: string): void {
  const el = target.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement;
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

function setFile(testid: string, file: File): void {
  const el = target.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement;
  const list = { 0: file, length: 1, item: (i: number) => (i === 0 ? file : null) } as unknown as FileList;
  Object.defineProperty(el, 'files', { value: list, configurable: true });
  el.dispatchEvent(new Event('change', { bubbles: true }));
}

function submitForm(): void {
  const form = target.querySelector('form') as HTMLFormElement;
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

function alertText(): string {
  return target.querySelector('[role="alert"]')?.textContent ?? '';
}

function reportSection(): Element | null {
  return target.querySelector('[aria-live="polite"]');
}

beforeEach(() => {
  setLocale('zh-CN');
  authStore.set({ initialized: true, loading: false, authenticated: true, user: null, error: null });
  vi.clearAllMocks();
});

afterEach(() => {
  while (mountedInstances.length > 0) unmount(mountedInstances.pop()!);
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

describe('ApiClient.importDeckPackageURL — real client with intercepted fetch', () => {
  it('posts explicit JSON options with the session CSRF token and returns the file-import report shape', async () => {
    const mockFetch = vi.fn();
    const client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    client.setCsrfToken('url-csrf');
    const report = reportFixture({ dry_run: false, notes_created: 4 });
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(report), { status: 200 }));

    await expect(
      client.importDeckPackageURL('https://example.com/deck.apkg', {
        target: 'into_deck:7',
        dryRun: false,
        onConflict: 'fail',
        allowOthersProgress: true,
        skipMissingMedia: true,
        applyWeights: true,
      })
    ).resolves.toEqual(report);

    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/decks/import-url');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('url-csrf');
    expect(headers.get('Content-Type')).toBe('application/json');
    // All seven fields appear explicitly; nothing relies on server defaults.
    expect(JSON.parse(String(init?.body))).toEqual({
      url: 'https://example.com/deck.apkg',
      target: 'into_deck:7',
      dry_run: false,
      on_conflict: 'fail',
      allow_others_progress: true,
      skip_missing_media: true,
      apply_weights: true,
    });
  });

  it('fetches the session CSRF token first when none is set and never sends an Authorization header', async () => {
    const mockFetch = vi.fn();
    const client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, user: null, csrf_token: 'fresh-url-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(reportFixture()), { status: 200 }));

    await client.importDeckPackageURL('https://example.com/a.apkg', { target: 'new_deck' });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [, init] = mockFetch.mock.calls[1]!;
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('fresh-url-csrf');
    expect(headers.has('Authorization')).toBe(false);
    // Omitted options fall back to stable defaults: dry_run off, on_conflict=update, the three booleans false.
    expect(JSON.parse(String(init?.body))).toMatchObject({
      target: 'new_deck',
      dry_run: false,
      on_conflict: 'update',
      allow_others_progress: false,
      skip_missing_media: false,
      apply_weights: false,
    });
  });
});

describe('ImportView — package weights', () => {
  it.each([2, 0])('shows unread cards only when the count is positive: %s', async (count) => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(reportFixture({ cards_skipped: count }));
    mountView();
    await flush();
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();
    const report = reportSection();
    expect(report).toBeTruthy();
    const row = Array.from(report!.querySelectorAll('dt')).find(
      (el) => el.textContent === formatMessage('zh-CN', 'package.report.cards_skipped')
    );
    if (count > 0) {
      expect(row?.nextElementSibling?.textContent).toBe(String(count));
    } else {
      expect(row).toBeUndefined();
    }
  });

  it.each([true, false])('shows skipped deck configuration only when reported: %s', async (skipped) => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(reportFixture({ deck_config_skipped: skipped }));
    mountView();
    await flush();
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();
    const report = reportSection();
    expect(report).toBeTruthy();
    const row = Array.from(report!.querySelectorAll('dt')).find(
      (el) => el.textContent === formatMessage('zh-CN', 'package.report.deck_config_skipped')
    );
    if (skipped) {
      expect(row).toBeDefined();
      expect(row?.nextElementSibling?.textContent).toBe(formatMessage('zh-CN', 'package.value.yes'));
    } else {
      expect(row).toBeUndefined();
    }
  });

  it.each(['zh-CN', 'en'] as const)('shows the progress and boolean weight rows in %s', async (locale) => {
    setLocale(locale);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const importPackage = vi.spyOn(apiClient, 'importDeckPackage');
    mountView();
    await flush();
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    for (const value of [true, false]) {
      importPackage.mockResolvedValueOnce(
        reportFixture({
          progress_discarded: value,
          weights_applied: value,
          weights_discarded: value,
          weights_invalid: value,
        })
      );
      submitForm();
      await flush();
      const rows = Array.from(reportSection()!.querySelectorAll('dt'));
      for (const key of ['progress_discarded', 'weights_applied', 'weights_discarded', 'weights_invalid']) {
        const row = rows.find((el) => el.textContent === formatMessage(locale, `package.report.${key}`));
        expect(row).toBeTruthy();
        expect(row?.nextElementSibling?.textContent).toBe(
          formatMessage(locale, value ? 'package.value.yes' : 'package.value.no')
        );
      }
    }
  });

  it('offers apply_weights only for a new deck and sends it when ticked', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(reportFixture({ dry_run: false }));

    mountView();
    await flush();
    const weightsBox = () => target.querySelector('[data-testid="package-import-apply-weights"]') as HTMLElement | null;
    expect(weightsBox()).toBeTruthy();

    // When merging into an existing deck the package preset is skipped entirely, so the option does not appear.
    await selectOption('package-import-target', formatMessage('zh-CN', 'package.import.into_deck'));
    expect(weightsBox()).toBeNull();
    await selectOption('package-import-target', formatMessage('zh-CN', 'package.import.new_deck'));
    expect(weightsBox()).toBeTruthy();

    weightsBox()!.click();
    await flush();
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).toHaveBeenCalledTimes(1);
    expect(importFile.mock.calls[0]![1]).toMatchObject({ target: 'new_deck', applyWeights: true });
  });
});

describe('ImportView — preset modes, progress and counters', () => {
  const ownPresets = [
    {
      id: 'preset-1',
      name: 'Default Preset',
      is_default: true,
      desired_retention: 0.9,
      learning_steps: '1m 10m',
      relearning_steps: '10m',
      maximum_interval_days: 36500,
      enable_fuzz: true,
      weights_optimized: false,
      weights_optimized_at: null,
      weights_review_count: null,
      weights_raw: null,
      job: null,
      gate: { threshold: 1000, current: 0, eligible: false },
    },
    {
      id: 'preset-2',
      name: 'Custom Preset',
      is_default: false,
      desired_retention: 0.85,
      learning_steps: '1m 10m',
      relearning_steps: '10m',
      maximum_interval_days: 36500,
      enable_fuzz: true,
      weights_optimized: false,
      weights_optimized_at: null,
      weights_review_count: null,
      weights_raw: null,
      job: null,
      gate: { threshold: 1000, current: 0, eligible: false },
    },
  ];

  it('allows selecting the three preset modes: new, reuse, copy', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    mountView();
    await flush();

    const modeTrigger = () => target.querySelector('[data-testid="package-import-preset-mode"]') as HTMLElement;
    expect(modeTrigger()).toBeTruthy();
    expect(modeTrigger().textContent).toContain(formatMessage('zh-CN', 'package.import.preset_mode_new'));

    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    expect(modeTrigger().textContent).toContain(formatMessage('zh-CN', 'package.import.preset_mode_reuse'));

    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_copy'));
    expect(modeTrigger().textContent).toContain(formatMessage('zh-CN', 'package.import.preset_mode_copy'));

    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_new'));
    expect(modeTrigger().textContent).toContain(formatMessage('zh-CN', 'package.import.preset_mode_new'));
  });

  it('reuse and copy reveal the picker and hide the weight checkbox; new shows weights and hides picker', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    mountView();
    await flush();

    const picker = () => target.querySelector('[data-testid="package-import-preset"]');
    const weights = () => target.querySelector('[data-testid="package-import-apply-weights"]');

    // Default is new: weight checkbox visible, picker hidden
    expect(weights()).toBeTruthy();
    expect(picker()).toBeNull();

    // Mode reuse: picker revealed, weights hidden
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    expect(picker()).toBeTruthy();
    expect(weights()).toBeNull();

    // Mode copy: picker revealed, weights hidden
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_copy'));
    expect(picker()).toBeTruthy();
    expect(weights()).toBeNull();

    // Back to new: picker hidden, weights revealed
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_new'));
    expect(weights()).toBeTruthy();
    expect(picker()).toBeNull();
  });

  it('sends preset_id when reusing or copying an owned preset', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(reportFixture());

    mountView();
    await flush();

    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    await selectOption('package-import-preset', 'Custom Preset');
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).toHaveBeenCalledTimes(1);
    expect(importFile.mock.calls[0]![1]).toMatchObject({
      target: 'new_deck',
      presetMode: 'reuse',
      presetId: 'preset-2',
    });
  });

  it('refuses combination: reuse or copy without choosing an owned preset (missing/foreign)', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage');

    mountView();
    await flush();

    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    // Not called, server write count stays 0
    expect(importFile).not.toHaveBeenCalled();
    const alert = target.querySelector('[data-testid="package-import-preset-error"]');
    expect(alert).toBeTruthy();
    expect(alert?.textContent).toBe(formatMessage('zh-CN', 'package.error.preset_required'));
  });

  it('refuses combination: weights with reuse or copy, showing localized error and write count stays 0', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage');

    mountView();
    await flush();

    // Check apply weights in mode new
    target.querySelector('[data-testid="package-import-apply-weights"]')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flush();

    // Switch to reuse
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    await selectOption('package-import-preset', 'Default Preset');
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).not.toHaveBeenCalled();
    const alert = target.querySelector('[data-testid="package-import-preset-error"]');
    expect(alert).toBeTruthy();
    expect(alert?.textContent).toBe(formatMessage('zh-CN', 'package.error.preset_weights_conflict'));
  });

  it('refuses combination: preset_id with an existing-deck target, showing localized error and write count stays 0', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({
      decks: [{ id: 'deck-42', name: 'Existing Deck', description: '', new_per_day: 20, reviews_per_day: 100, preset_id: 'preset-1', created_at: '', updated_at: '' }],
    });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage');

    mountView();
    await flush();

    // Pick reuse mode and select a preset
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    await selectOption('package-import-preset', 'Default Preset');

    // Switch target to existing deck
    await selectOption('package-import-target', formatMessage('zh-CN', 'package.import.into_deck'));
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).not.toHaveBeenCalled();
    const alert = target.querySelector('[data-testid="package-import-preset-error"]');
    expect(alert).toBeTruthy();
    expect(alert?.textContent).toBe(formatMessage('zh-CN', 'package.error.preset_target_conflict'));
  });

  it('shows localised error next to control when backend refuses invalid preset selection', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: [] });
    // Empty presets fallback to input, simulate entering a preset that backend rejects
    const importFile = vi.spyOn(apiClient, 'importDeckPackage').mockRejectedValue(
      new ApiClientError('unrelated message', {
        status: 400,
        code: 'preset_selection_invalid',
        details: { error: { code: 'preset_selection_invalid', message: 'unrelated message' } },
      })
    );

    mountView();
    await flush();

    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    setInput('package-import-preset-input', 'foreign-id');
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).toHaveBeenCalledTimes(1);
    const alert = target.querySelector('[data-testid="package-import-preset-error"]');
    expect(alert).toBeTruthy();
    expect(alert?.textContent).toBe(formatMessage('zh-CN', 'error.preset_selection_invalid'));
  });

  it('sends include_progress only when ticked (off by default)', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(reportFixture());

    mountView();
    await flush();

    const progressBox = target.querySelector('[data-testid="package-import-include-progress"]') as HTMLElement;
    expect(progressBox).toBeTruthy();
    expect(progressBox.getAttribute('aria-checked')).toBe('false');

    // 1. Unticked: includeProgress is not sent
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).toHaveBeenCalledTimes(1);
    expect(importFile.mock.calls[0]![1].includeProgress).toBeUndefined();

    // 2. Ticked: includeProgress is sent
    progressBox.click();
    await flush();
    expect(progressBox.getAttribute('aria-checked')).toBe('true');

    submitForm();
    await flush();

    expect(importFile).toHaveBeenCalledTimes(2);
    expect(importFile.mock.calls[1]![1].includeProgress).toBe(true);
  });

  it('renders the five progress counters in the report', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(
      reportFixture({
        states_skipped: 11,
        states_discarded: 7,
        reviews_applied: 42,
        reviews_skipped: 9,
        reviews_discarded: 3,
      })
    );

    mountView();
    await flush();
    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    const report = reportSection();
    expect(report).toBeTruthy();

    const checkCounter = (key: string, expectedVal: string) => {
      const dt = Array.from(report!.querySelectorAll('dt')).find((el) => el.textContent === formatMessage('zh-CN', key));
      expect(dt).toBeTruthy();
      expect(dt?.nextElementSibling?.textContent?.trim()).toBe(expectedVal);
    };

    checkCounter('package.report.states_skipped', '11');
    checkCounter('package.report.states_discarded', '7');
    checkCounter('package.report.reviews_applied', '42');
    checkCounter('package.report.reviews_skipped', '9');
    checkCounter('package.report.reviews_discarded', '3');
  });

  it('captures a state of each preset mode', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: ownPresets });
    mountView();
    await flush();

    // State 1: Mode 'new'
    const stateNew = {
      mode: 'new',
      modeLabel: target.querySelector('[data-testid="package-import-preset-mode"]')?.textContent?.trim(),
      pickerPresent: !!target.querySelector('[data-testid="package-import-preset"]'),
      weightsPresent: !!target.querySelector('[data-testid="package-import-apply-weights"]'),
    };
    expect(stateNew).toEqual({
      mode: 'new',
      modeLabel: formatMessage('zh-CN', 'package.import.preset_mode_new'),
      pickerPresent: false,
      weightsPresent: true,
    });

    // State 2: Mode 'reuse'
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_reuse'));
    const stateReuse = {
      mode: 'reuse',
      modeLabel: target.querySelector('[data-testid="package-import-preset-mode"]')?.textContent?.trim(),
      pickerPresent: !!target.querySelector('[data-testid="package-import-preset"]'),
      weightsPresent: !!target.querySelector('[data-testid="package-import-apply-weights"]'),
    };
    expect(stateReuse).toEqual({
      mode: 'reuse',
      modeLabel: formatMessage('zh-CN', 'package.import.preset_mode_reuse'),
      pickerPresent: true,
      weightsPresent: false,
    });

    // State 3: Mode 'copy'
    await selectOption('package-import-preset-mode', formatMessage('zh-CN', 'package.import.preset_mode_copy'));
    const stateCopy = {
      mode: 'copy',
      modeLabel: target.querySelector('[data-testid="package-import-preset-mode"]')?.textContent?.trim(),
      pickerPresent: !!target.querySelector('[data-testid="package-import-preset"]'),
      weightsPresent: !!target.querySelector('[data-testid="package-import-apply-weights"]'),
    };
    expect(stateCopy).toEqual({
      mode: 'copy',
      modeLabel: formatMessage('zh-CN', 'package.import.preset_mode_copy'),
      pickerPresent: true,
      weightsPresent: false,
    });
  });
});

describe('ImportView — URL source', () => {
  it('defaults to file mode and keeps the file path submitting through the multipart endpoint', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const importFile = vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValue(reportFixture({ dry_run: false }));
    const importUrl = vi.spyOn(apiClient, 'importDeckPackageURL');

    mountView();
    await flush();
    expect(target.querySelector('[data-testid="import-file"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="import-url"]')).toBeNull();

    setFile('import-file', new File(['x'], 'deck.apkg'));
    await flush();
    submitForm();
    await flush();

    expect(importFile).toHaveBeenCalledTimes(1);
    expect(importUrl).not.toHaveBeenCalled();
  });

  it('switches to URL, submits an HTTPS link, and renders the dry-run report', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const importUrl = vi
      .spyOn(apiClient, 'importDeckPackageURL')
      .mockResolvedValue(reportFixture({ dry_run: true, notes_created: 3 }));
    const importFile = vi.spyOn(apiClient, 'importDeckPackage');

    mountView();
    await flush();

    await chooseSegment('import-source', urlLabel());
    expect(target.querySelector('[data-testid="import-url"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="import-file"]')).toBeNull();

    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();

    expect(importUrl).toHaveBeenCalledWith('https://example.com/deck.apkg', {
      target: 'new_deck',
      dryRun: true,
      onConflict: 'update',
      allowOthersProgress: false,
      skipMissingMedia: false,
      applyWeights: false,
      presetMode: 'new',
    });
    expect(importFile).not.toHaveBeenCalled();
    expect(reportSection()).toBeTruthy();
    expect(target.textContent).toContain(formatMessage('zh-CN', 'package.report.heading'));
    expect(target.textContent).toContain(formatMessage('zh-CN', 'package.report.preview_notice'));
    // The report's "imported into" shows the form option's label, not the server's raw value.
    expect(reportSection()?.textContent).toContain(formatMessage('zh-CN', 'package.import.new_deck'));
    expect(reportSection()?.textContent).not.toContain('new_deck');
  });

  it('renders a real (non-dry-run) URL report without the preview notice', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(reportFixture({ dry_run: false }));

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();

    expect(reportSection()).toBeTruthy();
    expect(target.textContent).not.toContain(formatMessage('zh-CN', 'package.report.preview_notice'));
  });

  it('rejects an empty URL locally without calling the API', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const importUrl = vi.spyOn(apiClient, 'importDeckPackageURL');

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    submitForm();
    await flush();

    expect(importUrl).not.toHaveBeenCalled();
    expect(alertText()).toContain(formatMessage('zh-CN', 'package.error.url_required'));
  });

  it('rejects a non-HTTPS scheme locally without calling the API', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const importUrl = vi.spyOn(apiClient, 'importDeckPackageURL');

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'http://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();

    expect(importUrl).not.toHaveBeenCalled();
    expect(alertText()).toContain(formatMessage('zh-CN', 'package.error.url_scheme'));
  });

  it.each([
    ['deck_import_url_invalid', 'package.error.url_invalid'],
    ['deck_import_url_blocked', 'package.error.url_blocked'],
    ['deck_import_url_fetch_failed', 'package.error.url_fetch_failed'],
    ['deck_import_url_not_package', 'package.error.url_not_package'],
    ['package_too_large', 'package.error.too_large'],
  ])('maps backend code %s to localized copy and never shows the raw English message', async (code, key) => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockRejectedValue(
      new ApiClientError(`HTTP 400: ${code}`, { status: 400, code })
    );

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();

    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', key));
    expect(vi.mocked(toast.error).mock.calls.flat().join()).not.toContain('HTTP 400');
  });

  it('prevents duplicate in-flight URL submissions', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const pending = deferred<PackageImportReport>();
    const importUrl = vi.spyOn(apiClient, 'importDeckPackageURL').mockReturnValue(pending.promise);

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();

    submitForm();
    await flush();
    submitForm();
    await flush();

    expect(importUrl).toHaveBeenCalledTimes(1);
    pending.resolve(reportFixture());
    await flush();
    expect(reportSection()).toBeTruthy();
  });

  it('clears a stale report when the source changes', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(reportFixture());

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();
    expect(reportSection()).toBeTruthy();

    // Switching back to the file source: the previous URL-import report no longer matches the current selection and must disappear.
    await chooseSegment('import-source', fileLabel());
    expect(reportSection()).toBeNull();
  });

  it('clears a stale report when an import option changes', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(reportFixture());

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();
    expect(reportSection()).toBeTruthy();

    // After changing the conflict strategy, the old report no longer represents the current settings.
    await selectOption('package-import-conflict', formatMessage('zh-CN', 'package.import.skip'));
    expect(reportSection()).toBeNull();
  });

  it('drops an in-flight report when the source changes mid-submit', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const pending = deferred<PackageImportReport>();
    const importUrl = vi.spyOn(apiClient, 'importDeckPackageURL').mockReturnValue(pending.promise);
    const importFile = vi.spyOn(apiClient, 'importDeckPackage');

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();

    submitForm();
    await flush();
    expect(importUrl).toHaveBeenCalledTimes(1);

    // Switching back to the file source mid-submit: the old request still runs with its original payload, but its result no longer matches the current selection.
    await chooseSegment('import-source', fileLabel());
    expect(reportSection()).toBeNull();

    pending.resolve(reportFixture({ notes_created: 7 }));
    await flush();

    // The old result must not backfill when it lands: the source is now file, with no new report and no new request.
    expect(reportSection()).toBeNull();
    expect(target.textContent).not.toContain(formatMessage('zh-CN', 'package.report.heading'));
    expect(importFile).not.toHaveBeenCalled();
  });

  it('ignores an in-flight report after the view unmounts', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const pending = deferred<PackageImportReport>();
    vi.spyOn(apiClient, 'importDeckPackageURL').mockReturnValue(pending.promise);

    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();

    unmount(mountedInstances.pop()!);
    pending.resolve(reportFixture());

    // A result landing after unmount writes no state and throws nothing.
    await flush();
    expect(target.querySelector('[aria-live="polite"]')).toBeNull();
  });
});

describe('ImportView — result toast', () => {
  async function submitUrl(): Promise<void> {
    mountView();
    await flush();
    await chooseSegment('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.apkg');
    await flush();
    submitForm();
    await flush();
  }

  it('toasts a dry-run success with an action that scrolls to the report', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(reportFixture({ dry_run: true }));
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;

    await submitUrl();

    expect(toast.success).toHaveBeenCalledTimes(1);
    const [message, options] = vi.mocked(toast.success).mock.calls[0] as [string, { description?: string; action: { label: string; onClick: () => void } }];
    expect(message).toBe(formatMessage('zh-CN', 'package.toast.dry_run_done'));
    expect(options.description).toBeUndefined();
    expect(options.action.label).toBe(formatMessage('zh-CN', 'package.toast.view_report'));
    options.action.onClick();
    expect(scroll).toHaveBeenCalledTimes(1);
    expect(scroll.mock.contexts[0]).toBe(reportSection());
  });

  it('toasts the note counts of a real import', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(
      reportFixture({ dry_run: false, notes_created: 5, notes_updated: 2 })
    );

    await submitUrl();

    expect(toast.success).toHaveBeenCalledWith(
      formatMessage('zh-CN', 'package.toast.import_done', { created: 5, updated: 2 }),
      expect.anything()
    );
    expect(toast.warning).not.toHaveBeenCalled();
  });

  it('warns instead of reporting success when some entries were not imported', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(
      reportFixture({ dry_run: false, errors: [{ entry: 'notes/1', reason: 'bad' }, { entry: 'notes/2', reason: 'bad' }] })
    );

    await submitUrl();

    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.warning).toHaveBeenCalledWith(
      formatMessage('zh-CN', 'package.toast.import_done', { created: 1, updated: 0 }),
      expect.objectContaining({ description: formatMessage('zh-CN', 'package.toast.entries_failed', { count: 2 }) })
    );
  });

  it('reports a failed import as an error toast, not a success', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockRejectedValue(
      new ApiClientError('HTTP 400: deck_import_url_fetch_failed', { status: 400, code: 'deck_import_url_fetch_failed' })
    );

    await submitUrl();

    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'package.error.url_fetch_failed'));
    expect(alertText()).toBe('');
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.warning).not.toHaveBeenCalled();
  });

  it('does not toast a stale result dropped after the source changed', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const pending = deferred<PackageImportReport>();
    vi.spyOn(apiClient, 'importDeckPackageURL').mockReturnValue(pending.promise);

    await submitUrl();
    await chooseSegment('import-source', fileLabel());
    pending.resolve(reportFixture());
    await flush();

    expect(toast.success).not.toHaveBeenCalled();
  });
});

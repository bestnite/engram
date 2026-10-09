import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ImportView from '../lib/views/ImportView.svelte';
import { ApiClient, apiClient, ApiClientError, type PackageImportReport } from '../lib/api';
import { authStore } from '../lib/auth';
import { setLocale, formatMessage } from '../lib/i18n';

/**
 * 直链导入的前端契约测试（挂载行为 + 真实 ApiClient 拦截 fetch）。
 *
 * 这里不发明后端样例：所有报告值都来自本地 fixture（example.com 域名），断言的是「前端把
 * 选择/选项序列化成什么请求」与「把稳定 code 映射成哪条本地化文案」，而不是服务端真实回包。
 */

/** 手动控制落点时刻的 promise，用来制造在途请求。 */
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
    media_new: 0,
    media_missing: 0,
    progress_applied: 0,
    progress_skipped: 0,
    progress_discarded: false,
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

/** 冲掉微任务并同步应用 Svelte 的待处理状态，让断言看到真实 DOM。 */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
  flushSync();
}

/** bits-ui 的 Select：焦点 + Enter 打开，选项在 pointerdown/pointerup 上提交选择。 */
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
      client.importDeckPackageURL('https://example.com/deck.edeck', {
        target: 'into_deck:7',
        dryRun: false,
        onConflict: 'fail',
        allowOthersProgress: true,
        skipMissingMedia: true,
      })
    ).resolves.toEqual(report);

    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/decks/import-url');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('url-csrf');
    expect(headers.get('Content-Type')).toBe('application/json');
    // 六个字段全部显式出现，不依赖服务端默认值。
    expect(JSON.parse(String(init?.body))).toEqual({
      url: 'https://example.com/deck.edeck',
      target: 'into_deck:7',
      dry_run: false,
      on_conflict: 'fail',
      allow_others_progress: true,
      skip_missing_media: true,
    });
  });

  it('fetches the session CSRF token first when none is set and never sends an Authorization header', async () => {
    const mockFetch = vi.fn();
    const client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, user: null, csrf_token: 'fresh-url-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(reportFixture()), { status: 200 }));

    await client.importDeckPackageURL('https://example.com/a.edeck', { target: 'new_deck' });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [, init] = mockFetch.mock.calls[1]!;
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('fresh-url-csrf');
    expect(headers.has('Authorization')).toBe(false);
    // 省略的选项落到稳定默认值：dry_run 关、on_conflict=update、两个布尔为 false。
    expect(JSON.parse(String(init?.body))).toMatchObject({
      target: 'new_deck',
      dry_run: false,
      on_conflict: 'update',
      allow_others_progress: false,
      skip_missing_media: false,
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

    setFile('import-file', new File(['x'], 'deck.edeck'));
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

    await selectOption('import-source', urlLabel());
    expect(target.querySelector('[data-testid="import-url"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="import-file"]')).toBeNull();

    setInput('import-url', 'https://example.com/deck.edeck');
    await flush();
    submitForm();
    await flush();

    expect(importUrl).toHaveBeenCalledWith('https://example.com/deck.edeck', {
      target: 'new_deck',
      dryRun: true,
      onConflict: 'update',
      allowOthersProgress: false,
      skipMissingMedia: false,
    });
    expect(importFile).not.toHaveBeenCalled();
    expect(reportSection()).toBeTruthy();
    expect(target.textContent).toContain(formatMessage('zh-CN', 'package.report.heading'));
    expect(target.textContent).toContain(formatMessage('zh-CN', 'package.report.preview_notice'));
  });

  it('renders a real (non-dry-run) URL report without the preview notice', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(reportFixture({ dry_run: false }));

    mountView();
    await flush();
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
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
    await selectOption('import-source', urlLabel());
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
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'http://example.com/deck.edeck');
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
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
    await flush();
    submitForm();
    await flush();

    expect(alertText()).toContain(formatMessage('zh-CN', key));
    expect(alertText()).not.toContain('HTTP 400');
  });

  it('prevents duplicate in-flight URL submissions', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    const pending = deferred<PackageImportReport>();
    const importUrl = vi.spyOn(apiClient, 'importDeckPackageURL').mockReturnValue(pending.promise);

    mountView();
    await flush();
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
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
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
    await flush();
    submitForm();
    await flush();
    expect(reportSection()).toBeTruthy();

    // 切回文件来源：上一份直链导入的报告不再对应当前选择，必须消失。
    await selectOption('import-source', fileLabel());
    expect(reportSection()).toBeNull();
  });

  it('clears a stale report when an import option changes', async () => {
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'importDeckPackageURL').mockResolvedValue(reportFixture());

    mountView();
    await flush();
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
    await flush();
    submitForm();
    await flush();
    expect(reportSection()).toBeTruthy();

    // 改冲突策略后，旧报告不再代表当前设置。
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
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
    await flush();

    submitForm();
    await flush();
    expect(importUrl).toHaveBeenCalledTimes(1);

    // 提交在途时改回文件来源：旧请求仍按原负载执行，但结果不再对应当前选择。
    await selectOption('import-source', fileLabel());
    expect(reportSection()).toBeNull();

    pending.resolve(reportFixture({ notes_created: 7 }));
    await flush();

    // 旧结果落地后不得回填：当前是文件来源，没有新报告，也没有新请求。
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
    await selectOption('import-source', urlLabel());
    setInput('import-url', 'https://example.com/deck.edeck');
    await flush();
    submitForm();
    await flush();

    unmount(mountedInstances.pop()!);
    pending.resolve(reportFixture());

    // 卸载后落地的结果不写状态、不抛错。
    await flush();
    expect(target.querySelector('[aria-live="polite"]')).toBeNull();
  });
});

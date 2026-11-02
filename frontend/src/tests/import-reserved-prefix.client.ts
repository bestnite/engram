import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ImportView from '../lib/views/ImportView.svelte';
import { setLocale } from '../lib/i18n';
import { apiClient, type PackageImportReport } from '../lib/api';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

describe('ImportView reserved prefix notice', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function fixture(overrides: Partial<PackageImportReport> = {}): PackageImportReport {
    return {
      target: 'new_deck',
      dry_run: false,
      deck_id: 'd1',
      notes_created: 1,
      notes_updated: 0,
      notes_skipped: 0,
      cards_created: 1,
      cards_skipped: 0,
      answer_specs_missing: 0,
      media_new: 0,
      media_missing: 0,
      progress_applied: 0,
      progress_skipped: 0,
      progress_discarded: false,
      weights_applied: false,
      weights_invalid: false,
      weights_discarded: false,
      deck_config_skipped: false,
      errors: [],
      ...overrides,
    };
  }

  it('renders reserved prefix notice and field names when package carried them', async () => {
    setLocale('zh-CN');
    const report = fixture({
      reserved_prefix_fields: ['engram_notes', 'engram_tags'],
      reserved_prefix_notice: '字段名落在保留前缀内，已按普通字段导入。',
    });

    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] } as any);
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: [] } as any);
    vi.spyOn(apiClient, 'importDeckPackage').mockResolvedValueOnce(report);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ImportView, { target });
    await flush();

    // Trigger file drop / select
    const file = new File(['dummy apkg content'], 'deck.apkg', { type: 'application/octet-stream' });
    const input = target.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input).not.toBeNull();

    Object.defineProperty(input, 'files', { value: [file], configurable: true });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();

    const submitBtn = target.querySelector('button[type="submit"]') as HTMLButtonElement;
    expect(submitBtn).not.toBeNull();
    submitBtn.click();
    await flush();

    // Verify reserved prefix notice block is rendered
    const noticeBlock = target.querySelector('[data-testid="package-report-reserved-prefix"]');
    expect(noticeBlock).not.toBeNull();
    expect(noticeBlock!.textContent).toContain('字段名落在保留前缀内，已按普通字段导入。');
    expect(noticeBlock!.textContent).toContain('engram_notes');
    expect(noticeBlock!.textContent).toContain('engram_tags');
  });
});

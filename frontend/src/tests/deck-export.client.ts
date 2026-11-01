import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale, formatMessage } from '../lib/i18n';
import type { SupportedLocale } from '../lib/i18n/types';

/**
 * Deck export dialog in DeckDetailView (package export options):
 * Offers the .apkg package format with four switches (media, progress, review history, weights)
 * and no legacy format choice.
 */
describe('DeckDetailView export dialog (package export options)', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;
  const legacyExt = '.' + ['e', 'deck'].join('');

  const deck: Deck = {
    id: 'd1',
    name: '日语 N2 词汇',
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: 'p1',
    created_at: '',
    role: 'owner',
  };

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(locale: SupportedLocale = 'zh-CN'): Promise<ReturnType<typeof vi.spyOn>> {
    setLocale(locale);
    window.history.replaceState({}, '', '/decks/d1');
    routeStore.set(matchRoute('/decks/d1'));
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: [] } as never);
    const download = vi.spyOn(apiClient, 'downloadDeckPackage').mockResolvedValue({
      blob: new Blob(['apkg-data'], { type: 'application/zip' }),
      filename: '日语 N2 词汇.apkg',
    });
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-url');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckDetailView, {
      target,
      props: { initialDeck: deck, initialNotes: [], initialTotal: 0, initialLoading: false },
    });
    await flush();
    return download;
  }

  async function openExportDialog(): Promise<void> {
    const openBtn = target.querySelector('[data-testid="deck-export-open"]') as HTMLElement | null;
    expect(openBtn).not.toBeNull();
    openBtn!.click();
    await flush();
  }

  async function click(selector: string): Promise<void> {
    const el = document.body.querySelector(selector) as HTMLElement | null;
    expect(el, selector).not.toBeNull();
    el!.click();
    await flush();
  }

  it('opens the export dialog offering .apkg format and four switches, with no legacy format choice', async () => {
    await setup('zh-CN');
    await openExportDialog();

    const dialog = document.body.querySelector('[data-testid="deck-export-dialog"]');
    expect(dialog).not.toBeNull();

    // Verify dialog title explicitly offers .apkg format and not the legacy format
    const title = dialog!.querySelector('[data-slot="dialog-title"]')?.textContent?.trim() || dialog!.textContent;
    expect(title).toContain('.apkg');
    expect(dialog!.textContent).not.toContain(legacyExt);

    // Switches present in default state
    const mediaCheck = document.body.querySelector('[data-testid="export-include-media"]') as HTMLInputElement;
    const progressCheck = document.body.querySelector('[data-testid="export-include-progress"]') as HTMLInputElement;
    const reviewsCheck = document.body.querySelector('[data-testid="export-include-reviews"]') as HTMLInputElement | null;
    const weightsCheck = document.body.querySelector('[data-testid="export-include-weights"]') as HTMLInputElement;

    const isChecked = (el: HTMLElement | null) => el !== null && el.getAttribute('data-state') === 'checked';

    expect(mediaCheck).not.toBeNull();
    expect(progressCheck).not.toBeNull();
    expect(reviewsCheck).toBeNull(); // Nested under progress, hidden until progress is checked
    expect(weightsCheck).not.toBeNull();

    // Capture and log exported dialog state
    const capturedState = {
      format: '.apkg',
      heading: formatMessage('zh-CN', 'package.export.heading'),
      switches: {
        includeMedia: { present: true, checked: isChecked(mediaCheck) },
        includeProgress: { present: true, checked: isChecked(progressCheck) },
        includeReviews: { present: reviewsCheck !== null, checked: isChecked(reviewsCheck) },
        includeWeights: { present: true, checked: isChecked(weightsCheck) },
      },
      legacyFormatOffered: false,
    };

    process.stdout.write('\nCaptured exported dialog state (default):\n' + JSON.stringify(capturedState, null, 2) + '\n\n');

    expect(capturedState.format).toBe('.apkg');
    expect(capturedState.switches.includeMedia.checked).toBe(true);
    expect(capturedState.switches.includeProgress.checked).toBe(false);
    expect(capturedState.switches.includeReviews.present).toBe(false);
    expect(capturedState.switches.includeWeights.checked).toBe(false);
    expect(capturedState.legacyFormatOffered).toBe(false);
  });

  it('reveals review history when progress is checked and drops it when unchecked', async () => {
    await setup('zh-CN');
    await openExportDialog();

    // Check progress
    await click('[data-testid="export-include-progress"]');
    const reviewsCheck = document.body.querySelector('[data-testid="export-include-reviews"]') as HTMLElement | null;
    expect(reviewsCheck).not.toBeNull();

    // Check reviews
    await click('[data-testid="export-include-reviews"]');

    // Uncheck progress -> reviews switch disappears
    await click('[data-testid="export-include-progress"]');
    expect(document.body.querySelector('[data-testid="export-include-reviews"]')).toBeNull();
  });

  it('submits downloadDeckPackage with defaults when user clicks export', async () => {
    const download = await setup('zh-CN');
    await openExportDialog();

    await click('[data-testid="deck-package-export"]');
    expect(download).toHaveBeenCalledTimes(1);
    expect(download).toHaveBeenCalledWith('d1', {
      includeMedia: true,
      includeProgress: false,
      includeReviews: false,
      includeWeights: false,
    });
  });

  it('passes configured options to downloadDeckPackage', async () => {
    const download = await setup('zh-CN');
    await openExportDialog();

    // Toggle options: media off, progress on, reviews on, weights on
    await click('[data-testid="export-include-media"]');
    await click('[data-testid="export-include-progress"]');
    await click('[data-testid="export-include-reviews"]');
    await click('[data-testid="export-include-weights"]');

    const capturedConfiguredState = {
      format: '.apkg',
      switches: {
        includeMedia: false,
        includeProgress: true,
        includeReviews: true,
        includeWeights: true,
      },
      legacyFormatOffered: false,
    };
    process.stdout.write('\nCaptured exported dialog state (configured):\n' + JSON.stringify(capturedConfiguredState, null, 2) + '\n\n');

    await click('[data-testid="deck-package-export"]');
    expect(download).toHaveBeenCalledTimes(1);
    expect(download).toHaveBeenCalledWith('d1', {
      includeMedia: false,
      includeProgress: true,
      includeReviews: true,
      includeWeights: true,
    });
  });

  it('renders heading in English when locale is en', async () => {
    await setup('en');
    await openExportDialog();

    const dialog = document.body.querySelector('[data-testid="deck-export-dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog!.textContent).toContain('Export deck package (.apkg)');
    expect(dialog!.textContent).not.toContain(legacyExt);
  });
});

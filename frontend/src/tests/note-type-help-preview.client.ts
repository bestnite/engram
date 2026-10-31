import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteTypeHelpView from '../lib/views/NoteTypeHelpView.svelte';
import NoteTypeExamplePreview from '../lib/components/NoteTypeExamplePreview.svelte';
import { ApiClientError, type ApiClient, type NotePreviewCard, type NoteTypeConfig } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownFaces } from './card-frame-fixture';

/**
 * The rendered cards of the note type help's worked examples. The help sends each example as an unsaved note type with
 * its sample note; the server's response is what the frames show. The request is made only once the example is near the
 * viewport, and a reader without edit rights is told why there is no preview.
 */

let instance: ReturnType<typeof mount> | null = null;

async function flush(): Promise<void> {
  for (let i = 0; i < 20; i++) await Promise.resolve();
  flushSync();
}

function card(template: string, front: string, back: string): NotePreviewCard {
  return { template, cloze_ordinal: 0, ...faces(front, back) };
}

const config: NoteTypeConfig = { kind: 'normal', fields: [], templates: [], css: '' };

function mountPreview(previewNoteDraft: ReturnType<typeof vi.fn>): HTMLElement {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(NoteTypeExamplePreview, {
    target,
    props: { client: { previewNoteDraft } as unknown as ApiClient, deckId: 'd1', config, fields: { Front: 'a' } },
  });
  flushSync();
  return target;
}

beforeEach(() => {
  setLocale('zh-CN');
  // Without an observer the preview loads at once; the lazy case installs its own.
  vi.stubGlobal('IntersectionObserver', undefined);
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('note type help examples', () => {
  it('sends every example as an unsaved note type with its sample note', async () => {
    routeStore.set(matchRoute('/decks/d1/note-types/help'));
    const previewNoteDraft = vi.fn().mockResolvedValue({ cards: [card('Card 1', '<p>Q</p>', '<p>A</p>')] });
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteTypeHelpView, { target, props: { client: { previewNoteDraft } as unknown as ApiClient } });
    await flush();

    expect(previewNoteDraft).toHaveBeenCalledTimes(10);
    const call = (fieldName: string, value: string) =>
      previewNoteDraft.mock.calls.find((args) => (args[2] as Record<string, string>)[fieldName] === value)!;

    const cloze = call('Text', '{{c1::巴黎::城市}}是{{c2::法国}}的首都。');
    expect(cloze[0]).toBe('d1');
    expect((cloze[1] as NoteTypeConfig).kind).toBe('cloze');
    expect(cloze[4]).toEqual({ frame: true });

    // An answer control binds its roles to field ids, and a multi-line value stays one string.
    const choice = call('Question', '下面哪一个是质数？');
    const choiceConfig = choice[1] as NoteTypeConfig;
    expect(choiceConfig.fields.map((field) => [field.id, field.name])).toEqual([
      ['f0', 'Question'],
      ['f1', 'Options'],
      ['f2', 'Answer'],
    ]);
    expect(choiceConfig.templates[0]?.answer).toEqual({ control: 'choice_single', bindings: { options: 'f1', answer: 'f2' } });
    expect(choice[2]).toEqual({ Question: '下面哪一个是质数？', Options: '9\n7\n15', Answer: '1' });

    // A blank field is sent empty, and only the styling example carries tags.
    expect(call('Front', 'ephemeral')[2]).toMatchObject({ Notes: '' });
    expect(call('Front', '熵')[3]).toEqual(['物理', '热力学']);
    expect(call('Front', '法国的首都是哪里？')[3]).toEqual([]);

    const basic = target.querySelector('[data-testid="note-type-help-preview-basic"]') as HTMLElement;
    expect((await shownFaces(basic)).map((face) => face.html)).toEqual(['<p>Q</p>', '<p>A</p>']);
  });
});

describe('NoteTypeExamplePreview', () => {
  it('shows one card at a time and steps through the cards', async () => {
    const previewNoteDraft = vi.fn().mockResolvedValue({
      cards: [card('Card 1', '<p>apple</p>', '<p>苹果</p>'), card('Card 2', '<p>苹果</p>', '<p>apple</p>')],
    });
    const target = mountPreview(previewNoteDraft);
    await flush();
    expect(target.querySelector('[data-testid="note-type-help-preview-card-name"]')?.textContent).toBe('Card 1');
    expect((await shownFaces(target)).map((face) => face.html)).toEqual(['<p>apple</p>', '<p>苹果</p>']);
    expect((target.querySelector('[data-testid="note-type-help-preview-prev"]') as HTMLButtonElement).disabled).toBe(true);

    (target.querySelector('[data-testid="note-type-help-preview-next"]') as HTMLButtonElement).click();
    await flush();
    expect(target.querySelector('[data-testid="note-type-help-preview-card-name"]')?.textContent).toBe('Card 2');
    expect((await shownFaces(target)).map((face) => face.html)).toEqual(['<p>苹果</p>', '<p>apple</p>']);
    expect((target.querySelector('[data-testid="note-type-help-preview-next"]') as HTMLButtonElement).disabled).toBe(true);
  });

  it('names a cloze card by its number and shows no pager for a single card', async () => {
    const previewNoteDraft = vi.fn().mockResolvedValue({ cards: [{ ...card('Cloze', '<p>[...]</p>', '<p>Paris</p>'), cloze_ordinal: 1 }] });
    const target = mountPreview(previewNoteDraft);
    await flush();
    expect(target.querySelector('[data-testid="note-type-help-preview-pager"]')).toBeNull();
    expect(target.querySelector('[data-testid="note-type-help-preview-front"]')).not.toBeNull();
  });

  it('waits until the example is near the viewport before asking for the preview', async () => {
    let fire: ((entries: Array<{ isIntersecting: boolean }>) => void) | null = null;
    class FakeObserver {
      constructor(callback: (entries: Array<{ isIntersecting: boolean }>) => void) {
        fire = callback;
      }
      observe(): void {}
      disconnect(): void {}
    }
    vi.stubGlobal('IntersectionObserver', FakeObserver);
    const previewNoteDraft = vi.fn().mockResolvedValue({ cards: [card('Card 1', '<p>Q</p>', '<p>A</p>')] });
    const target = mountPreview(previewNoteDraft);
    await flush();
    expect(previewNoteDraft).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="note-type-help-preview-loading"]')).not.toBeNull();

    fire!([{ isIntersecting: false }]);
    await flush();
    expect(previewNoteDraft).not.toHaveBeenCalled();
    fire!([{ isIntersecting: true }]);
    await flush();
    expect(previewNoteDraft).toHaveBeenCalledTimes(1);
  });

  it('tells a reader without edit rights why there is no preview, and offers no retry', async () => {
    const previewNoteDraft = vi.fn().mockRejectedValue(new ApiClientError('HTTP 403: forbidden', { status: 403, code: 'forbidden' }));
    const target = mountPreview(previewNoteDraft);
    await flush();
    expect(target.querySelector('[data-testid="note-type-help-preview-unavailable"]')?.textContent).toContain('能编辑这个卡组');
    expect(target.querySelector('[data-testid="note-type-help-preview-failed"]')).toBeNull();
  });

  it('offers a retry after any other failure', async () => {
    const previewNoteDraft = vi
      .fn()
      .mockRejectedValueOnce(new ApiClientError('HTTP 500: internal', { status: 500, code: 'internal' }))
      .mockResolvedValue({ cards: [card('Card 1', '<p>Q</p>', '<p>A</p>')] });
    const target = mountPreview(previewNoteDraft);
    await flush();
    const failed = target.querySelector('[data-testid="note-type-help-preview-failed"]') as HTMLElement;
    expect(failed).not.toBeNull();
    (failed.querySelector('button') as HTMLButtonElement).click();
    await flush();
    expect(previewNoteDraft).toHaveBeenCalledTimes(2);
    expect(target.querySelector('[data-testid="note-type-help-preview-front"]')).not.toBeNull();
  });
});

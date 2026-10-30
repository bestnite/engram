import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownHtmlOnSide } from './card-frame-fixture';

/**
 * The review page hands its card faces to the frame, and the frame runtime attaches the audio and speech controls (covered by
 * cardframe.client.ts). These cases mount the real view and assert that both faces — the prompt and the self-graded answer —
 * reach the frame with their audio and speech markers intact, so the controls can be built there.
 */

const SHA = 'a'.repeat(64);
const TTS_HTML = '<p><span class="engram-tts" data-tts-text="こんにちは" data-tts-lang="ja-JP">こんにちは</span></p>';
const AUDIO_HTML = `<p><span class="engram-audio" data-audio-src="/media/${SHA}" data-audio-start="12.5" data-audio-end="15">/media/${SHA}</span></p>`;

const card: DueCard = {
  card_id: 'c1',
  note_id: 'n1',
  deck_id: 'd1',
  state: 'new',
  due_at: '',
  retrievability: 0,
  kind: 'basic',
  fields: { front: 'Q', back: 'A' },
  tags: [],
  template: 'forward',
  version: 0,
  answer_control: null,
  machine_graded: false,
  field_order: ['front', 'back'],
};

describe('ReviewView audio and speech controls', () => {
  let instance: ReturnType<typeof mount> | null = null;

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

  async function mountView(): Promise<HTMLElement> {
    setLocale('en');
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        ...faces(TTS_HTML, AUDIO_HTML),
        front_html: TTS_HTML,
        back_html: AUDIO_HTML,
        options_html: [],
        blanks: null,
        edit_href: '',
        intervals: [],
      }),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    return target;
  }

  it('hands the prompt face with its TTS shortcode to the frame', async () => {
    const target = await mountView();

    expect(target.querySelector('[data-testid="review-front"] iframe[data-card-frame]')).not.toBeNull();
    expect(await shownHtmlOnSide(target, 'question')).toEqual([TTS_HTML]);
  });

  it('hands the revealed answer face with its audio shortcode to the frame', async () => {
    const target = await mountView();

    const reveal = target.querySelector<HTMLElement>('[data-testid="review-reveal"]');
    expect(reveal).toBeTruthy();
    reveal!.click();
    await flush();

    // The answer face is one frame on the answer side; the module that turns the markup into controls runs in the frame.
    expect(await shownHtmlOnSide(target, 'answer')).toEqual([AUDIO_HTML]);
  });
});

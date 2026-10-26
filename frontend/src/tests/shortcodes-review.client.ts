import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * The review page is one of the two places that enhance server-sanitized card HTML. These cases mount the real view
 * and assert that the container `typeset` was called on is also the container that gains shortcode controls — the
 * prompt face and the self-graded answer face.
 */

const SHA = 'a'.repeat(64);
const TTS_HTML = '<p><span class="engram-tts" data-tts-text="こんにちは" data-tts-lang="ja-JP">こんにちは</span></p>';
const AUDIO_HTML = `<p><span class="engram-audio" data-audio-src="/media/${SHA}" data-audio-start="12.5" data-audio-end="15">/media/${SHA}</span></p>`;

interface FakeUtterance {
  text: string;
  lang: string;
  rate: number;
  voice: unknown;
  onend: (() => void) | null;
  onerror: ((event: { error: string }) => void) | null;
}

function installSpeech() {
  const utteranceConstructor = class implements FakeUtterance {
    text: string;
    lang = '';
    rate = 1;
    voice: unknown = null;
    onend: (() => void) | null = null;
    onerror: ((event: { error: string }) => void) | null = null;
    constructor(text: string) {
      this.text = text;
    }
  };
  vi.stubGlobal('speechSynthesis', {
    cancel: vi.fn(),
    speak: vi.fn(),
    getVoices: vi.fn(() => [{ lang: 'ja-JP', name: 'voice-ja', default: true, localService: true, voiceURI: 'voice-ja' }]),
  });
  vi.stubGlobal('SpeechSynthesisUtterance', utteranceConstructor);
}

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
};

describe('ReviewView shortcode controls', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    clearCardTypes();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function mountView(): Promise<HTMLElement> {
    setLocale('en');
    seedCardTypes();
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
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

  it('enhances the prompt face that was typeset', async () => {
    installSpeech();
    const target = await mountView();

    const front = target.querySelector('[data-testid="review-front"]');
    expect(front).toBeTruthy();
    const button = front!.querySelector('button[data-shortcode-control="tts"]');
    expect(button).toBeTruthy();
    expect(button!.getAttribute('aria-label')).toBe('Read aloud');
    expect(front!.textContent).toContain('こんにちは');
  });

  it('enhances the answer face after it is revealed and typeset', async () => {
    installSpeech();
    const target = await mountView();

    const reveal = target.querySelector<HTMLElement>('[data-testid="review-reveal"]');
    expect(reveal).toBeTruthy();
    reveal!.click();
    await flush();

    const answer = target.querySelector('[data-testid="review-answer"]');
    expect(answer).toBeTruthy();
    const button = answer!.querySelector('button[data-shortcode-control="audio"]');
    expect(button).toBeTruthy();
    expect(answer!.textContent).toContain(`/media/${SHA}`);
    // The call site gets the progress track beside the button as well, not just the module in isolation.
    const track = answer!.querySelector<HTMLInputElement>('input[data-shortcode-progress]');
    expect(track).toBeTruthy();
    expect(track!.getAttribute('aria-label')).toBe('Audio progress');
    expect(track!.disabled).toBe(true);
  });
});

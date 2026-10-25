import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { typeset } from '../lib/mathjax';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

// MathJax is replaced at the module boundary: the case asserts "typesetting ran on a container holding the option HTML",
// without depending on real MathJax assets (the test environment has no self-hosted script).
vi.mock('../lib/mathjax', () => ({ typeset: vi.fn(async () => {}) }));

const typesetSpy = vi.mocked(typeset);

/**
 * Review option formulas come from the server as sanitized HTML: the answer controls must sit inside a typesetting target,
 * otherwise the TeX delimiters the HTML keeps would be shown literally.
 */
describe('ReviewView option typesetting', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    clearCardTypes();
    vi.restoreAllMocks();
    typesetSpy.mockClear();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  const card: DueCard = {
    card_id: 'c1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'choice_single',
    fields: { question: '公式？', options: ['raw-a', 'raw-b'] },
    tags: [],
    template: 'forward',
    version: 0,
  };

  it('typesets a container that holds the option HTML', async () => {
    setLocale('zh-CN');
    seedCardTypes();
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        front_html: '<p>公式？</p>',
        back_html: '<p>raw-a</p>',
        options_html: ['<p class="option-math">\\(x^2\\)</p>', '<p>raw-b</p>'],
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

    const typesetTargets = typesetSpy.mock.calls.flatMap(([elements]) => (elements ?? []) as Array<HTMLElement | null>);
    expect(typesetTargets.some((element) => element?.querySelector('.option-math'))).toBe(true);
  });
});

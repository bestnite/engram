import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownHtmlOnSide, postAnswerFromFrame } from './card-frame-fixture';

describe('ReviewView cloze alternatives presentation', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let restoreIframeStub: (() => void) | null = null;

  beforeEach(() => {
    const origSet = HTMLIFrameElement.prototype.setAttribute;
    const desc = Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype, 'src');

    HTMLIFrameElement.prototype.setAttribute = function (name: string, val: string) {
      if (name === 'src' && (val.includes('/card-frame') || val.includes('/edit-frame'))) {
        this.srcdoc = '<!doctype html><html><body></body></html>';
        return;
      }
      return origSet.call(this, name, val);
    };

    if (desc?.set) {
      Object.defineProperty(HTMLIFrameElement.prototype, 'src', {
        get: desc.get,
        set(val: string) {
          if (typeof val === 'string' && (val.includes('/card-frame') || val.includes('/edit-frame'))) {
            this.srcdoc = '<!doctype html><html><body></body></html>';
            return;
          }
          desc.set?.call(this, val);
        },
        configurable: true,
      });
    }

    restoreIframeStub = () => {
      HTMLIFrameElement.prototype.setAttribute = origSet;
      if (desc) Object.defineProperty(HTMLIFrameElement.prototype, 'src', desc);
    };
  });

  afterEach(() => {
    if (restoreIframeStub) restoreIframeStub();
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  const clozeCard: DueCard = {
    card_id: 'c_cloze',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    fields: { text: '{{c1::Paris}} is the capital.' },
    tags: [],
    template: 'cloze:1',
    version: 0,
    answer_control: 'cloze_blanks',
    machine_graded: true,
    field_order: ['text'],
  };

  it('does not leak alternative wordings before submit, and displays canonical + alternatives on back feedback', async () => {
    setLocale('zh-CN');
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'c_cloze',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: {
        verdict: 'correct',
        score: 1.0,
        rating: 3,
        answer_html: '<p>Paris</p>',
        given: 'City of Light',
        breakdown: [
          {
            answer_html: 'Paris / City of Light / Lutetia',
            correct: true,
            canonical: 'Paris',
            alternatives: ['City of Light', 'Lutetia'],
          },
        ],
      },
    });

    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [clozeCard] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c_cloze',
        ...faces(
          '<p><span class="cloze">[…]</span> is the capital.</p>',
          '<p>Paris is the capital.</p>'
        ),
        edit_href: '/decks/d1/notes/n1',
      }),
      revealGradedAnswer: vi.fn(),
      submitGradedReview,
    } as unknown as ApiClient;

    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();

    // 1. Verify before submit: front face has no alternatives
    const frontHtml = (await shownHtmlOnSide(target, 'question')).join('');
    expect(frontHtml).not.toContain('City of Light');
    expect(frontHtml).not.toContain('Lutetia');
    expect(target.querySelector('[data-testid="review-graded-alternatives"]')).toBeNull();

    // 2. Submit answer
    await postAnswerFromFrame(target, 'question', ['City of Light']);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();

    // 3. Verify after submit: shows "正文答案: Paris" and "也接受: City of Light / Lutetia"
    const breakdownItem = target.querySelector('[data-testid="review-graded-breakdown-item"]');
    expect(breakdownItem).not.toBeNull();
    const canonLabel = target.querySelector('[data-testid="review-graded-canonical-label"]');
    expect(canonLabel).not.toBeNull();
    expect(canonLabel!.textContent).toContain('正文答案');
    expect(breakdownItem!.textContent).toContain('Paris');

    const alternativesEl = target.querySelector('[data-testid="review-graded-alternatives"]');
    expect(alternativesEl).not.toBeNull();
    expect(alternativesEl!.textContent).toContain('也接受: City of Light / Lutetia');
  });

  it('preserves answer_html rendering for non-cloze items without canonical text', async () => {
    setLocale('zh-CN');
    const listCard: DueCard = {
      card_id: 'c_list',
      note_id: 'n2',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      fields: { items: 'one\ntwo' },
      tags: [],
      template: 'list',
      version: 0,
      answer_control: 'list',
      machine_graded: true,
      field_order: ['items'],
    };

    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'c_list',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: {
        verdict: 'correct',
        score: 1.0,
        rating: 3,
        answer_html: '<ol><li>One</li><li>Two</li></ol>',
        given: 'One, Two',
        breakdown: [
          {
            answer_html: '<strong>One</strong>',
            correct: true,
          },
        ],
      },
    });

    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [listCard] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c_list',
        ...faces('<p>List Question</p>', '<p>List Answer</p>'),
        edit_href: '/decks/d1/notes/n2',
      }),
      revealGradedAnswer: vi.fn(),
      submitGradedReview,
    } as unknown as ApiClient;

    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();

    await postAnswerFromFrame(target, 'question', ['One', 'Two']);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();

    const breakdownItem = target.querySelector('[data-testid="review-graded-breakdown-item"]');
    expect(breakdownItem).not.toBeNull();
    expect(breakdownItem!.innerHTML).toContain('<strong>One</strong>');
    expect(target.querySelector('[data-testid="review-graded-alternatives"]')).toBeNull();
  });
});

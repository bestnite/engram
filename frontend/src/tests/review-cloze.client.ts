import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * 挖空卡的逐空作答：输入框个数与提示来自 render 响应的 blanks，提交时按出现顺序
 * 把每空的作答作为字符串数组交给服务端判分；render 没给出空数时不提交。
 */
describe('ReviewView cloze blanks', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    clearCardTypes();
    vi.restoreAllMocks();
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
    kind: 'cloze',
    fields: { text: '{{c1::Paris}} and {{c1::Rome::city}}' },
    tags: [],
    template: 'cloze:1',
    version: 0,
  };

  function fakeClient(blanks: string[] | null) {
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'c1',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: { verdict: 'partial', score: 0.5, rating: 2, answer_html: '<p>Paris and Rome</p>', given: 'Paris, Milan' },
    });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        front_html: '<p><span class="cloze">[…]</span> and <span class="cloze">[city]</span></p>',
        back_html: '<p>Paris and Rome</p>',
        blanks,
        edit_href: '/decks/d1/notes/n1',
      }),
      getCardTypes: vi.fn(),
      revealGradedAnswer: vi.fn().mockResolvedValue({ revealed: true, card_id: 'c1', answer_html: '<p>Paris and Rome</p>' }),
      submitGradedReview,
    } as unknown as ApiClient;
    return { client, submitGradedReview };
  }

  async function setup(blanks: string[] | null) {
    setLocale('zh-CN');
    seedCardTypes();
    const fake = fakeClient(blanks);
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client: fake.client } });
    await flush();
    return { target, ...fake };
  }

  function blankInputs(target: HTMLElement): HTMLInputElement[] {
    return Array.from(target.querySelectorAll<HTMLInputElement>('[data-testid="review-graded-blank"]'));
  }

  it('renders one labelled input per blank with the hint as placeholder', async () => {
    const { target } = await setup(['', 'city']);
    const inputs = blankInputs(target);
    expect(inputs).toHaveLength(2);
    expect(inputs[0]!.placeholder).toBe('输入你的答案');
    expect(inputs[1]!.placeholder).toBe('city');
    expect(target.querySelector('[data-testid="review-graded-blanks"]')?.textContent).toContain('第 2 空');
  });

  it('submits the blanks in order as an array of strings', async () => {
    const { target, submitGradedReview } = await setup(['', 'city']);
    const inputs = blankInputs(target);
    inputs[0]!.value = 'Paris';
    inputs[0]!.dispatchEvent(new Event('input', { bubbles: true }));
    inputs[1]!.value = 'Milan';
    inputs[1]!.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview).toHaveBeenCalledTimes(1);
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ card_id: 'c1', answer: ['Paris', 'Milan'] });
    expect(target.querySelector('[data-testid="review-graded-verdict"]')).not.toBeNull();
  });

  it('keeps the prompt above the revealed answer, separated by one rule', async () => {
    const { target } = await setup(['', 'city']);
    (target.querySelector('[data-testid="review-graded-reveal"]') as HTMLElement).click();
    await flush();
    // 揭示答案后题干仍在上方，答案面板用自己的顶线与题干隔开；答案块本身不再画线，免得两条线叠在一起。
    expect(target.querySelector('[data-testid="review-graded-prompt"]')?.textContent).toContain('city');
    const panel = target.querySelector('[data-testid="review-graded-revealed"]') as HTMLElement;
    expect(panel.className).toContain('border-t');
    const answer = target.querySelector('[data-testid="review-graded-revealed-answer"]') as HTMLElement;
    expect(answer.textContent).toContain('Paris and Rome');
    expect(answer.className).not.toContain('border-t');
    expect(target.querySelector('[data-testid="review-graded-blanks"]')).toBeNull();
  });

  it('does not submit when the render response gave no blanks', async () => {
    const { target, submitGradedReview } = await setup(null);
    expect(blankInputs(target)).toHaveLength(0);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="review-graded-need-answer"]')).not.toBeNull();
  });
});

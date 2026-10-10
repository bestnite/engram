import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * 简答题先写再对照：翻面前有多行输入框；翻面后先给出自己写的作答、再给参考答案；
 * 打分时作答随自评提交。没写就不带 answer；没有参考答案时给出提示而不是空白。
 */
describe('ReviewView short answer', () => {
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
    card_id: 's1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'short_answer',
    fields: { prompt: '月相为什么会变化？', reference: '太阳照亮月球的一半。' },
    tags: [],
    template: 'forward',
    version: 0,
  };

  async function setup(backHTML: string) {
    setLocale('zh-CN');
    seedCardTypes();
    const submitSelfReview = vi.fn().mockResolvedValue({ card_id: 's1', state: 'learning', version: 1, remaining: 0, cards: [] });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({ card_id: 's1', front_html: '<p>月相为什么会变化？</p>', back_html: backHTML, blanks: null, edit_href: '' }),
      getCardTypes: vi.fn(),
      submitSelfReview,
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    return { target, submitSelfReview };
  }

  async function reveal(target: HTMLElement): Promise<void> {
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
  }

  it('shows the written answer above the reference and submits it with the rating', async () => {
    const { target, submitSelfReview } = await setup('<p>太阳照亮月球的一半。</p>');
    const input = target.querySelector('[data-testid="review-essay-input"]') as HTMLTextAreaElement;
    expect(input).not.toBeNull();
    input.value = '因为<b>月球</b>绕地球转';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    await reveal(target);
    expect(target.querySelector('[data-testid="review-essay-input"]')).toBeNull();
    const given = target.querySelector('[data-testid="review-essay-given"]');
    // 作答是纯文本：尖括号原样显示，不被当成 HTML。
    expect(given?.textContent).toContain('因为<b>月球</b>绕地球转');
    expect(given?.querySelector('b')).toBeNull();
    expect(target.querySelector('[data-testid="review-answer"]')?.textContent).toContain('太阳照亮月球的一半。');
    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
    expect(submitSelfReview.mock.calls[0]![0]).toMatchObject({ card_id: 's1', rating: 3, answer: '因为<b>月球</b>绕地球转' });
  });

  it('submits no answer when nothing was written', async () => {
    const { target, submitSelfReview } = await setup('<p>太阳照亮月球的一半。</p>');
    await reveal(target);
    expect(target.querySelector('[data-testid="review-essay-given"]')).toBeNull();
    (target.querySelector('[data-rating="2"]') as HTMLElement).click();
    await flush();
    expect(submitSelfReview.mock.calls[0]![0].answer).toBeUndefined();
  });

  it('says so when the card has no reference answer', async () => {
    card.fields = { prompt: '月相为什么会变化？' };
    const { target } = await setup('');
    await reveal(target);
    expect(target.querySelector('[data-testid="review-essay-no-reference"]')).not.toBeNull();
    card.fields = { prompt: '月相为什么会变化？', reference: '太阳照亮月球的一半。' };
  });
});

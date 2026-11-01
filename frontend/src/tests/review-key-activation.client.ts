import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';

/**
 * The review page listens for Space and Enter on the window to reveal or submit a card. A header control that has
 * focus must still activate itself with those keys, so the page may not take them for the card.
 */
describe('ReviewView header key activation', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    window.history.replaceState({}, '', '/');
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function card(id: string): DueCard {
    return {
      card_id: id,
      note_id: 'n1',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      fields: { front: 'Q', back: 'A' },
      tags: [],
      template: 'forward',
      version: 0,
      answer_control: null,
      machine_graded: false,
      field_order: ['front', 'back'],
    };
  }

  async function mountView(): Promise<HTMLElement> {
    setLocale('zh-CN');
    window.history.replaceState({}, '', '/review?deck=d1');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, {
      target,
      props: {
        client: {
          getDueCards: vi.fn().mockResolvedValue({ cards: [card('c1')] }),
          renderReviewCard: vi.fn().mockResolvedValue({
            card_id: 'c1',
            ...faces('<p>Q</p>', '<p>A</p>'),
            edit_href: '/decks/d1/notes/n1',
          }),
          submitSelfReview: vi.fn().mockResolvedValue({ cards: [card('c2')], remaining: 1, version: 1 }),
        } as unknown as ApiClient,
      },
    });
    await flush();
    return target;
  }

  function control(target: HTMLElement, testId: string): HTMLElement {
    const element = target.querySelector<HTMLElement>(`[data-testid="${testId}"]`);
    if (!element) throw new Error(`${testId} is not rendered`);
    return element;
  }

  /** Dispatches one key on the control and returns whether any listener swallowed its default. */
  function press(element: HTMLElement, key: string): boolean {
    const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true });
    element.dispatchEvent(event);
    return event.defaultPrevented;
  }

  function expectCardNotRevealed(target: HTMLElement): void {
    expect(target.querySelector('[data-testid="review-reveal"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="review-answer"]')).toBeNull();
  }

  it('leaves Space and Enter to the edit button', async () => {
    const target = await mountView();
    const edit = control(target, 'review-edit');

    expect(press(edit, ' ')).toBe(false);
    expectCardNotRevealed(target);
    expect(press(edit, 'Enter')).toBe(false);
    expectCardNotRevealed(target);
  });

  it('leaves Space and Enter to the exit link', async () => {
    const target = await mountView();
    const exit = control(target, 'review-exit');

    expect(press(exit, ' ')).toBe(false);
    expectCardNotRevealed(target);
    expect(press(exit, 'Enter')).toBe(false);
    expectCardNotRevealed(target);
  });

  it('leaves Space and Enter to the scroll-to-answer checkbox', async () => {
    const target = await mountView();
    const toggle = control(target, 'review-scroll-toggle');
    const before = toggle.getAttribute('aria-checked');

    press(toggle, ' ');
    await flush();
    expect(toggle.getAttribute('aria-checked')).not.toBe(before);
    expectCardNotRevealed(target);

    // Space is a checkbox's activation key; Enter leaves it unchanged but must still not reveal the card.
    press(toggle, 'Enter');
    await flush();
    expect(toggle.getAttribute('aria-checked')).not.toBe(before);
    expectCardNotRevealed(target);
  });

  it('leaves Space and Enter to the header undo entry', async () => {
    const target = await mountView();
    control(target, 'review-reveal').click();
    await flush();
    // The header's undo entry only exists after a submission, which also puts the next card back to unrevealed.
    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();

    const undo = control(target, 'review-undo-last');
    expect(press(undo, ' ')).toBe(false);
    expectCardNotRevealed(target);
    expect(press(undo, 'Enter')).toBe(false);
    expectCardNotRevealed(target);
  });
});

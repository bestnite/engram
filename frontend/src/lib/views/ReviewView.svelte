<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, DueCard, GradedAnswer, GradedFeedback } from '../api';
  import { typeset } from '../mathjax';
  import { reviewShortcut, reviewSwipe } from '../review-shortcuts';

  interface Props {
    client?: ApiClient;
    initialCards?: DueCard[];
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialFeedback?: GradedFeedback | null;
    initialRevealedAnswerHTML?: string;
    // 服务端清洗后的卡面 HTML 与编辑地址。生产路径由 $effect 从 render 端点取；这里保留
    // 初值是为了让无 DOM 的服务端渲染测试能钉住「只有清洗后的 HTML 进 HTML 汇」。
    initialFrontHTML?: string;
    initialBackHTML?: string;
    initialEditHref?: string;
    /** 自评卡是否已翻面（生产路径由 reveal() 翻转，测试用初值覆盖）。 */
    initialRevealed?: boolean;
  }

  let {
    client = apiClient,
    initialCards = [],
    initialLoading = true,
    initialError = null,
    initialFeedback = null,
    initialRevealedAnswerHTML = '',
    initialFrontHTML = '',
    initialBackHTML = '',
    initialEditHref = '',
    initialRevealed = false,
  }: Props = $props();

  // 自评类题型渲染四档按钮；作答类题型渲染输入控件并由服务端判分（DESIGN.md §8.2）。
  const SELF_ASSESSABLE = ['basic', 'basic_both', 'cloze', 'list', 'short_answer'];
  const GRADED_KINDS = ['typed', 'numeric', 'choice_single', 'choice_multi', 'true_false'];

  // svelte-ignore state_referenced_locally
  let cards = $state<DueCard[]>(initialCards);
  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let error = $state<unknown>(initialError);
  // svelte-ignore state_referenced_locally
  let revealed = $state(initialRevealed);
  let submitting = $state(false);
  let done = $state(0);
  // svelte-ignore state_referenced_locally
  let remaining = $state(initialCards.length);
  let startedAt = Date.now();

  // 作答类题型的作答与反馈状态；判分结果由服务端返回，客户端不选择档位。
  let answerText = $state('');
  let singleChoice = $state<number | null>(null);
  let multiChoice = $state<number[]>([]);
  let boolChoice = $state<boolean | null>(null);
  // svelte-ignore state_referenced_locally
  let feedback = $state<GradedFeedback | null>(initialFeedback);
  let pendingCards = $state<DueCard[]>([]);
  // svelte-ignore state_referenced_locally
  let revealedAnswerHTML = $state(initialRevealedAnswerHTML);
  // svelte-ignore state_referenced_locally
  let gradedRevealed = $state(initialRevealedAnswerHTML !== '');
  let needAnswer = $state(false);

  // 服务端清洗后的卡面 HTML（复习页唯一的 HTML 汇，DESIGN.md §6.1）。空串表示尚未取到，
  // 此时回退纯文本；绝不把 fields 原文当 Markdown 送进 HTML 汇。
  // svelte-ignore state_referenced_locally
  let frontHTML = $state(initialFrontHTML);
  // svelte-ignore state_referenced_locally
  let backHTML = $state(initialBackHTML);
  // svelte-ignore state_referenced_locally
  let editHref = $state(initialEditHref);
  let renderToken = 0;

  // 服务端清洗后返回的 HTML 容器：只对它们排版公式，绝不整页排版（DESIGN.md §6.1、§11）。
  let frontSection = $state<HTMLElement | null>(null);
  let answerSection = $state<HTMLElement | null>(null);
  let feedbackSection = $state<HTMLElement | null>(null);
  let revealedSection = $state<HTMLElement | null>(null);

  const current = $derived(cards[0] || null);
  const selfAssessable = $derived(current !== null && SELF_ASSESSABLE.includes(current.kind));
  const gradedKind = $derived(current !== null && GRADED_KINDS.includes(current.kind));
  const front = $derived(current ? cardSide(current, false) : []);
  const answer = $derived(current ? cardSide(current, true) : []);
  // 编辑入口：优先用服务端 render 返回的 edit_href；未取到时按规范路径拼接（DESIGN.md §8.1）。
  const fallbackEditHref = $derived(current ? `/decks/${current.deck_id}/notes/${current.note_id}` : '');

  function cardSide(card: DueCard, back: boolean): [string, string][] {
    const fields = card.fields || {};
    const reverse = card.template === 'reverse';
    const frontKey = card.kind === 'cloze' || card.kind === 'list' || card.kind === 'typed' || card.kind === 'numeric'
      ? (card.kind === 'cloze' ? 'text' : 'prompt')
      : card.kind === 'choice_single' || card.kind === 'choice_multi' ? 'question'
        : card.kind === 'true_false' ? 'statement' : (reverse ? 'back' : 'front');
    const answerKey = card.kind === 'cloze' ? 'text'
      : card.kind === 'list' ? 'items'
        : card.kind === 'typed' || card.kind === 'short_answer' ? (card.kind === 'typed' ? 'answer' : 'reference')
          : card.kind === 'numeric' ? 'value'
            : card.kind === 'choice_single' || card.kind === 'choice_multi' ? 'options'
              : card.kind === 'true_false' ? 'answer' : (reverse ? 'front' : 'back');
    const key = back ? answerKey : frontKey;
    const value = fields[key];
    return value === undefined ? [] : [[key, formatValue(value)]];
  }

  function formatValue(value: unknown): string {
    if (typeof value === 'string') return value;
    if (value === null || value === undefined) return '';
    if (typeof value === 'object') return JSON.stringify(value, null, 2);
    return String(value);
  }

  /** 作答类题型的题面文本：typed/numeric 用 prompt，choice_* 用 question，true_false 用 statement。 */
  function gradedPrompt(card: DueCard): string {
    const fields = card.fields || {};
    const key = card.kind === 'typed' || card.kind === 'numeric'
      ? 'prompt'
      : card.kind === 'true_false' ? 'statement' : 'question';
    const value = fields[key];
    return value === undefined || value === null ? '' : String(value);
  }

  /** 选择题的选项文本；只读 options，绝不把答案字段渲染进题面。 */
  function gradedOptions(card: DueCard): string[] {
    const raw = (card.fields || {}).options;
    return Array.isArray(raw) ? raw.map((option) => String(option)) : [];
  }

  function resetAnswerState(): void {
    answerText = '';
    singleChoice = null;
    multiChoice = [];
    boolChoice = null;
    needAnswer = false;
  }

  /** 组装当前卡片的原始作答；未作答的选择题返回 undefined（提交前拦截）。 */
  function currentAnswer(): GradedAnswer | undefined {
    switch (current?.kind) {
      case 'typed':
      case 'numeric':
        return answerText;
      case 'choice_single':
        return singleChoice ?? undefined;
      case 'choice_multi':
        return multiChoice;
      case 'true_false':
        return boolChoice ?? undefined;
      default:
        return undefined;
    }
  }

  function selectedDecks(): number[] | undefined {
    const search = new URLSearchParams(window.location.search);
    const values = search.getAll('deck').flatMap((raw) => raw.split(',')).filter(Boolean).map(Number);
    return values.length ? values : undefined;
  }

  /**
   * 取当前卡的服务端清洗 HTML（DESIGN.md §6.1）。token 防止换卡后旧请求覆盖新卡内容；
   * 失败只丢弃富文本并回退纯文本，绝不打断复习流程。
   */
  async function loadRender(card: DueCard, token: number): Promise<void> {
    try {
      const response = await client.renderReviewCard({ card_id: card.card_id, deck: selectedDecks() });
      if (token !== renderToken) return;
      frontHTML = response.front_html;
      backHTML = response.back_html;
      editHref = response.edit_href;
      await tick();
      await typeset([frontSection, answerSection]);
    } catch {
      if (token !== renderToken) return;
      frontHTML = '';
      backHTML = '';
      editHref = '';
    }
  }

  // 换卡即重取清洗 HTML；清空旧卡内容，避免在响应到达前显示上一张的富文本。
  $effect(() => {
    const card = current;
    const token = ++renderToken;
    frontHTML = '';
    backHTML = '';
    editHref = '';
    if (card) void loadRender(card, token);
  });

  async function loadQueue(): Promise<void> {
    loading = true;
    error = null;
    revealed = false;
    feedback = null;
    pendingCards = [];
    gradedRevealed = false;
    revealedAnswerHTML = '';
    resetAnswerState();
    try {
      const query = selectedDecks();
      const response = await client.getDueCards(query ? { deck: query, limit: 500 } : { limit: 500 });
      remaining = response.cards.length;
      cards = response.cards.slice(0, 1);
      startedAt = Date.now();
    } catch (cause) {
      error = cause;
    } finally {
      loading = false;
    }
  }

  async function rate(rating: number): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const elapsed = Math.max(0, Date.now() - startedAt);
      const response = await client.submitSelfReview({
        card_id: current.card_id,
        rating,
        expected_version: current.version,
        elapsed_ms: elapsed,
        deck: selectedDecks(),
      });
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      done += 1;
      revealed = false;
      startedAt = Date.now();
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /** 揭示自评卡的背面：先挂上 DOM 再只对背面容器排版公式。 */
  async function reveal(): Promise<void> {
    revealed = true;
    await tick();
    await typeset([answerSection]);
  }

  /** 提交原始作答，由服务端判分；结果面板保留已作答的卡片，用户点「继续」才换卡。 */
  async function submitGraded(): Promise<void> {
    if (!current || submitting) return;
    const answerValue = currentAnswer();
    if (answerValue === undefined) {
      needAnswer = true;
      return;
    }
    needAnswer = false;
    submitting = true;
    error = null;
    try {
      const response = await client.submitGradedReview({
        card_id: current.card_id,
        expected_version: current.version,
        elapsed_ms: Math.max(0, Date.now() - startedAt),
        deck: selectedDecks(),
        answer: answerValue,
      });
      pendingCards = response.cards;
      remaining = response.remaining;
      feedback = response.feedback ?? null;
      done += 1;
      // 等结果面板挂上 DOM，再只对它排版服务端返回的答案 HTML。
      await tick();
      await typeset([feedbackSection]);
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /** 请求揭示答案（只读）；揭示后输入控件禁用，只剩「记 0 分并继续」。 */
  async function revealGraded(): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await client.revealGradedAnswer({
        card_id: current.card_id,
        deck: selectedDecks(),
      });
      revealedAnswerHTML = response.answer_html;
      gradedRevealed = true;
      // 揭示面板挂上 DOM 后再排版揭示出的答案 HTML。
      await tick();
      await typeset([revealedSection]);
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /** 已揭示答案后放弃作答：服务端按 Again 记一条自评，不判分。 */
  async function giveUp(): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await client.submitGradedReview({
        card_id: current.card_id,
        expected_version: current.version,
        elapsed_ms: Math.max(0, Date.now() - startedAt),
        deck: selectedDecks(),
        action: 'give_up',
      });
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      done += 1;
      gradedRevealed = false;
      revealedAnswerHTML = '';
      resetAnswerState();
      startedAt = Date.now();
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /**
   * 埋藏当前卡（DESIGN.md §8.2）：只写本人进度，不产生 reviews 行；响应带同范围重建后的队列。
   * 服务端按 reader 判定，共享卡组读者可自行复习。
   */
  async function bury(): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await client.buryReview({ card_id: current.card_id, deck: selectedDecks() });
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      revealed = false;
      feedback = null;
      pendingCards = [];
      gradedRevealed = false;
      revealedAnswerHTML = '';
      resetAnswerState();
      startedAt = Date.now();
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /** 编辑入口：跳到服务端返回的 edit_href（缺省按规范路径拼接）。 */
  function gotoEdit(): void {
    const href = editHref || fallbackEditHref;
    if (href) window.location.assign(href);
  }

  /** 结果面板的「继续」：换到判分响应里预取的下一张卡，不产生额外写请求。 */
  function continueNext(): void {
    cards = pendingCards.slice(0, 1);
    pendingCards = [];
    feedback = null;
    gradedRevealed = false;
    revealedAnswerHTML = '';
    resetAnswerState();
    startedAt = Date.now();
  }

  let inputElement = $state<HTMLInputElement | null>(null);

  $effect(() => {
    if (current && gradedKind && !feedback && !gradedRevealed) {
      tick().then(() => {
        inputElement?.focus();
      });
    }
  });

  function verdictBadgeClass(verdict: GradedFeedback['verdict']): string {
    switch (verdict) {
      case 'correct':
        return 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-800';
      case 'partial':
        return 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:border-amber-800';
      default:
        return 'bg-rose-50 text-rose-700 border-rose-200 dark:bg-rose-950/40 dark:text-rose-300 dark:border-rose-800';
    }
  }

  function ratingClass(rating: number): string {
    switch (rating) {
      case 1:
        return 'bg-rose-50 text-rose-700 hover:bg-rose-100 border border-rose-200 dark:bg-rose-950/40 dark:text-rose-300 dark:border-rose-800/80';
      case 2:
        return 'bg-amber-50 text-amber-700 hover:bg-amber-100 border border-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:border-amber-800/80';
      case 3:
        return 'bg-emerald-600 hover:bg-emerald-700 text-white border border-emerald-600 shadow-sm shadow-emerald-500/20 dark:bg-emerald-600 dark:hover:bg-emerald-500 font-semibold ring-2 ring-emerald-500/30';
      case 4:
        return 'bg-sky-50 text-sky-700 hover:bg-sky-100 border border-sky-200 dark:bg-sky-950/40 dark:text-sky-300 dark:border-sky-800/80';
      default:
        return 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900';
    }
  }

  function isTypingTarget(target: EventTarget | null): boolean {
    return (
      target instanceof HTMLInputElement ||
      target instanceof HTMLTextAreaElement ||
      target instanceof HTMLSelectElement
    );
  }

  /** 空格/回车与滑动的共同动作：判分卡请求揭示答案，自评卡翻面。 */
  function applyReveal(): void {
    if (gradedKind) {
      if (!gradedRevealed) void revealGraded();
      return;
    }
    if (!revealed) void reveal();
  }

  function onKeydown(event: KeyboardEvent): void {
    // 键位映射是纯函数（review-shortcuts.ts），这里只把决策映射成副作用。
    const action = reviewShortcut(
      event.key,
      { alt: event.altKey, ctrl: event.ctrlKey, meta: event.metaKey },
      {
        typing: isTypingTarget(event.target),
        feedback: feedback !== null,
        gradedKind,
        gradedRevealed,
        revealed,
      },
    );
    switch (action.kind) {
      case 'ignore':
        return;
      case 'swallow':
        // 判分卡待作答时 1–4 失效，但仍吞掉按键，避免浏览器把数字当热键。
        event.preventDefault();
        return;
      case 'continue':
        event.preventDefault();
        continueNext();
        return;
      case 'reveal':
        event.preventDefault();
        applyReveal();
        return;
      case 'rate':
        event.preventDefault();
        void rate(action.rating);
        return;
      case 'bury':
        event.preventDefault();
        void bury();
        return;
      case 'edit':
        event.preventDefault();
        gotoEdit();
        return;
    }
  }

  // 手机滑动：落点在输入控件/按钮/链接上时不当作滑动（DESIGN.md §8.2）。
  let touchStartX = 0;
  let touchStartY = 0;
  let tracking = false;

  function interactiveTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) return false;
    const tag = target.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || tag === 'BUTTON' || tag === 'A';
  }

  function onTouchStart(event: TouchEvent): void {
    const touch = event.touches[0];
    if (event.touches.length !== 1 || !touch || interactiveTarget(event.target)) {
      tracking = false;
      return;
    }
    tracking = true;
    touchStartX = touch.clientX;
    touchStartY = touch.clientY;
  }

  function onTouchEnd(event: TouchEvent): void {
    const touch = event.changedTouches[0];
    if (!tracking || event.changedTouches.length !== 1 || !touch) {
      tracking = false;
      return;
    }
    tracking = false;
    const action = reviewSwipe(touch.clientX - touchStartX, touch.clientY - touchStartY, {
      feedback: feedback !== null,
      gradedKind,
      gradedRevealed,
      revealed,
    });
    if (action.kind === 'reveal') applyReveal();
    else if (action.kind === 'rate') void rate(action.rating);
  }

  onMount(() => {
    void loadQueue();
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });
</script>

<svelte:head><title>{$t('review.spa.title')} · {$t('app.name')}</title></svelte:head>
<section class="max-w-3xl mx-auto px-4 py-8 sm:py-12">
  <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('review.spa.title')}</h1>
    <div
      class="inline-flex items-center self-start sm:self-auto gap-2.5 px-3.5 py-1.5 rounded-full bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 shadow-xs text-xs font-medium text-zinc-600 dark:text-zinc-300"
      aria-live="polite"
    >
      <span class="inline-flex items-center gap-1.5">
        <span class="w-2 h-2 rounded-full bg-blue-500 animate-pulse"></span>
        <span>{$t('review.spa.remaining')}</span>
        <strong class="font-mono text-zinc-900 dark:text-zinc-100 text-sm">{remaining}</strong>
      </span>
      <span class="w-1 h-3 border-r border-zinc-200 dark:border-zinc-700"></span>
      <span class="text-zinc-500 dark:text-zinc-400">
        {$t('review.spa.done', { count: done })}
      </span>
    </div>
  </div>
  <div
    class="card-elevated min-h-72 p-6 sm:p-10 rounded-2xl flex flex-col justify-center touch-manipulation select-none"
    role="group"
    aria-label={$t('review.spa.title')}
    ontouchstart={onTouchStart}
    ontouchend={onTouchEnd}
  >
    {#if loading}
      <div data-testid="review-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400 space-y-3">
        <div class="inline-block animate-spin w-7 h-7 border-2 border-current border-t-transparent rounded-full" aria-hidden="true"></div>
        <p class="text-sm font-medium">{$t('review.spa.loading')}</p>
      </div>
    {:else if error}
      <div class="text-center space-y-4 py-6" data-testid="review-error">
        <p class="text-rose-700 dark:text-rose-300 font-medium">{error instanceof ApiClientError ? $t('error.' + (error.isConflict ? 'conflict' : error.isUnauthorized ? 'unauthorized' : error.isForbidden ? 'forbidden' : error.isNetworkError ? 'network' : 'unknown')) : $t('review.spa.failed')}</p>
        <button class="inline-flex items-center gap-2 px-4 py-2 min-h-10 rounded-lg border border-zinc-300 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 text-sm font-medium transition-colors cursor-pointer" onclick={() => void loadQueue()}>
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" /></svg>
          <span>{$t('common.retry')}</span>
        </button>
      </div>
    {:else if !current}
      <div class="text-center py-10" data-testid="review-empty">
        <p class="text-lg font-medium text-zinc-800 dark:text-zinc-200">{$t('review.spa.empty')}</p>
        <a class="inline-block mt-4 text-sm text-blue-600 dark:text-blue-400 hover:underline font-medium" href="/">{$t('review.spa.home')}</a>
      </div>
    {:else}
      <article data-testid="review-card" data-card-id={current.card_id} class="space-y-6">
        <div class="flex items-center justify-between pb-3 border-b border-zinc-100 dark:border-zinc-800/80">
          <span class="text-xs font-mono uppercase tracking-wider font-semibold text-zinc-500 dark:text-zinc-400 bg-zinc-100 dark:bg-zinc-800 px-2.5 py-0.5 rounded">
            {current.kind}
          </span>
          <button
            type="button"
            onclick={gotoEdit}
            data-testid="review-edit"
            class="text-xs text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 px-2.5 py-1 rounded-md transition-colors inline-flex items-center gap-1.5 cursor-pointer border border-zinc-200 dark:border-zinc-700/80"
          >
            <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>
            </svg>
            <span>{$t('review.spa.edit')}</span>
          </button>
        </div>

        {#if selfAssessable}
          <div bind:this={frontSection} class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed" data-testid="review-front">
            {#if frontHTML}
              {@html frontHTML}
            {:else}
              {#each front as [label, value] (label)}<div><span class="sr-only">{label}</span>{value}</div>{/each}
            {/if}
          </div>
          {#if revealed}
            <div bind:this={answerSection} class="border-t border-zinc-200 dark:border-zinc-700 pt-6 text-lg whitespace-pre-wrap break-words leading-relaxed" data-testid="review-answer">
              {#if backHTML}
                {@html backHTML}
              {:else}
                {#each answer as [label, value] (label)}<div><span class="text-xs text-zinc-500 block">{label}</span>{value}</div>{/each}
              {/if}
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 pt-2" aria-label={$t('review.spa.ratings')}>
              {#each [1, 2, 3, 4] as rating}
                <button
                  type="button"
                  disabled={submitting}
                  onclick={() => void rate(rating)}
                  class="min-h-12 py-2 px-3 rounded-xl font-medium transition-all btn-press disabled:opacity-50 cursor-pointer text-center {ratingClass(rating)}"
                  data-rating={rating}
                >
                  <span class="block text-sm font-semibold">{$t(`review.spa.rating.${rating}`)}</span>
                </button>
              {/each}
            </div>
          {:else}
            <button
              type="button"
              class="w-full min-h-12 rounded-xl bg-zinc-900 text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200 font-semibold transition-all btn-press shadow-sm cursor-pointer"
              onclick={() => void reveal()}
            >
              {$t('review.spa.show_answer')}
            </button>
          {/if}
        {:else if gradedKind}
          {#if feedback}
            <div bind:this={feedbackSection} class="space-y-5" data-testid="review-graded-result">
              <div class="flex items-center justify-between">
                <span class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-sm font-semibold border {verdictBadgeClass(feedback.verdict)}" data-testid="review-graded-verdict">
                  {#if feedback.verdict === 'correct'}
                    <svg class="w-4 h-4 text-emerald-600 dark:text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/>
                    </svg>
                  {:else if feedback.verdict === 'partial'}
                    <svg class="w-4 h-4 text-amber-600 dark:text-amber-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                      <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
                    </svg>
                  {:else}
                    <svg class="w-4 h-4 text-rose-600 dark:text-rose-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                      <circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/>
                    </svg>
                  {/if}
                  <span>{$t(`review.spa.graded.${feedback.verdict}`)}</span>
                </span>
                <span class="text-xs px-2.5 py-1 rounded-md bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-300 font-mono" data-testid="review-graded-score">
                  {$t('review.spa.graded.score')} · {Math.round(feedback.score * 100)}%
                </span>
              </div>
              <div class="border-t border-zinc-200 dark:border-zinc-700 pt-5">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.spa.graded.answer')}</div>
                <div class="text-lg whitespace-pre-wrap break-words font-medium" data-testid="review-graded-answer">{@html feedback.answer_html}</div>
              </div>
              {#if feedback.given}
                <div class="text-sm text-zinc-600 dark:text-zinc-300"><span class="text-xs text-zinc-500">{$t('review.spa.graded.given')}</span> · {feedback.given}</div>
              {/if}
              {#if feedback.parsed}
                <div class="text-sm text-zinc-600 dark:text-zinc-300"><span class="text-xs text-zinc-500">{$t('review.spa.graded.parsed')}</span> · {feedback.parsed}</div>
              {/if}
              <div class="text-sm text-zinc-500" data-testid="review-graded-rating">{$t('review.spa.graded.rating')} · {$t(`review.spa.rating.${feedback.rating}`)}</div>
              <button type="button" class="w-full min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-semibold hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer" onclick={continueNext} data-testid="review-graded-continue">{$t('review.spa.graded.continue')}</button>
            </div>
          {:else if gradedRevealed}
            <div bind:this={revealedSection} class="space-y-5" data-testid="review-graded-revealed">
              <div class="border-t border-zinc-200 dark:border-zinc-700 pt-5">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.spa.graded.answer')}</div>
                <div class="text-lg whitespace-pre-wrap break-words font-medium" data-testid="review-graded-answer">{@html revealedAnswerHTML}</div>
              </div>
              <button type="button" disabled={submitting} class="w-full min-h-12 rounded-xl border border-rose-300 dark:border-rose-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 font-medium disabled:opacity-50 transition-colors cursor-pointer" onclick={() => void giveUp()} data-testid="review-graded-give-up">{$t('review.spa.graded.give_up')}</button>
            </div>
          {:else}
            <div class="space-y-5">
              <div bind:this={frontSection} class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed" data-testid="review-graded-prompt">
                {#if frontHTML}{@html frontHTML}{:else}{gradedPrompt(current)}{/if}
              </div>
              {#if current.kind === 'typed' || current.kind === 'numeric'}
                <input
                  bind:this={inputElement}
                  type="text"
                  inputmode={current.kind === 'numeric' ? 'decimal' : undefined}
                  bind:value={answerText}
                  placeholder={current.kind === 'numeric' ? $t('review.spa.graded.placeholder_number') : $t('review.spa.graded.placeholder')}
                  aria-label={$t('review.spa.graded.placeholder')}
                  data-testid="review-graded-input"
                  class="w-full min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-4 text-lg"
                />
              {:else if current.kind === 'choice_single'}
                <div role="radiogroup" class="space-y-2" data-testid="review-graded-options">
                  {#each gradedOptions(current) as option, index (index)}
                    <label class="flex items-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 hover:border-zinc-400 dark:hover:border-zinc-600 px-4 transition-colors cursor-pointer">
                      <input type="radio" name="graded-single" value={index} bind:group={singleChoice} data-testid="review-graded-option" />
                      <span>{option}</span>
                    </label>
                  {/each}
                </div>
              {:else if current.kind === 'choice_multi'}
                <div class="space-y-2" data-testid="review-graded-options">
                  {#each gradedOptions(current) as option, index (index)}
                    <label class="flex items-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 hover:border-zinc-400 dark:hover:border-zinc-600 px-4 transition-colors cursor-pointer">
                      <input type="checkbox" value={index} bind:group={multiChoice} data-testid="review-graded-option" />
                      <span>{option}</span>
                    </label>
                  {/each}
                </div>
              {:else if current.kind === 'true_false'}
                <div role="radiogroup" class="grid grid-cols-2 gap-3" data-testid="review-graded-options">
                  <label class="flex items-center justify-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 hover:border-zinc-400 dark:hover:border-zinc-600 px-4 transition-colors cursor-pointer">
                    <input type="radio" name="graded-bool" value={true} bind:group={boolChoice} data-testid="review-graded-option" />
                    <span class="font-medium">{$t('review.spa.graded.true')}</span>
                  </label>
                  <label class="flex items-center justify-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 hover:border-zinc-400 dark:hover:border-zinc-600 px-4 transition-colors cursor-pointer">
                    <input type="radio" name="graded-bool" value={false} bind:group={boolChoice} data-testid="review-graded-option" />
                    <span class="font-medium">{$t('review.spa.graded.false')}</span>
                  </label>
                </div>
              {/if}
              {#if needAnswer}
                <p class="text-sm text-rose-700 dark:text-rose-300" data-testid="review-graded-need-answer">{$t('review.spa.graded.need_answer')}</p>
              {/if}
              <div class="flex flex-col sm:flex-row gap-3 pt-2">
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-semibold hover:bg-zinc-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void submitGraded()} data-testid="review-graded-submit">{$t('review.spa.graded.submit')}</button>
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 font-medium disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void revealGraded()} data-testid="review-graded-reveal">{$t('review.spa.graded.show_answer')}</button>
              </div>
            </div>
          {/if}
        {/if}

        {#if !feedback}
          <div class="pt-4 border-t border-zinc-100 dark:border-zinc-800/80 flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs text-zinc-500 dark:text-zinc-400">
            <button
              type="button"
              disabled={submitting}
              onclick={() => void bury()}
              class="self-start px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700/80 hover:bg-zinc-100 dark:hover:bg-zinc-800 text-zinc-600 dark:text-zinc-300 font-medium disabled:opacity-50 transition-colors cursor-pointer"
              data-testid="review-bury"
            >
              {$t('review.spa.bury')}
            </button>
            <p class="text-xs text-zinc-400 dark:text-zinc-500" data-testid="review-bury-hint">
              {$t('review.spa.bury_hint')}
            </p>
          </div>
        {/if}
      </article>
    {/if}
  </div>
  <p class="mt-4 text-center text-xs text-zinc-500 dark:text-zinc-400">
    {feedback ? $t('review.spa.shortcuts_result') : gradedKind ? (gradedRevealed ? $t('review.spa.shortcuts_result') : $t('review.spa.shortcuts_graded')) : revealed ? $t('review.spa.shortcuts') : $t('review.spa.shortcuts_reveal')}
  </p>
</section>

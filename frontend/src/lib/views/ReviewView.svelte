<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, DueCard, GradedAnswer, GradedFeedback } from '../api';
  import { typeset } from '../mathjax';

  interface Props {
    client?: ApiClient;
    initialCards?: DueCard[];
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialFeedback?: GradedFeedback | null;
    initialRevealedAnswerHTML?: string;
  }

  let {
    client = apiClient,
    initialCards = [],
    initialLoading = true,
    initialError = null,
    initialFeedback = null,
    initialRevealedAnswerHTML = '',
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
  let revealed = $state(false);
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
  // 服务端清洗后返回的 HTML 容器（判分反馈答案 / 揭示答案）：只对它们排版公式，
  // 绝不整页排版，也绝不把字段原文送进 HTML sink（DESIGN.md §6.1、§11）。
  let feedbackSection = $state<HTMLElement | null>(null);
  let revealedSection = $state<HTMLElement | null>(null);

  const current = $derived(cards[0] || null);
  const selfAssessable = $derived(current !== null && SELF_ASSESSABLE.includes(current.kind));
  const gradedKind = $derived(current !== null && GRADED_KINDS.includes(current.kind));
  const front = $derived(current ? cardSide(current, false) : []);
  const answer = $derived(current ? cardSide(current, true) : []);

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

  function verdictClass(verdict: GradedFeedback['verdict']): string {
    switch (verdict) {
      case 'correct':
        return 'text-emerald-700 dark:text-emerald-300';
      case 'partial':
        return 'text-amber-700 dark:text-amber-300';
      default:
        return 'text-rose-700 dark:text-rose-300';
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.altKey || event.ctrlKey || event.metaKey || event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement) return;
    // 结果面板：回车/空格/1–4 都走「继续」。
    if (feedback) {
      if (event.key === ' ' || event.key === 'Enter' || /^[1-4]$/.test(event.key)) {
        event.preventDefault();
        continueNext();
      }
      return;
    }
    // 判分卡待作答：1–4 完全失效（不评分、不揭示），显示答案仍由空格/回车承担。
    if (gradedKind) {
      if (event.key === ' ' || event.key === 'Enter') {
        event.preventDefault();
        if (!gradedRevealed) void revealGraded();
      }
      return;
    }
    if (event.key === ' ' || event.key === 'Enter') {
      event.preventDefault();
      if (!revealed) revealed = true;
      return;
    }
    if (revealed && /^[1-4]$/.test(event.key)) void rate(Number(event.key));
  }

  onMount(() => {
    void loadQueue();
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });
</script>

<svelte:head><title>{$t('review.spa.title')} · {$t('app.name')}</title></svelte:head>
<section class="max-w-3xl mx-auto px-4 py-8 sm:py-12">
  <div class="flex items-center justify-between mb-5 text-sm text-zinc-500 dark:text-zinc-400">
    <h1 class="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">{$t('review.spa.title')}</h1>
    <div aria-live="polite"><span>{remaining}</span> · {$t('review.spa.done', { count: done })}</div>
  </div>
  <div class="card-elevated min-h-72 p-6 sm:p-10 rounded-2xl flex flex-col justify-center">
    {#if loading}
      <p class="text-center text-zinc-500" data-testid="review-loading">{$t('review.spa.loading')}</p>
    {:else if error}
      <div class="text-center space-y-4" data-testid="review-error">
        <p class="text-rose-700 dark:text-rose-300">{error instanceof ApiClientError ? $t('error.' + (error.isConflict ? 'conflict' : error.isUnauthorized ? 'unauthorized' : error.isForbidden ? 'forbidden' : error.isNetworkError ? 'network' : 'unknown')) : $t('review.spa.failed')}</p>
        <button class="px-4 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700" onclick={() => void loadQueue()}>{$t('common.retry')}</button>
      </div>
    {:else if !current}
      <div class="text-center" data-testid="review-empty"><p class="text-lg font-medium">{$t('review.spa.empty')}</p><a class="inline-block mt-4 underline" href="/">{$t('review.spa.home')}</a></div>
    {:else}
      <article data-testid="review-card" data-card-id={current.card_id} class="space-y-8">
        <div class="text-xs uppercase tracking-wide text-zinc-500">{current.kind}</div>
        {#if selfAssessable}
          <div class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words" data-testid="review-front">
            {#each front as [label, value] (label)}<div><span class="sr-only">{label}</span>{value}</div>{/each}
          </div>
          {#if revealed}
            <div class="border-t border-zinc-200 dark:border-zinc-700 pt-6 text-lg whitespace-pre-wrap break-words" data-testid="review-answer">
              {#each answer as [label, value] (label)}<div><span class="text-xs text-zinc-500 block">{label}</span>{value}</div>{/each}
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3" aria-label={$t('review.spa.ratings')}>
              {#each [1, 2, 3, 4] as rating}
                <button type="button" disabled={submitting} onclick={() => void rate(rating)} class="min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-medium disabled:opacity-50" data-rating={rating}>
                  <span class="block">{rating} · {$t(`review.spa.rating.${rating}`)}</span>
                </button>
              {/each}
            </div>
          {:else}
            <button type="button" class="w-full min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 font-medium" onclick={() => revealed = true}>{$t('review.spa.show_answer')}</button>
          {/if}
        {:else if gradedKind}
          {#if feedback}
            <div bind:this={feedbackSection} class="space-y-5" data-testid="review-graded-result">
              <div class="flex items-center justify-between">
                <span class={verdictClass(feedback.verdict)} data-testid="review-graded-verdict">{$t(`review.spa.graded.${feedback.verdict}`)}</span>
                <span class="text-sm text-zinc-500" data-testid="review-graded-score">{$t('review.spa.graded.score')} · {Math.round(feedback.score * 100)}%</span>
              </div>
              <div class="border-t border-zinc-200 dark:border-zinc-700 pt-5">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.spa.graded.answer')}</div>
                <div class="text-lg whitespace-pre-wrap break-words" data-testid="review-graded-answer">{@html feedback.answer_html}</div>
              </div>
              {#if feedback.given}
                <div class="text-sm text-zinc-600 dark:text-zinc-300"><span class="text-xs text-zinc-500">{$t('review.spa.graded.given')}</span> · {feedback.given}</div>
              {/if}
              {#if feedback.parsed}
                <div class="text-sm text-zinc-600 dark:text-zinc-300"><span class="text-xs text-zinc-500">{$t('review.spa.graded.parsed')}</span> · {feedback.parsed}</div>
              {/if}
              <div class="text-sm text-zinc-500" data-testid="review-graded-rating">{$t('review.spa.graded.rating')} · {$t(`review.spa.rating.${feedback.rating}`)}</div>
              <button type="button" class="w-full min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-medium" onclick={continueNext} data-testid="review-graded-continue">{$t('review.spa.graded.continue')}</button>
            </div>
          {:else if gradedRevealed}
            <div bind:this={revealedSection} class="space-y-5" data-testid="review-graded-revealed">
              <div class="border-t border-zinc-200 dark:border-zinc-700 pt-5">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.spa.graded.answer')}</div>
                <div class="text-lg whitespace-pre-wrap break-words" data-testid="review-graded-answer">{@html revealedAnswerHTML}</div>
              </div>
              <button type="button" disabled={submitting} class="w-full min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 font-medium disabled:opacity-50" onclick={() => void giveUp()} data-testid="review-graded-give-up">{$t('review.spa.graded.give_up')}</button>
            </div>
          {:else}
            <div class="space-y-5">
              <div class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words" data-testid="review-graded-prompt">{gradedPrompt(current)}</div>
              {#if current.kind === 'typed' || current.kind === 'numeric'}
                <input
                  type="text"
                  inputmode={current.kind === 'numeric' ? 'decimal' : undefined}
                  bind:value={answerText}
                  placeholder={current.kind === 'numeric' ? $t('review.spa.graded.placeholder_number') : $t('review.spa.graded.placeholder')}
                  aria-label={$t('review.spa.graded.placeholder')}
                  data-testid="review-graded-input"
                  class="w-full min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 bg-transparent px-4 text-lg"
                />
              {:else if current.kind === 'choice_single'}
                <div role="radiogroup" class="space-y-2" data-testid="review-graded-options">
                  {#each gradedOptions(current) as option, index (index)}
                    <label class="flex items-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 px-4">
                      <input type="radio" name="graded-single" value={index} bind:group={singleChoice} data-testid="review-graded-option" />
                      <span>{option}</span>
                    </label>
                  {/each}
                </div>
              {:else if current.kind === 'choice_multi'}
                <div class="space-y-2" data-testid="review-graded-options">
                  {#each gradedOptions(current) as option, index (index)}
                    <label class="flex items-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 px-4">
                      <input type="checkbox" value={index} bind:group={multiChoice} data-testid="review-graded-option" />
                      <span>{option}</span>
                    </label>
                  {/each}
                </div>
              {:else if current.kind === 'true_false'}
                <div role="radiogroup" class="grid grid-cols-2 gap-3" data-testid="review-graded-options">
                  <label class="flex items-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 px-4">
                    <input type="radio" name="graded-bool" value={true} bind:group={boolChoice} data-testid="review-graded-option" />
                    <span>{$t('review.spa.graded.true')}</span>
                  </label>
                  <label class="flex items-center gap-3 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 px-4">
                    <input type="radio" name="graded-bool" value={false} bind:group={boolChoice} data-testid="review-graded-option" />
                    <span>{$t('review.spa.graded.false')}</span>
                  </label>
                </div>
              {/if}
              {#if needAnswer}
                <p class="text-sm text-rose-700 dark:text-rose-300" data-testid="review-graded-need-answer">{$t('review.spa.graded.need_answer')}</p>
              {/if}
              <div class="flex flex-col sm:flex-row gap-3">
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-medium disabled:opacity-50" onclick={() => void submitGraded()} data-testid="review-graded-submit">{$t('review.spa.graded.submit')}</button>
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl border border-zinc-300 dark:border-zinc-700 font-medium disabled:opacity-50" onclick={() => void revealGraded()} data-testid="review-graded-reveal">{$t('review.spa.graded.show_answer')}</button>
              </div>
            </div>
          {/if}
        {/if}
      </article>
    {/if}
  </div>
  <p class="mt-4 text-center text-xs text-zinc-500 dark:text-zinc-400">{feedback ? $t('review.spa.shortcuts_result') : gradedKind ? $t('review.spa.shortcuts_graded') : $t('review.spa.shortcuts')}</p>
</section>

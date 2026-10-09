<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount, tick } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, DueCard, GradedAnswer, GradedFeedback } from '../api';
  import { typeset } from '../mathjax';
  import { reviewShortcut, reviewSwipe } from '../review-shortcuts';
  import RadioGroup from '../components/ui/RadioGroup.svelte';

  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import { noteKindLabel as kindLabel } from '../labels';
  import { cardTypes, descriptionOf, loadCardTypes, type CardTypeCatalog } from '../card-types';
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
    /** 最近一次提交的撤销记录（生产路径由提交写入，测试用初值覆盖）。 */
    initialLastUndo?: { cardId: string; version: number; rating: number } | null;
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
    initialLastUndo = null,
  }: Props = $props();

  // 自评类题型渲染四档按钮；作答类题型渲染输入控件并由服务端判分。
  // 哪些题型属于哪一类由服务端的自描述决定（graded / answer_control），前端不再维护清单。

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
  // 本次会话最近一次提交（卡 id、提交后的状态版本、档位）：复习页据此提供「撤销上一次评分」。
  // 自评题提交后画面立刻翻到下一张、没有结果面板，撤销只能靠这个记录；判分题也用同一记录，
  // 面板按钮与头部入口共用它，避免两套状态。
  // svelte-ignore state_referenced_locally
  let lastUndo = $state<{ cardId: string; version: number; rating: number } | null>(initialLastUndo);
  // svelte-ignore state_referenced_locally
  let revealedAnswerHTML = $state(initialRevealedAnswerHTML);
  // svelte-ignore state_referenced_locally
  let gradedRevealed = $state(initialRevealedAnswerHTML !== '');
  let needAnswer = $state(false);

  // 服务端清洗后的卡面 HTML（复习页唯一的 HTML 汇）。空串表示尚未取到，
  // 此时回退纯文本；绝不把 fields 原文当 Markdown 送进 HTML 汇。
  // svelte-ignore state_referenced_locally
  let frontHTML = $state(initialFrontHTML);
  // svelte-ignore state_referenced_locally
  let backHTML = $state(initialBackHTML);
  // svelte-ignore state_referenced_locally
  let editHref = $state(initialEditHref);
  let renderToken = 0;

  // 服务端清洗后返回的 HTML 容器：只对它们排版公式，绝不整页排版。
  let frontSection = $state<HTMLElement | null>(null);
  let answerSection = $state<HTMLElement | null>(null);
  let feedbackSection = $state<HTMLElement | null>(null);
  let revealedSection = $state<HTMLElement | null>(null);

  const current = $derived(cards[0] || null);
  // 当前卡的题型自描述；元数据未就绪或题型未知时为 null，视图渲染加载态而不是乱猜字段。
  const desc = $derived(descriptionOf($cardTypes, current?.kind));
  const selfAssessable = $derived(desc !== null && !desc.graded);
  const gradedKind = $derived(desc !== null && desc.graded);
  const answerControl = $derived(desc?.answer_control ?? 'none');
  const front = $derived(current && $cardTypes ? cardSide($cardTypes, current, false) : []);
  const answer = $derived(current && $cardTypes ? cardSide($cardTypes, current, true) : []);
  // 编辑入口：优先用服务端 render 返回的 edit_href；未取到时按规范路径拼接。
  const fallbackEditHref = $derived(current ? `/decks/${current.deck_id}/notes/${current.note_id}` : '');

  /**
   * 卡面的正/反面字段：正反面字段名来自元数据（front_field / back_field）。
   * 模板级反转（basic_both 的 reverse）在这里把正反面互换——元数据描述的是正向映射，
   * 不编码模板级反转。
   */
  function cardSide(
    catalog: CardTypeCatalog,
    card: DueCard,
    back: boolean
  ): [string, string][] {
    const description = descriptionOf(catalog, card.kind);
    if (!description) return [];
    const fields = card.fields || {};
    const reverse = card.template === 'reverse';
    const frontKey = reverse ? description.back_field : description.front_field;
    const answerKey = reverse ? description.front_field : description.back_field;
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

  /** 作答类题型的题面文本：字段名来自元数据的 prompt_field。 */
  function gradedPrompt(catalog: CardTypeCatalog | null, card: DueCard): string {
    const description = descriptionOf(catalog, card.kind);
    if (!description || !description.prompt_field) return '';
    const value = (card.fields || {})[description.prompt_field];
    return value === undefined || value === null ? '' : String(value);
  }

  /** 选择题的选项文本：字段名来自元数据的 options_field；只读选项，绝不渲染答案字段。 */
  function gradedOptions(catalog: CardTypeCatalog | null, card: DueCard): string[] {
    const description = descriptionOf(catalog, card.kind);
    const raw = description && description.options_field ? (card.fields || {})[description.options_field] : undefined;
    return Array.isArray(raw) ? raw.map((option) => String(option)) : [];
  }

  function resetAnswerState(): void {
    answerText = '';
    singleChoice = null;
    multiChoice = [];
    boolChoice = null;
    needAnswer = false;
  }

  /** 组装当前卡片的原始作答；未作答的选择题返回 undefined（提交前拦截）。控件类型来自元数据。 */
  function currentAnswer(): GradedAnswer | undefined {
    switch (answerControl) {
      case 'text':
      case 'number':
        return answerText;
      case 'single':
        return singleChoice ?? undefined;
      case 'multi':
        return multiChoice;
      case 'bool':
        return boolChoice ?? undefined;
      default:
        return undefined;
    }
  }

  function selectedDecks(): string[] | undefined {
    const search = new URLSearchParams(window.location.search);
    const values = search.getAll('deck').flatMap((raw) => raw.split(',')).filter(Boolean);
    return values.length ? values : undefined;
  }

  /**
   * 取当前卡的服务端清洗 HTML。token 防止换卡后旧请求覆盖新卡内容；
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
    lastUndo = null;
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
      const ratedCardId = current.card_id;
      const response = await client.submitSelfReview({
        card_id: ratedCardId,
        rating,
        expected_version: current.version,
        elapsed_ms: elapsed,
        deck: selectedDecks(),
      });
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      done += 1;
      // 自评后画面已翻到下一张：记住这次提交，头部「撤销上一次评分」据此可用。
      lastUndo = { cardId: ratedCardId, version: response.version, rating };
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
      lastUndo = { cardId: current.card_id, version: response.version, rating: response.feedback?.rating ?? 0 };
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
      // 放弃作答按 Again 记一条自评日志；撤销入口同样可用。
      lastUndo = { cardId: current.card_id, version: response.version, rating: 1 };
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
   * 埋藏当前卡：只写本人进度，不产生 reviews 行；响应带同范围重建后的队列。
   * 服务端按 reader 判定，共享卡组读者可自行复习。
   */
  async function bury(): Promise<void> {
    await skipCurrent((cardId) => client.buryReview({ card_id: cardId, deck: selectedDecks() }));
  }

  /**
   * 暂停当前卡：只对本人生效（共享卡组的其他人不受影响），在卡组的笔记列表里可以取消暂停。
   */
  async function suspend(): Promise<void> {
    await skipCurrent((cardId) => client.suspendReview({ card_id: cardId, deck: selectedDecks() }));
  }

  /** 埋藏与暂停共用：请求成功后换成服务端重建的队列，并重置本卡的作答状态。 */
  async function skipCurrent(send: (cardId: string) => Promise<{ cards: DueCard[]; remaining: number }>): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await send(current.card_id);
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

  /**
   * 结果面板的「继续」：换到判分响应里预取的下一张卡，不产生额外写请求。
   * 故意不清 lastUndo：离开面板后头部的「撤销上一次评分」仍然可用（撤销刚判的那张）。
   */
  function continueNext(): void {
    cards = pendingCards.slice(0, 1);
    pendingCards = [];
    feedback = null;
    gradedRevealed = false;
    revealedAnswerHTML = '';
    resetAnswerState();
    startedAt = Date.now();
  }

  /**
   * 头部撤销按钮的文案：带上被撤销的档位。自评题提交后画面已翻到下一张，只说「撤销」会让人
   * 以为撤的是当前这张卡。
   */
  function undoLabel(): string {
    const rating = lastUndo?.rating ?? 0;
    if (rating >= 1 && rating <= 4) {
      return $t('review.undo_last_rating', { rating: $t(`review.rating.${rating}`) });
    }
    return $t('review.undo_last');
  }

  /**
   * 撤销上一次提交的评分（自评题与判分题共用这一个入口）：服务端恢复进度并删除那条复习日志，
   * 响应带同范围重建的队列。撤销后把被撤销的卡放回当前位——队列按 due_at 排序，它不保证排在
   * 首位，因此按 undone_card_id 在队列里找它。
   *
   * 目标是 lastUndo 记下的那张卡，而不是当前正在看的卡：自评提交后画面已经翻到下一张。
   */
  async function undo(): Promise<void> {
    const target = lastUndo;
    if (!target || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await client.undoReview({
        card_id: target.cardId,
        deck: selectedDecks(),
        expected_version: target.version,
      });
      const restored = response.cards.find((card) => card.card_id === response.undone_card_id);
      if (restored) cards = [restored];
      remaining = response.remaining;
      done = Math.max(0, done - 1);
      lastUndo = null;
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

  let inputElement = $state<HTMLInputElement | null>(null);

  $effect(() => {
    if (current && gradedKind && !feedback && !gradedRevealed) {
      tick().then(() => {
        inputElement?.focus();
      });
    }
  });

  function verdictVariant(verdict: GradedFeedback['verdict']): 'success' | 'warning' | 'danger' {
    switch (verdict) {
      case 'correct':
        return 'success';
      case 'partial':
        return 'warning';
      default:
        return 'danger';
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

  /** 焦点是否在判分卡的选项（bits-ui 渲染为 role=radio/checkbox 的按钮）上。 */
  function isChoiceTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) return false;
    const role = target.getAttribute('role');
    return role === 'radio' || role === 'checkbox';
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
        choosing: isChoiceTarget(event.target),
        // keyCode 229 是部分浏览器在组字期间不设 isComposing 时的兜底信号。
        composing: event.isComposing || event.keyCode === 229,
        repeat: event.repeat,
        feedback: feedback !== null,
        gradedKind,
        gradedRevealed,
        revealed,
        canUndo: lastUndo !== null,
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
      case 'submit':
        event.preventDefault();
        void submitGraded();
        return;
      case 'give_up':
        event.preventDefault();
        void giveUp();
        return;
      case 'rate':
        event.preventDefault();
        void rate(action.rating);
        return;
      case 'bury':
        event.preventDefault();
        void bury();
        return;
      case 'suspend':
        event.preventDefault();
        void suspend();
        return;
      case 'edit':
        event.preventDefault();
        gotoEdit();
        return;
      case 'undo':
        event.preventDefault();
        void undo();
        return;
    }
  }

  // 手机滑动：落点在输入控件/按钮/链接上时不当作滑动。
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
    // 元数据只加载一次（模块内幂等）；失败时静默降级为加载/空态，不打断复习流程。
    void loadCardTypes(client).catch(() => {});
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });
</script>

<svelte:head><title>{$t('review.title')} · {$t('app.name')}</title></svelte:head>
<Page as="section">
  <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
    <h1 class="text-2xl font-semibold tracking-tight text-foreground">{$t('review.title')}</h1>
    <div class="flex items-center gap-2 self-start sm:self-auto">
      <!-- 撤销上一次评分：自评题提交后没有结果面板，入口在这里；判分题面板打开时隐藏，避免两个入口。 -->
      {#if lastUndo && !feedback}
        <Button variant="outline" size="sm" testId="review-undo-last" disabled={submitting} onclick={() => void undo()}>
          {undoLabel()}
        </Button>
      {/if}
      <div
        class="inline-flex items-center gap-2.5 px-3.5 py-1.5 rounded-full bg-white dark:bg-zinc-900 border border-border shadow-xs text-xs font-medium text-muted-foreground"
        aria-live="polite"
      >
      <span class="inline-flex items-center gap-1.5">
        <span class="w-2 h-2 rounded-full bg-blue-500 animate-pulse"></span>
        <span>{$t('review.remaining')}</span>
        <strong class="font-mono text-foreground text-sm">{remaining}</strong>
      </span>
      <span class="w-1 h-3 border-r border-input"></span>
      <span class="text-muted-foreground">
        {$t('review.done', { count: done })}
      </span>
      </div>
    </div>
  </div>
  <div
    class="card-elevated min-h-72 p-6 sm:p-10 rounded-2xl flex flex-col justify-center touch-manipulation select-none"
    role="group"
    aria-label={$t('review.title')}
    ontouchstart={onTouchStart}
    ontouchend={onTouchEnd}
  >
    {#if loading}
      <Skeleton testId="review-loading" label={$t('review.loading')} lines={2} />
    {:else if error}
      <div class="text-center space-y-4 py-6" data-testid="review-error">
        <p class="text-rose-700 dark:text-rose-300 font-medium">{error instanceof ApiClientError ? $t('error.' + (error.isConflict ? 'conflict' : error.isUnauthorized ? 'unauthorized' : error.isForbidden ? 'forbidden' : error.isNetworkError ? 'network' : 'unknown')) : $t('review.failed')}</p>
        <button class="inline-flex items-center gap-2 px-4 py-2 min-h-10 rounded-lg border border-input hover:bg-muted text-sm font-medium transition-colors cursor-pointer" onclick={() => void loadQueue()}>
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" /></svg>
          <span>{$t('common.retry')}</span>
        </button>
      </div>
    {:else if !current}
      <div class="text-center py-10" data-testid="review-empty">
        <p class="text-lg font-medium text-foreground/90">{$t('review.empty')}</p>
        <a class="inline-block mt-4 text-sm text-blue-600 dark:text-blue-400 hover:underline font-medium" href="/">{$t('review.home')}</a>
      </div>
    {:else}
      <article data-testid="review-card" data-card-id={current.card_id} class="space-y-6">
        <div class="flex items-center justify-between pb-3 border-b border-border">
          <Badge class="px-2.5 font-semibold">{kindLabel(current.kind, $cardTypes, $t)}</Badge>
          <button
            type="button"
            onclick={gotoEdit}
            data-testid="review-edit"
            class="text-xs text-muted-foreground hover:text-foreground hover:bg-muted px-2.5 py-1 rounded-md transition-colors inline-flex items-center gap-1.5 cursor-pointer border border-input"
          >
            <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>
            </svg>
            <span>{$t('review.edit')}</span>
          </button>
        </div>

        {#if !desc}
          <!-- 元数据未就绪（加载中或拉取失败）：渲染加载态，不猜测字段、不打印机器词汇。 -->
          <Skeleton testId="review-meta-loading" label={$t('common.loading')} lines={3} />
        {:else if selfAssessable}
          <div bind:this={frontSection} class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed" data-testid="review-front">
            {#if frontHTML}
              {@html frontHTML}
            {:else}
              {#each front as [label, value] (label)}<div><span class="sr-only">{label}</span>{value}</div>{/each}
            {/if}
          </div>
          {#if revealed}
            <div bind:this={answerSection} class="border-t border-input pt-6 text-lg whitespace-pre-wrap break-words leading-relaxed" data-testid="review-answer">
              {#if backHTML}
                {@html backHTML}
              {:else}
                {#each answer as [label, value] (label)}<div><span class="text-xs text-zinc-500 block">{label}</span>{value}</div>{/each}
              {/if}
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 pt-2" aria-label={$t('review.ratings')}>
              {#each [1, 2, 3, 4] as rating}
                <button
                  type="button"
                  disabled={submitting}
                  onclick={() => void rate(rating)}
                  class="min-h-12 py-2 px-3 rounded-xl font-medium transition-all btn-press disabled:opacity-50 cursor-pointer text-center {ratingClass(rating)}"
                  data-rating={rating}
                >
                  <span class="block text-sm font-semibold">{$t(`review.rating.${rating}`)}</span>
                </button>
              {/each}
            </div>
          {:else}
            <button
              type="button"
              class="w-full min-h-12 rounded-xl bg-zinc-900 text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200 font-semibold transition-all btn-press shadow-sm cursor-pointer"
              onclick={() => void reveal()}
            >
              {$t('review.show_answer')}
            </button>
          {/if}
        {:else if gradedKind}
          {#if feedback}
            <div bind:this={feedbackSection} class="space-y-5" data-testid="review-graded-result">
              <div class="flex items-center justify-between">
                <Badge variant={verdictVariant(feedback.verdict)} class="gap-1.5 px-3 py-1 text-sm font-semibold" testId="review-graded-verdict">
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
                  <span>{$t(`review.graded.${feedback.verdict}`)}</span>
                </Badge>
                <Badge testId="review-graded-score" class="px-2.5 py-1 font-mono">
                  {$t('review.graded.score')} · {Math.round(feedback.score * 100)}%
                </Badge>
              </div>
              <div class="border-t border-input pt-5">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.answer')}</div>
                <div class="text-lg whitespace-pre-wrap break-words font-medium" data-testid="review-graded-answer">{@html feedback.answer_html}</div>
              </div>
              {#if feedback.given}
                <div class="text-sm text-muted-foreground"><span class="text-xs text-zinc-500">{$t('review.graded.given')}</span> · {feedback.given}</div>
              {/if}
              {#if feedback.parsed}
                <div class="text-sm text-muted-foreground"><span class="text-xs text-zinc-500">{$t('review.graded.parsed')}</span> · {feedback.parsed}</div>
              {/if}
              <div class="text-sm text-zinc-500" data-testid="review-graded-rating">{$t('review.graded.rating')} · {$t(`review.rating.${feedback.rating}`)}</div>
              <div class="flex flex-col sm:flex-row gap-3">
                <button type="button" class="flex-1 min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-semibold hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer" onclick={continueNext} data-testid="review-graded-continue">{$t('review.graded.continue')}</button>
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl border border-input hover:bg-muted font-medium disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void undo()} data-testid="review-undo">{$t('review.undo')}</button>
              </div>
            </div>
          {:else if gradedRevealed}
            <div bind:this={revealedSection} class="space-y-5" data-testid="review-graded-revealed">
              <div class="border-t border-input pt-5">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.answer')}</div>
                <div class="text-lg whitespace-pre-wrap break-words font-medium" data-testid="review-graded-answer">{@html revealedAnswerHTML}</div>
              </div>
              <button type="button" disabled={submitting} class="w-full min-h-12 rounded-xl border border-rose-300 dark:border-rose-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 font-medium disabled:opacity-50 transition-colors cursor-pointer" onclick={() => void giveUp()} data-testid="review-graded-give-up">{$t('review.graded.give_up')}</button>
            </div>
          {:else}
            <div class="space-y-5">
              <div bind:this={frontSection} class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed" data-testid="review-graded-prompt">
                {#if frontHTML}{@html frontHTML}{:else}{gradedPrompt($cardTypes, current)}{/if}
              </div>
              {#if answerControl === 'text' || answerControl === 'number'}
                <input
                  bind:this={inputElement}
                  type="text"
                  inputmode={answerControl === 'number' ? 'decimal' : undefined}
                  bind:value={answerText}
                  placeholder={answerControl === 'number' ? $t('review.graded.placeholder_number') : $t('review.graded.placeholder')}
                  aria-label={$t('review.graded.placeholder')}
                  data-testid="review-graded-input"
                  class="field-input text-sm w-full min-h-12 text-lg"
                />
              {:else if answerControl === 'single'}
                <RadioGroup
                  testId="review-graded-options"
                  itemTestId="review-graded-option"
                  itemClass="min-h-12"
                  name="graded-single"
                  ariaLabel={$t('review.ratings')}
                  value={singleChoice === null ? '' : String(singleChoice)}
                  onValueChange={(value) => (singleChoice = value === '' ? null : Number(value))}
                  options={gradedOptions($cardTypes, current).map((option, index) => ({ value: String(index), label: option }))}
                />
              {:else if answerControl === 'multi'}
                <div class="space-y-2" data-testid="review-graded-options">
                  {#each gradedOptions($cardTypes, current) as option, index (index)}
                    <div class="flex min-h-12 items-center gap-3 rounded-xl border border-zinc-300 px-4 transition-colors hover:border-zinc-400 dark:border-zinc-700 dark:hover:border-zinc-600">
                      <Checkbox
                        testId="review-graded-option"
                        checked={multiChoice.includes(index)}
                        onCheckedChange={(checked) =>
                          (multiChoice = checked ? [...multiChoice, index] : multiChoice.filter((item) => item !== index))}
                        label={option}
                      />
                      <span>{option}</span>
                    </div>
                  {/each}
                </div>
              {:else if answerControl === 'bool'}
                <RadioGroup
                  testId="review-graded-options"
                  itemTestId="review-graded-option"
                  class="grid grid-cols-2 gap-3"
                  itemClass="min-h-12 justify-center font-medium"
                  name="graded-bool"
                  ariaLabel={$t('review.ratings')}
                  value={boolChoice === null ? '' : String(boolChoice)}
                  onValueChange={(value) => (boolChoice = value === '' ? null : value === 'true')}
                  options={[
                    { value: 'true', label: $t('review.graded.true') },
                    { value: 'false', label: $t('review.graded.false') },
                  ]}
                />
              {/if}
              {#if needAnswer}
                <p class="text-sm text-rose-700 dark:text-rose-300" data-testid="review-graded-need-answer">{$t('review.graded.need_answer')}</p>
              {/if}
              <div class="flex flex-col sm:flex-row gap-3 pt-2">
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-semibold hover:bg-zinc-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void submitGraded()} data-testid="review-graded-submit">{$t('review.graded.submit')}</button>
                <button type="button" disabled={submitting} class="flex-1 min-h-12 rounded-xl border border-input hover:bg-muted font-medium disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void revealGraded()} data-testid="review-graded-reveal">{$t('review.graded.show_answer')}</button>
              </div>
            </div>
          {/if}
        {/if}

        {#if !feedback}
          <div class="pt-4 border-t border-border flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs text-muted-foreground">
            <div class="flex gap-2 self-start">
              <Button variant="outline" size="sm" testId="review-bury" disabled={submitting} onclick={() => void bury()}>
                {$t('review.bury')}
              </Button>
              <Button variant="outline" size="sm" testId="review-suspend" disabled={submitting} onclick={() => void suspend()}>
                {$t('review.suspend')}
              </Button>
            </div>
            <p class="text-xs text-muted-foreground/70" data-testid="review-bury-hint">
              {$t('review.bury_hint')}
            </p>
          </div>
        {/if}
      </article>
    {/if}
  </div>
  <p class="mt-4 text-center text-xs text-muted-foreground">
    {feedback ? $t('review.shortcuts_result') : gradedKind ? (gradedRevealed ? $t('review.shortcuts_give_up') : $t('review.shortcuts_graded')) : revealed ? $t('review.shortcuts') : $t('review.shortcuts_reveal')}{lastUndo ? ` · ${$t('review.shortcuts_undo')}` : ''}
  </p>
</Page>

<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount, tick } from 'svelte';
  import { t } from '../i18n';
  import { fieldText } from '../format';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, AVTag, CardFace, DueCard, ExtraNewStatus, GradedAnswer, GradedFeedback } from '../api';
  import { toast } from '../components/ui/toast';
  import { typeset } from '../mathjax';
  import { enhanceShortcodes, stopShortcodePlayback } from '../shortcodes';
  import { reviewShortcut, reviewSwipe, type ReviewShortcut } from '../review-shortcuts';
  import CardFrame, { type CardFrameKey } from '../components/CardFrame.svelte';
  import { REVIEW_FRONT_CSS, REVIEW_BACK_CSS, REVIEW_ANSWER_CSS, faceStyle } from '../card-frame';

  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import HelpTip from '../components/ui/HelpTip.svelte';
  import { ArrowLeft, Check, CircleAlert, CircleCheckBig, CircleX, Eye, EyeOff, PartyPopper, Pause, Pencil, Plus, RefreshCw, Undo2, X } from '@lucide/svelte';
  import { withReturn } from '../return-to';
  import { navigate } from '../router';
  interface Props {
    client?: ApiClient;
    initialCards?: DueCard[];
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialFeedback?: GradedFeedback | null;
    initialRevealedAnswerHTML?: string;
    // The render endpoint's two faces, carrying the note type's CSS, body class and media base. In production the path is
    // fetched from the render endpoint via $effect; these initial values are kept so the DOM-less server-side rendering tests
    // can pin "the face HTML reaches the frame and never the parent DOM".
    initialFrontFace?: CardFace | null;
    initialBackFace?: CardFace | null;
    initialEditHref?: string;
    /** Whether the self-graded card is already flipped (flipped by reveal() in production; the initial value overrides it in tests). */
    initialRevealed?: boolean;
    /** The undo record of the most recent submission (written by submit in production; the initial value overrides it in tests). */
    initialLastUndo?: { cardId: string; version: number; rating: number } | null;
  }

  let {
    client = apiClient,
    initialCards = [],
    initialLoading = true,
    initialError = null,
    initialFeedback = null,
    initialRevealedAnswerHTML = '',
    initialFrontFace = null,
    initialBackFace = null,
    initialEditHref = '',
    initialRevealed = false,
    initialLastUndo = null,
  }: Props = $props();

  // The server decides how a card is answered: `machine_graded` is the rule the submit path applies, and `answer_control`
  // names the template's answer control (null when it has none). The page switches between the four self-ratings, the
  // essay flow and the machine-graded flow on those two fields only, so a note type that matches no stock kind answers
  // like any other.
  /** The short answer control records the written answer and keeps the rating self-assessed. */
  const SHORT_ANSWER_CONTROL = 'short_answer';

  /** Counts the queued cards that have never been studied; every other state (learning, review, relearning) counts as a review card. */
  function countNew(queue: DueCard[]): number {
    return queue.filter((card) => card.state === 'new').length;
  }

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
  // The header splits the due count into new and review cards. Every queue-bearing response carries the full remaining queue
  // (the visible card is only the first slice of it), so the new count is read off that queue and the review count is the
  // rest of the due total — no extra request is needed and the two numbers always add up to the due total.
  // svelte-ignore state_referenced_locally
  let newRemaining = $state(countNew(initialCards));
  const reviewRemaining = $derived(remaining - newRemaining);
  let startedAt = Date.now();

  // Answer state of answer-type card types; the control lives in the frame and posts its value, the server grades it.
  // frameAnswer is the last value the frame posted; undefined means the learner has not answered yet.
  let frameAnswer = $state<GradedAnswer | undefined>(undefined);
  // Short-answer cards: the answer written before self-grading. The frame posts it as the control's value; after flipping
  // it is compared against the reference answer and archived together with the self-grade when scoring.
  let essayText = $state('');
  // svelte-ignore state_referenced_locally
  let feedback = $state<GradedFeedback | null>(initialFeedback);
  let pendingCards = $state<DueCard[]>([]);
  // The most recent submission in this session (card id, post-submission state version, rating): the review page uses it to offer "undo the last rating".
  // After a self-graded submission the screen flips to the next card at once with no result panel, so undo can only rely on this record; graded cards use the same record,
  // and the panel button and the header entry share it, avoiding two sets of state.
  // svelte-ignore state_referenced_locally
  let lastUndo = $state<{ cardId: string; version: number; rating: number } | null>(initialLastUndo);
  // svelte-ignore state_referenced_locally
  let revealedAnswerHTML = $state(initialRevealedAnswerHTML);
  let revealedAVTags = $state<AVTag[]>([]);
  // svelte-ignore state_referenced_locally
  let gradedRevealed = $state(initialRevealedAnswerHTML !== '');
  let needAnswer = $state(false);

  // The render endpoint's card faces, one of the review page's HTML sinks. A null face means it has not been fetched yet,
  // in which case it falls back to plain text; the raw fields are never sent into the HTML sink as Markdown.
  // svelte-ignore state_referenced_locally
  let frontFace = $state<CardFace | null>(initialFrontFace);
  // svelte-ignore state_referenced_locally
  let backFace = $state<CardFace | null>(initialBackFace);
  // The current card's zero-based template ordinal, used for the frame body class `cardN` (N = ordinal + 1).
  let cardOrdinal = $state(0);
  // Whether the front template is a type-in placeholder without an answer spec. Such a card is self-rated like any other,
  // but its answer face compares the typed value, which only the server can do, so the reveal fetches that face again.
  // svelte-ignore state_referenced_locally
  let typeIn = $state(false);
  // svelte-ignore state_referenced_locally
  let editHref = $state(initialEditHref);
  let renderToken = 0;

  // The grading breakdown stays in the parent page: it is the only HTML container left here, so it is what gets typeset and enhanced.
  let feedbackSection = $state<HTMLElement | null>(null);

  // The `<base href>` prefix for the reveal and the graded-result answer frames. Those two responses carry the answer HTML but
  // no face object, so they bring their own base; the front and back frames read the base off the face the render sent.
  let revealedMediaBase = $state('');
  let feedbackMediaBase = $state('');

  const current = $derived(cards[0] || null);

  // "One more round": when the queue is empty and the review scope happens to be a single deck, read today's new card quota; if there are still new cards beyond today's quota
  // the completion page offers an append entry. The daily new card cap is a study goal rather than a hard limit, and appending only applies to today and only to yourself.
  let extraStatus = $state<ExtraNewStatus | null>(null);
  // The quota request is in progress: the completion page waits for it to finish before appearing as a whole, otherwise "review complete" would flash first and the append form would be inserted afterwards.
  let extraLoading = $state(false);
  // The raw text of the input (a string): number inputs do not use bind:value, otherwise an empty string is coerced to null and there is nothing left to validate.
  let extraCount = $state('');
  let extraBusy = $state(false);
  let extraInvalid = $state(false);
  // The appendable cap: the smaller of the per-request cap and "how many new cards are left beyond today's quota".
  const extraMax = $derived(
    extraStatus ? Math.min(extraStatus.max_count, Math.max(extraStatus.new_available - extraStatus.new_left, 0)) : 0,
  );
  const showExtra = $derived(extraStatus !== null && !extraStatus.new_unlimited && extraStatus.suggested_count > 0);
  // The current card's answer flow, read off the due-card payload. The plain-text fallback takes the first field in
  // config order as the question side and the rest as the answer side, so the answer stays out of it before the reveal.
  const machineGraded = $derived(current?.machine_graded ?? false);
  const answerControl = $derived(current?.answer_control ?? null);
  const essay = $derived(answerControl === SHORT_ANSWER_CONTROL);
  const frontFallback = $derived(current ? fieldValues(current).slice(0, 1) : []);
  const answerFallback = $derived(current ? fieldValues(current).slice(1) : []);
  // Edit entry: prefer the edit_href returned by the server's render; when it has not been fetched, join it per the canonical path.
  const fallbackEditHref = $derived(current ? `/decks/${current.deck_id}/notes/${current.note_id}` : '');

  /** The note's field values in the note type's field order, for the plain-text fallback when the face HTML is missing. */
  function fieldValues(card: DueCard): string[] {
    const fields = card.fields || {};
    return card.field_order.flatMap((id) => {
      const value = fields[id];
      return value === undefined ? [] : [fieldText(value)];
    });
  }

  function resetAnswerState(): void {
    frameAnswer = undefined;
    essayText = '';
    needAnswer = false;
  }

  /**
   * The last value the frame posted for the answer control. The frame posts the same shapes the SPA used to submit
   * (a string, an option index, an index array, a boolean, or one string per blank/item), so this is exactly the
   * payload internal/answer decodes.
   */
  function currentAnswer(): GradedAnswer | undefined {
    return frameAnswer;
  }

  /**
   * Receives one `answer {value}` message from a card frame. The short-answer card's written answer is kept for the
   * reveal and the self-grade; every other control's value is what submission sends.
   */
  function onFrameAnswer(value: GradedAnswer): void {
    if (essay) {
      essayText = typeof value === 'string' ? value : '';
      return;
    }
    frameAnswer = value;
    needAnswer = false;
  }

  function selectedDecks(): string[] | undefined {
    const search = new URLSearchParams(window.location.search);
    const values = search.getAll('deck').flatMap((raw) => raw.split(',')).filter(Boolean);
    return values.length ? values : undefined;
  }

  /**
   * The tag scope in the address (`/review?deck=A&tag=x&tag=y`), carried back verbatim with the deck scope on every request.
   * Not split on commas: a tag itself may contain a comma.
   */
  function selectedTags(): string[] | undefined {
    const search = new URLSearchParams(window.location.search);
    const values = search.getAll('tag').map((raw) => raw.trim()).filter(Boolean);
    return values.length ? values : undefined;
  }

  // The tag scope shown in the header; changing scope makes the router rebuild this view (the query string is part of the route key), so reading it once at initialization is enough.
  // Server-side rendering (svelte/server in tests) has no window, so there is no address to read and it is treated as having no tags.
  const scopeTags = typeof window === 'undefined' ? [] : (selectedTags() ?? []);

  /**
   * Fetch the current card's two faces. The token keeps an old request from overwriting the new card's content after a card change;
   * on failure it only discards the rich text and falls back to plain text, never interrupting the review flow.
   */
  async function loadRender(card: DueCard, token: number): Promise<void> {
    try {
      // The card face goes into the frame, so the request asks for frame-ready HTML and its media base.
      const response = await client.renderReviewCard(
        { card_id: card.card_id, deck: selectedDecks(), tags: selectedTags() },
        { frame: true },
      );
      if (token !== renderToken) return;
      frontFace = response.front;
      backFace = response.back;
      cardOrdinal = response.ordinal ?? 0;
      typeIn = response.type_in ?? false;
      editHref = response.edit_href;
    } catch {
      if (token !== renderToken) return;
      frontFace = null;
      backFace = null;
      cardOrdinal = 0;
      typeIn = false;
      editHref = '';
    }
  }

  // A card change refetches the faces; the old card's content is cleared so the previous card's rich text is not shown before the response arrives.
  $effect(() => {
    const card = current;
    const token = ++renderToken;
    frontFace = null;
    backFace = null;
    cardOrdinal = 0;
    typeIn = false;
    editHref = '';
    essayText = '';
    frameAnswer = undefined;
    if (card) void loadRender(card, token);
  });

  /** Returns the deck's public id when the review scope is exactly one deck; multiple decks and all decks return null ("one more round" works per single deck only). */
  function singleDeck(): string | null {
    const decks = selectedDecks();
    return decks?.length === 1 ? (decks[0] ?? null) : null;
  }

  /** Read the current deck's new card quota for today; when the scope is not a single deck or the read fails, no append entry is shown, without interrupting the completion page. */
  async function loadExtraNew(): Promise<void> {
    const deck = singleDeck();
    if (!deck) {
      extraStatus = null;
      return;
    }
    extraLoading = true;
    try {
      const status = await client.getExtraNew(deck, selectedTags() ?? []);
      extraStatus = status;
      extraCount = String(status.suggested_count);
      extraInvalid = false;
    } catch {
      extraStatus = null;
    } finally {
      extraLoading = false;
    }
  }

  /** Append new cards for today by the entered count, then refetch the queue on success so the new cards appear. */
  async function addExtraNew(): Promise<void> {
    const deck = singleDeck();
    if (!deck || !extraStatus || extraBusy) return;
    const count = Number(extraCount);
    if (extraCount.trim() === '' || !Number.isInteger(count) || count < 1 || count > extraMax) {
      extraInvalid = true;
      return;
    }
    extraInvalid = false;
    extraBusy = true;
    try {
      await client.addExtraNew(deck, count);
      extraStatus = null;
      await loadQueue();
    } catch (cause) {
      toast.error($t(cause instanceof ApiClientError && cause.isForbidden ? 'error.forbidden' : 'review.extra_new.failed'));
    } finally {
      extraBusy = false;
    }
  }

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
      const tags = selectedTags();
      const response = await client.getDueCards(query ? { deck: query, tag: tags, limit: 500 } : { tag: tags, limit: 500 });
      remaining = response.cards.length;
      newRemaining = countNew(response.cards);
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
      const written = essay && essayText.trim() !== '' ? essayText : undefined;
      const response = await client.submitSelfReview({
        card_id: ratedCardId,
        rating,
        answer: written,
        expected_version: current.version,
        elapsed_ms: elapsed,
        deck: selectedDecks(),
        tags: selectedTags(),
      });
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      newRemaining = countNew(response.cards);
      done += 1;
      // The screen has already flipped to the next card after self-grading: remember this submission so the header's "undo the last rating" becomes available.
      lastUndo = { cardId: ratedCardId, version: response.version, rating };
      revealed = false;
      startedAt = Date.now();
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /**
   * Reveal the back of a self-graded card. A type-in card compares the typed value with the expected text, and that
   * comparison is Anki's server-side rule, so its answer face is fetched again with the typed value. The rating stays
   * self-chosen: the typed value is only used to render the comparison and is never submitted as progress.
   */
  async function reveal(): Promise<void> {
    if (revealed) return;
    revealed = true;
    const answer = frameAnswer;
    const card = current;
    if (!typeIn || !card || typeof answer !== 'string' || answer === '') return;
    const cardId = card.card_id;
    try {
      const response = await client.renderReviewCard(
        { card_id: cardId, deck: selectedDecks(), tags: selectedTags(), answer },
        { frame: true },
      );
      // The learner may have moved on while the request was in flight; a stale answer face must not replace the new card's.
      if (current?.card_id === cardId) backFace = response.back;
    } catch {
      // The pre-fetched answer face still shows the expected text, so a failed comparison keeps the card usable.
    }
  }

  /** Submit the raw answer and let the server grade it; the result panel keeps the answered card, and the card only changes when the user presses "continue". */
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
      const response = await client.submitGradedReview(
        {
          card_id: current.card_id,
          expected_version: current.version,
          elapsed_ms: Math.max(0, Date.now() - startedAt),
          deck: selectedDecks(),
          tags: selectedTags(),
          answer: answerValue,
        },
        // The graded answer face is rendered by the frame, so this response must carry frame-ready HTML.
        { frame: true },
      );
      pendingCards = response.cards;
      remaining = response.remaining;
      newRemaining = countNew(response.cards);
      feedback = response.feedback ?? null;
      feedbackMediaBase = response.feedback?.media_base ?? backFace?.media_base ?? '';
      lastUndo = { cardId: current.card_id, version: response.version, rating: response.feedback?.rating ?? 0 };
      done += 1;
      // The answer face goes into the frame; only the per-item breakdown stays here, so it is what gets typeset and enhanced.
      await tick();
      await typeset([feedbackSection]);
      enhanceShortcodes([feedbackSection]);
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /** Request the answer reveal (read-only); after revealing, the input control is disabled and only "grade 0 and continue" remains. */
  async function revealGraded(): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await client.revealGradedAnswer(
        {
          card_id: current.card_id,
          deck: selectedDecks(),
          tags: selectedTags(),
        },
        // The revealed answer is a card face, so it is rendered by the frame.
        { frame: true },
      );
      revealedAnswerHTML = response.answer_html;
      revealedAVTags = response.back?.av_tags ?? [];
      revealedMediaBase = response.media_base ?? backFace?.media_base ?? '';
      gradedRevealed = true;
    } catch (cause) {
      error = cause;
    } finally {
      submitting = false;
    }
  }

  /** Give up answering after the answer was revealed: the server records a self-grade as Again without grading. */
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
        tags: selectedTags(),
        action: 'give_up',
      });
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      newRemaining = countNew(response.cards);
      // Giving up records a self-grade log as Again; the undo entry is available there as well.
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
   * Bury the current card: only the caller's own progress is written and no reviews rows are produced; the response carries the queue rebuilt for the same scope.
   * The server decides by reader, so readers of a shared deck can review on their own.
   */
  async function bury(): Promise<void> {
    await skipCurrent((cardId) => client.buryReview({ card_id: cardId, deck: selectedDecks(), tags: selectedTags() }));
  }

  /**
   * Suspend the current card: applies only to yourself (other people on a shared deck are unaffected); it can be un-suspended in the deck's note list.
   */
  async function suspend(): Promise<void> {
    await skipCurrent((cardId) => client.suspendReview({ card_id: cardId, deck: selectedDecks(), tags: selectedTags() }));
  }

  /** Shared by bury and suspend: after a successful request it swaps in the queue rebuilt by the server and resets this card's answer state. */
  async function skipCurrent(send: (cardId: string) => Promise<{ cards: DueCard[]; remaining: number }>): Promise<void> {
    if (!current || submitting) return;
    submitting = true;
    error = null;
    try {
      const response = await send(current.card_id);
      cards = response.cards.slice(0, 1);
      remaining = response.remaining;
      newRemaining = countNew(response.cards);
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

  /**
   * Edit entry: jump to the edit_href returned by the server (joined from the canonical path when absent).
   * The review address, scope included, rides along as the return path, so the editor's own back and
   * cancel actions lead back here instead of to the deck page.
   */
  function gotoEdit(): void {
    const href = editHref || fallbackEditHref;
    // A client-side route change: the editor is a page of this app, so a full document reload would only
    // refetch the shell.
    if (href) navigate(withReturn(href, window.location.pathname + window.location.search));
  }

  /**
   * The result panel's "continue": switch to the next card prefetched in the grading response, producing no extra write request.
   * lastUndo is deliberately not cleared: after leaving the panel the header's "undo the last rating" stays usable (undoing the card just graded).
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
   * Copy of the header undo button: it names the rating being undone. After a self-graded submission the screen has already flipped
   * to the next card, so saying only "undo" would make people think it undoes the current card.
   */
  function undoLabel(): string {
    const rating = lastUndo?.rating ?? 0;
    if (rating >= 1 && rating <= 4) {
      return $t('review.undo_last_rating', { rating: $t(`review.rating.${rating}`) });
    }
    return $t('review.undo_last');
  }

  /**
   * Undo the last submission's rating (self-graded and graded cards share this one entry): the server restores the progress and deletes that
   * review log, and the response carries the queue rebuilt for the same scope. After undoing, the undone card is put back at the current
   * position — the queue is sorted by due_at and is not guaranteed to come first, so it is looked up by undone_card_id inside the queue.
   *
   * The target is the card recorded in lastUndo, not the card currently on screen: after a self-graded submission the screen has already flipped to the next card.
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
        tags: selectedTags(),
        expected_version: target.version,
      });
      const restored = response.cards.find((card) => card.card_id === response.undone_card_id);
      if (restored) cards = [restored];
      remaining = response.remaining;
      newRemaining = countNew(response.cards);
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

  /** Whether focus is on a graded card's options. The choice options moved into the frame with the front face, so no
   * parent element can be a choice control and the answer is always "no". */
  function isChoiceTarget(_target: EventTarget | null): boolean {
    return false;
  }

  /** The action shared by space/enter and swipe: a graded card requests the answer reveal, a self-graded card flips. */
  function applyReveal(): void {
    if (machineGraded) {
      if (!gradedRevealed) void revealGraded();
      return;
    }
    if (!revealed) void reveal();
  }

  /**
   * Maps one key decision onto the page's side effects. The key can come from the window (focus anywhere in the parent page:
   * options, buttons, dialogs) or from the frame (focus inside the card face); only the flags describing the focus differ, the
   * decision itself stays in review-shortcuts.ts. The decision is returned so the window handler can prevent the browser
   * default of a key the page handled.
   */
  function handleShortcut(
    key: string,
    modifiers: { alt: boolean; ctrl: boolean; meta: boolean },
    focus: { typing: boolean; choosing: boolean; composing: boolean; repeat: boolean },
  ): ReviewShortcut {
    const action = reviewShortcut(key, modifiers, {
      ...focus,
      feedback: feedback !== null,
      machineGraded,
      gradedRevealed,
      revealed,
      canUndo: lastUndo !== null,
    });
    switch (action.kind) {
      case 'ignore':
        return action;
      case 'swallow':
        // On a graded card awaiting an answer, 1–4 are disabled but the key is still swallowed, so the browser does not treat the digit as a hotkey.
        return action;
      case 'continue':
        continueNext();
        return action;
      case 'reveal':
        applyReveal();
        return action;
      case 'submit':
        void submitGraded();
        return action;
      case 'give_up':
        void giveUp();
        return action;
      case 'rate':
        void rate(action.rating);
        return action;
      case 'bury':
        void bury();
        return action;
      case 'suspend':
        void suspend();
        return action;
      case 'edit':
        gotoEdit();
        return action;
      case 'undo':
        void undo();
        return action;
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    const action = handleShortcut(
      event.key,
      { alt: event.altKey, ctrl: event.ctrlKey, meta: event.metaKey },
      {
        typing: isTypingTarget(event.target),
        choosing: isChoiceTarget(event.target),
        // keyCode 229 is the fallback signal in some browsers that do not set isComposing during composition.
        composing: event.isComposing || event.keyCode === 229,
        repeat: event.repeat,
      },
    );
    // Every decision but `ignore` says the page took the key, so its browser default must not also run.
    if (action.kind !== 'ignore') event.preventDefault();
  }

  /**
   * A key pressed inside the card face. The frame withholds keys typed into an answer control, except Enter in a
   * single-line input, which it forwards as the graded card's submit key. Choice options now live in the frame too, so
   * no parent-side focus flag applies here.
   */
  function onFrameKey(message: CardFrameKey): void {
    handleShortcut(
      message.key,
      { alt: message.alt, ctrl: message.ctrl, meta: message.meta },
      { typing: false, choosing: false, composing: false, repeat: false },
    );
  }

  // Phone swipe: when the touch lands on an input control/button/link it is not treated as a swipe.
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
      machineGraded,
      gradedRevealed,
      revealed,
    });
    if (action.kind === 'reveal') applyReveal();
    else if (action.kind === 'rate') void rate(action.rating);
  }

  // The quota is only read when the queue is empty (loading finished, no error); it is cleared while there are cards, so the next empty queue does not flash an old number first.
  $effect(() => {
    if (loading || error) return;
    if (current) {
      extraStatus = null;
      return;
    }
    void loadExtraNew();
  });

  onMount(() => {
    void loadQueue();
    window.addEventListener('keydown', onKeydown);
    return () => {
      window.removeEventListener('keydown', onKeydown);
      // Speech and audio started from this card must not outlive the page that shows it.
      stopShortcodePlayback();
    };
  });
</script>

<svelte:head><title>{$t('review.title')} · {$t('app.name')}</title></svelte:head>
<Page as="section">
  <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
    <div class="min-w-0">
      <h1 class="text-2xl font-semibold tracking-tight text-foreground">{$t('review.title')}</h1>
      {#if scopeTags.length}
        <div class="mt-1.5 flex flex-wrap items-center gap-1" data-testid="review-scope-tags">
          <span class="text-xs text-muted-foreground">{$t('review.tag_scope')}</span>
          {#each scopeTags as tag (tag)}
            <span class="inline-flex h-5 max-w-full items-center truncate rounded-full border border-border px-2 text-xs text-muted-foreground">{tag}</span>
          {/each}
        </div>
      {/if}
    </div>
    <div class="flex items-center gap-2 self-start sm:self-auto">
      <!-- Undo the last rating: a self-graded submission has no result panel, so the entry lives here; it is hidden while the graded card's panel is open, avoiding two entries. -->
      {#if lastUndo && !feedback}
        <Button variant="outline" size="sm" testId="review-undo-last" disabled={submitting} onclick={() => void undo()}>
          <Undo2 class="size-3.5" aria-hidden="true" />
          <span>{undoLabel()}</span>
        </Button>
      {/if}
      <div
        class="inline-flex items-center gap-2.5 px-3.5 py-1.5 rounded-full bg-white dark:bg-zinc-900 border border-border shadow-xs text-xs font-medium text-muted-foreground"
        aria-live="polite"
      >
      <!-- The pulse marks the due queue as a whole, not one half of the split: it stays whether or not any new cards are left. -->
      <span class="w-2 h-2 shrink-0 rounded-full bg-blue-500 animate-pulse"></span>
      <!-- A new count of zero is hidden instead of shown as "new 0": the review count already reports the whole due total. -->
      {#if newRemaining > 0}
        <span class="inline-flex items-center gap-1.5" data-testid="review-due-new">
          <span>{$t('review.due_new')}</span>
          <strong class="font-mono text-foreground text-sm">{newRemaining}</strong>
        </span>
        <span class="w-1 h-3 border-r border-input"></span>
      {/if}
      <span class="inline-flex items-center gap-1.5" data-testid="review-due-review">
        <span>{$t('review.due_review')}</span>
        <strong class="font-mono text-foreground text-sm">{reviewRemaining}</strong>
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
        <Button variant="outline" size="lg" testId="review-retry" onclick={() => void loadQueue()}>
          <RefreshCw class="size-4" aria-hidden="true" />
          <span>{$t('common.retry')}</span>
        </Button>
      </div>
    {:else if !current && extraLoading}
      <Skeleton testId="review-extra-loading" label={$t('review.loading')} lines={2} />
    {:else if !current}
      <div class="text-center py-10" data-testid="review-empty">
        <!-- A session that just cleared its queue gets a celebration; one that opened onto an empty queue only a quiet check mark. -->
        <div class="mx-auto mb-4 flex size-12 items-center justify-center rounded-full bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400">
          {#if done > 0}
            <PartyPopper class="size-6" aria-hidden="true" />
          {:else}
            <CircleCheckBig class="size-6" aria-hidden="true" />
          {/if}
        </div>
        <p class="text-lg font-medium text-foreground/90">{$t('review.empty')}</p>
        {#if showExtra && extraStatus}
          <form
            class="mx-auto mt-6 max-w-sm space-y-3 rounded-xl border border-border p-4 text-left"
            data-testid="review-extra-new"
            novalidate
            onsubmit={(event) => {
              event.preventDefault();
              void addExtraNew();
            }}
          >
            <p class="text-sm text-foreground">{$t('review.extra_new.goal_done', { count: extraStatus.new_used })}</p>
            <div class="flex items-end gap-2">
              <div class="min-w-0 flex-1">
                <div class="flex items-baseline justify-between gap-2">
                  <label class="text-sm font-medium text-foreground" for="review-extra-count">{$t('review.extra_new.count_label')}</label>
                  <span id="review-extra-hint" class="text-xs text-muted-foreground">{$t('review.extra_new.hint', { count: extraMax })}</span>
                </div>
                <input
                  id="review-extra-count"
                  type="number"
                  min="1"
                  max={extraMax}
                  step="1"
                  inputmode="numeric"
                  data-testid="review-extra-count"
                  value={extraCount}
                  oninput={(event) => {
                    extraCount = event.currentTarget.value;
                    extraInvalid = false;
                  }}
                  aria-invalid={extraInvalid}
                  aria-describedby="review-extra-hint"
                  class="field-input mt-1.5 block w-full text-sm"
                />
              </div>
              <Button type="submit" testId="review-extra-submit" loading={extraBusy}><Plus class="size-4" aria-hidden="true" /><span>{$t('review.extra_new.submit')}</span></Button>
            </div>
            {#if extraInvalid}
              <p class="text-xs text-destructive-foreground" role="alert" data-testid="review-extra-invalid">{$t('review.extra_new.invalid', { max: extraMax })}</p>
            {/if}
          </form>
        {/if}
        <a class="mt-4 inline-flex items-center gap-1 text-sm text-blue-600 dark:text-blue-400 hover:underline font-medium" href="/">
          <ArrowLeft class="size-4" aria-hidden="true" />
          <span>{$t('review.home')}</span>
        </a>
      </div>
    {:else}
      <article data-testid="review-card" data-card-id={current.card_id} class="space-y-6">
        <div class="flex items-center justify-end pb-3 border-b border-border">
          <button
            type="button"
            onclick={gotoEdit}
            data-testid="review-edit"
            class="text-xs text-muted-foreground hover:text-foreground hover:bg-muted px-2.5 py-1 rounded-md transition-colors inline-flex items-center gap-1.5 cursor-pointer border border-input"
          >
            <Pencil class="size-3.5" aria-hidden="true" />
            <span>{$t('review.edit')}</span>
          </button>
        </div>

        {#if !machineGraded}
          <div class="min-h-28" data-testid="review-front">
            {#if frontFace && frontFace.html}
              <CardFrame
                html={frontFace.html}
                css={faceStyle(REVIEW_FRONT_CSS, frontFace.css)}
                bodyClass={frontFace.body_class}
                side="question"
                mediaBase={frontFace.media_base}
                autoplay={frontFace.autoplay ?? false}
                avTags={frontFace.av_tags ?? []}
                ordinal={cardOrdinal}
                title={$t('note_preview.front')}
                onkey={onFrameKey}
                onanswer={onFrameAnswer}
              />
            {:else}
              <div class="text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed">
                {#each frontFallback as value, index (index)}<div>{value}</div>{/each}
              </div>
            {/if}
          </div>
          {#if revealed}
            {#if essay}
              <!-- Short-answer card: first show the answer the learner wrote (plain text, not into the HTML sink), then the reference answer for comparison. -->
              {#if essayText.trim() !== ''}
                <div class="border-t border-input pt-5" data-testid="review-essay-given">
                  <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.given')}</div>
                  <div class="text-lg whitespace-pre-wrap break-words leading-relaxed">{essayText}</div>
                </div>
              {/if}
              <div class="border-t border-input pt-5" data-testid="review-answer">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.essay.reference')}</div>
                {#if backFace && backFace.html}
                  <CardFrame
                    html={backFace.html}
                    css={faceStyle(REVIEW_BACK_CSS, backFace.css)}
                    bodyClass={backFace.body_class}
                    ordinal={cardOrdinal}
                    side="answer"
                    mediaBase={backFace.media_base}
                    autoplay={backFace.autoplay ?? false}
                    avTags={backFace.av_tags ?? []}
                    title={$t('note_preview.back')}
                    onkey={onFrameKey}
                  />
                {:else if answerFallback.length}
                  <div class="text-lg whitespace-pre-wrap break-words leading-relaxed">
                    {#each answerFallback as value, index (index)}<div>{value}</div>{/each}
                  </div>
                {:else}
                  <p class="text-sm text-muted-foreground" data-testid="review-essay-no-reference">{$t('review.essay.no_reference')}</p>
                {/if}
              </div>
            {:else}
              <div class="border-t border-input pt-6" data-testid="review-answer">
                {#if backFace && backFace.html}
                  <CardFrame
                    html={backFace.html}
                    css={faceStyle(REVIEW_BACK_CSS, backFace.css)}
                    bodyClass={backFace.body_class}
                    ordinal={cardOrdinal}
                    side="answer"
                    mediaBase={backFace.media_base}
                    autoplay={backFace.autoplay ?? false}
                    avTags={backFace.av_tags ?? []}
                    title={$t('note_preview.back')}
                    onkey={onFrameKey}
                  />
                {:else}
                  <div class="text-lg whitespace-pre-wrap break-words leading-relaxed">
                    {#each answerFallback as value, index (index)}<div>{value}</div>{/each}
                  </div>
                {/if}
              </div>
            {/if}
            <!-- How to choose among the four ratings goes into a popover: keeping four explanations on every card takes too much space, and one click when unsure is enough. -->
            <div class="flex items-center gap-1 pt-2 text-xs text-muted-foreground">
              <span>{$t('review.ratings')}</span>
              <HelpTip testId="review-ratings-help" label={$t('help.review.ratings_label')} text={$t('help.review.ratings')} />
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3" aria-label={$t('review.ratings')}>
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
              data-testid="review-reveal"
              class="w-full min-h-12 inline-flex items-center justify-center gap-2 rounded-xl bg-zinc-900 text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200 font-semibold transition-all btn-press shadow-sm cursor-pointer"
              onclick={() => void reveal()}
            >
              <Eye class="size-4" aria-hidden="true" />
              <span>{$t('review.show_answer')}</span>
            </button>
          {/if}
        {:else}
          <!-- The answer card's prompt stays on top in all three states — answering, answer revealed, grading result — with the answer below
               behind a divider, so the learner does not have to recall the original question while reading the answer. -->
          <div class="space-y-5">
            <div class="min-h-28" data-testid="review-graded-prompt">
              {#if frontFace && frontFace.html}
                <CardFrame
                  html={frontFace.html}
                  css={faceStyle(REVIEW_FRONT_CSS, frontFace.css)}
                  bodyClass={frontFace.body_class}
                  ordinal={cardOrdinal}
                  side="question"
                  mediaBase={frontFace.media_base}
                  autoplay={frontFace.autoplay ?? false}
                  avTags={frontFace.av_tags ?? []}
                  title={$t('note_preview.front')}
                  onkey={onFrameKey}
                  onanswer={onFrameAnswer}
                />
              {:else}
                <div class="text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed">
                  {#each frontFallback as value, index (index)}<div>{value}</div>{/each}
                </div>
              {/if}
            </div>
            {#if feedback}
              <div bind:this={feedbackSection} class="space-y-5 border-t border-input pt-5" data-testid="review-graded-result">
                <div class="flex items-center justify-between">
                  <Badge variant={verdictVariant(feedback.verdict)} class="gap-1.5 px-3 py-1 text-sm font-semibold" testId="review-graded-verdict">
                    {#if feedback.verdict === 'correct'}
                      <CircleCheckBig class="size-4 text-emerald-600 dark:text-emerald-400" strokeWidth={2.5} aria-hidden="true" />
                    {:else if feedback.verdict === 'partial'}
                      <CircleAlert class="size-4 text-amber-600 dark:text-amber-400" strokeWidth={2.5} aria-hidden="true" />
                    {:else}
                      <CircleX class="size-4 text-rose-600 dark:text-rose-400" strokeWidth={2.5} aria-hidden="true" />
                    {/if}
                    <span>{$t(`review.graded.${feedback.verdict}`)}</span>
                  </Badge>
                  <Badge testId="review-graded-score" class="px-2.5 py-1 font-mono">
                    {$t('review.graded.score')} · {Math.round(feedback.score * 100)}%
                  </Badge>
                </div>
                <div>
                  <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.answer')}</div>
                  {#if feedback.breakdown && feedback.breakdown.length}
                    <!-- Per-item grading: marks every item as remembered (✓) or missed (✗) in item order. -->
                    <ul class="space-y-1.5 text-lg" data-testid="review-graded-breakdown">
                      {#each feedback.breakdown as item, index (index)}
                        <li class="flex items-start gap-2" data-testid="review-graded-breakdown-item" data-correct={item.correct}>
                          {#if item.correct}
                            <Check class="mt-1.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" strokeWidth={3} aria-label={$t('review.graded.correct')} />
                          {:else}
                            <X class="mt-1.5 size-4 shrink-0 text-rose-600 dark:text-rose-400" strokeWidth={3} aria-label={$t('review.graded.incorrect')} />
                          {/if}
                          <div class="min-w-0 break-words font-medium [&_p]:m-0">{@html item.answer_html}</div>
                        </li>
                      {/each}
                    </ul>
                  {:else}
                    <div data-testid="review-graded-answer">
                      <!-- The grade response carries the answer HTML but no face object: the note type is the card's own, so its
                           back face supplies the CSS and body class the frame needs. -->
                      <CardFrame
                        html={feedback.back?.html ?? feedback.answer_html}
                        css={faceStyle(REVIEW_ANSWER_CSS, feedback.back?.css ?? backFace?.css)}
                        bodyClass={feedback.back?.body_class ?? backFace?.body_class ?? 'card'}
                        ordinal={cardOrdinal}
                        side="answer"
                        mediaBase={feedbackMediaBase}
                        autoplay={feedback.back?.autoplay ?? backFace?.autoplay ?? false}
                        avTags={feedback.back?.av_tags ?? []}
                        title={$t('note_preview.back')}
                        onkey={onFrameKey}
                      />
                    </div>
                  {/if}
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
              <div class="space-y-5 border-t border-input pt-5" data-testid="review-graded-revealed">
                <div data-testid="review-graded-revealed-answer">
                  <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.answer')}</div>
                  <div data-testid="review-graded-answer">
                    <!-- The reveal response carries the answer HTML but no face object: the note type is the card's own, so its
                         back face supplies the CSS and body class the frame needs. -->
                    <CardFrame
                      html={revealedAnswerHTML}
                      css={faceStyle(REVIEW_ANSWER_CSS, backFace?.css)}
                      bodyClass={backFace?.body_class ?? 'card'}
                      ordinal={cardOrdinal}
                      side="answer"
                      mediaBase={revealedMediaBase}
                      autoplay={backFace?.autoplay ?? false}
                      avTags={revealedAVTags}
                      title={$t('note_preview.back')}
                      onkey={onFrameKey}
                    />
                  </div>
                </div>
                <button type="button" disabled={submitting} class="w-full min-h-12 rounded-xl border border-rose-300 dark:border-rose-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 font-medium disabled:opacity-50 transition-colors cursor-pointer" onclick={() => void giveUp()} data-testid="review-graded-give-up">{$t('review.graded.give_up')}</button>
              </div>
            {:else}
              <div class="space-y-5">
                <!-- The answer control itself is part of the frame's front face: its markup comes from the server and it posts `answer {value}`.
                     The submit and reveal buttons stay here so the graded flow and its shortcuts keep one owner. -->
                {#if needAnswer}
                  <p class="text-sm text-rose-700 dark:text-rose-300" data-testid="review-graded-need-answer">{$t('review.graded.need_answer')}</p>
                {/if}
                <div class="flex flex-col sm:flex-row gap-3 pt-2">
                  <button type="button" disabled={submitting} class="flex-1 min-h-12 inline-flex items-center justify-center gap-2 rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 font-semibold hover:bg-zinc-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void submitGraded()} data-testid="review-graded-submit"><Check class="size-4" aria-hidden="true" /><span>{$t('review.graded.submit')}</span></button>
                  <button type="button" disabled={submitting} class="flex-1 min-h-12 inline-flex items-center justify-center gap-2 rounded-xl border border-input hover:bg-muted font-medium disabled:opacity-50 transition-colors btn-press cursor-pointer" onclick={() => void revealGraded()} data-testid="review-graded-reveal"><Eye class="size-4" aria-hidden="true" /><span>{$t('review.graded.show_answer')}</span></button>
                </div>
              </div>
            {/if}
          </div>
        {/if}

        {#if !feedback}
          <div class="pt-4 border-t border-border flex items-center text-xs text-muted-foreground">
            <div class="flex items-center gap-2">
              <Button variant="outline" size="sm" testId="review-bury" disabled={submitting} onclick={() => void bury()}>
                <EyeOff class="size-3.5" aria-hidden="true" />
                <span>{$t('review.bury')}</span>
              </Button>
              <Button variant="outline" size="sm" testId="review-suspend" disabled={submitting} onclick={() => void suspend()}>
                <Pause class="size-3.5" aria-hidden="true" />
                <span>{$t('review.suspend')}</span>
              </Button>
              <!-- The difference between the two buttons goes into a popover: keeping a whole sentence at the bottom of every card is too conspicuous, and one click the first time it is unclear is enough. -->
              <HelpTip testId="review-bury-hint" label={$t('help.review.bury_label')} text={$t('review.bury_hint')} />
            </div>
          </div>
        {/if}
      </article>
    {/if}
  </div>
  <p class="mt-4 text-center text-xs text-muted-foreground">
    {feedback ? $t('review.shortcuts_result') : machineGraded ? (gradedRevealed ? $t('review.shortcuts_give_up') : $t('review.shortcuts_graded')) : revealed ? $t('review.shortcuts') : $t('review.shortcuts_reveal')}{lastUndo ? ` · ${$t('review.shortcuts_undo')}` : ''}
  </p>
</Page>

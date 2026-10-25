<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount, tick } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ApiClient, DueCard, ExtraNewStatus, GradedAnswer, GradedFeedback } from '../api';
  import { toast } from '../components/ui/toast';
  import { typeset, removeMenuTabStops } from '../mathjax';
  import { enhanceShortcodes, stopShortcodePlayback } from '../shortcodes';
  import { reviewShortcut, reviewSwipe } from '../review-shortcuts';
  import RadioGroup from '../components/ui/RadioGroup.svelte';

  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import HelpTip from '../components/ui/HelpTip.svelte';
  import { noteKindLabel as kindLabel } from '../labels';
  import { cardTypes, descriptionOf, loadCardTypes, type CardTypeCatalog } from '../card-types';
  interface Props {
    client?: ApiClient;
    initialCards?: DueCard[];
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialFeedback?: GradedFeedback | null;
    initialRevealedAnswerHTML?: string;
    // The server-sanitized card-face HTML and the edit address. In production the path is fetched from the render endpoint via $effect; these
    // initial values are kept so the DOM-less server-side rendering tests can pin "only sanitized HTML reaches the HTML sink".
    initialFrontHTML?: string;
    initialBackHTML?: string;
    /**
     * The server-sanitized HTML of each choice option, in option order (null/empty for card types without options). In production it arrives
     * with the render response; the initial value lets the DOM-less server-side rendering tests pin the option HTML sink.
     */
    initialOptionsHTML?: string[];
    /**
     * Display order of the choice options: display slot -> stored option index. In production it arrives with the render
     * response; the initial value lets the DOM-less server-side rendering tests pin a non-identity order.
     */
    initialOptionOrder?: number[];
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
    initialFrontHTML = '',
    initialBackHTML = '',
    initialOptionsHTML = [],
    initialOptionOrder,
    initialEditHref = '',
    initialRevealed = false,
    initialLastUndo = null,
  }: Props = $props();

  // A per-instance id prefix for elements that labels point at with aria-labelledby. Two mounted review views (or a client-side
  // remount) must not produce the same id, otherwise a checkbox could take its accessible name from the other view's option.
  const uid = $props.id();

  // Self-graded card types render four rating buttons; answer-type card types render an input control and are graded by the server.
  // Which card type belongs to which class is decided by the server's self-description (graded / answer_control); the frontend no longer maintains a list.

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

  // Answer and feedback state of answer-type card types; the grading result comes from the server, the client does not choose a rating.
  let answerText = $state('');
  let singleChoice = $state<number | null>(null);
  let multiChoice = $state<number[]>([]);
  let boolChoice = $state<boolean | null>(null);
  // Per-blank answer cards: blanks are the per-blank prompts given by the server (they decide the number of inputs), and blankAnswers corresponds to them one to one.
  let blanks = $state<string[]>([]);
  let blankAnswers = $state<string[]>([]);
  let blankInputs = $state<(HTMLInputElement | null)[]>([]);
  // Short-answer cards: the answer written before self-grading. After flipping it is compared against the reference answer and archived together with the self-grade when scoring.
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
  // svelte-ignore state_referenced_locally
  let gradedRevealed = $state(initialRevealedAnswerHTML !== '');
  let needAnswer = $state(false);

  // The server-sanitized card-face HTML, one of the review page's HTML sinks. An empty string means it has not been fetched yet,
  // in which case it falls back to plain text; the raw fields are never sent into the HTML sink as Markdown.
  // svelte-ignore state_referenced_locally
  let frontHTML = $state(initialFrontHTML);
  // svelte-ignore state_referenced_locally
  let backHTML = $state(initialBackHTML);
  // The server-sanitized HTML of each choice option, in option order. The answer controls inject these fragments instead of the raw option
  // text, which would print TeX delimiters literally; the raw field text never reaches an HTML sink.
  // svelte-ignore state_referenced_locally
  let optionsHTML = $state<string[]>(initialOptionsHTML);
  // The server's display order for the current card's options: display slot -> stored option index. An empty/absent value means
  // the stored order; the derived list below validates it before use, so a stale or malformed value can never hide an option.
  // svelte-ignore state_referenced_locally
  let optionOrder = $state<number[] | null>(initialOptionOrder ?? null);
  // svelte-ignore state_referenced_locally
  let editHref = $state(initialEditHref);
  let renderToken = 0;

  // The HTML containers returned sanitized by the server: formulas are typeset only on them, never for the whole page.
  let frontSection = $state<HTMLElement | null>(null);
  let answerSection = $state<HTMLElement | null>(null);
  let optionsSection = $state<HTMLElement | null>(null);
  let feedbackSection = $state<HTMLElement | null>(null);
  let revealedSection = $state<HTMLElement | null>(null);

  const current = $derived(cards[0] || null);

  type OptionSlot = { slot: number; stored: number; text: string; html: string | undefined };

  /**
   * The choice options in display order. `slot` is the screen row, `stored` is the option's index in the stored list; the
   * server's option_order maps one to the other. A missing, null, or malformed order (not a permutation of the option count)
   * falls back to the stored order, so every option is still rendered exactly once.
   */
  const optionSlots = $derived.by<OptionSlot[]>(() => {
    const options = current ? gradedOptions($cardTypes, current) : [];
    const count = options.length;
    const identity = options.map((_, index) => index);
    const order = optionOrder;
    let resolved = identity;
    if (order && order.length === count) {
      const seen = new Set(order);
      const valid = seen.size === count && order.every((stored) => Number.isInteger(stored) && stored >= 0 && stored < count);
      if (valid) resolved = order;
    }
    return resolved.map((stored, slot) => ({
      slot,
      stored,
      text: options[stored] ?? '',
      html: optionsHTML[stored],
    }));
  });

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
  // The current card's card type self-description; null when the metadata is not ready or the card type is unknown, and the view renders the loading state instead of guessing fields.
  const desc = $derived(descriptionOf($cardTypes, current?.kind));
  const selfAssessable = $derived(desc !== null && !desc.graded);
  const gradedKind = $derived(desc !== null && desc.graded);
  const answerControl = $derived(desc?.answer_control ?? 'none');
  const front = $derived(current && $cardTypes ? cardSide($cardTypes, current, false) : []);
  const answer = $derived(current && $cardTypes ? cardSide($cardTypes, current, true) : []);
  // Edit entry: prefer the edit_href returned by the server's render; when it has not been fetched, join it per the canonical path.
  const fallbackEditHref = $derived(current ? `/decks/${current.deck_id}/notes/${current.note_id}` : '');

  /**
   * The card face's front/back fields: the front and back field names come from the metadata (front_field / back_field).
   * Template-level reversal (basic_both's reverse) swaps front and back here — the metadata describes the forward
   * mapping and does not encode template-level reversal.
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

  /** The prompt text of an answer-type card type: the field name comes from the metadata's prompt_field. */
  function gradedPrompt(catalog: CardTypeCatalog | null, card: DueCard): string {
    const description = descriptionOf(catalog, card.kind);
    if (!description || !description.prompt_field) return '';
    const value = (card.fields || {})[description.prompt_field];
    return value === undefined || value === null ? '' : String(value);
  }

  /** The option text of a multiple-choice question: the field name comes from the metadata's options_field; only the options are read, the answer field is never rendered. */
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
    blankAnswers = blanks.map(() => '');
    needAnswer = false;
  }

  /** Assemble the current card's raw answer; an unanswered multiple-choice question returns undefined (blocked before submission). The control type comes from the metadata. */
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
      case 'blanks':
      case 'items':
        // The number of inputs comes from the render response; without it there is no way to answer, so it is blocked as unanswered instead of submitting an empty array that is bound to be wrong.
        return blanks.length ? blankAnswers.slice(0, blanks.length) : undefined;
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
   * Fetch the current card's server-sanitized HTML. The token keeps an old request from overwriting the new card's content after a card change;
   * on failure it only discards the rich text and falls back to plain text, never interrupting the review flow.
   */
  async function loadRender(card: DueCard, token: number): Promise<void> {
    try {
      const response = await client.renderReviewCard({ card_id: card.card_id, deck: selectedDecks(), tags: selectedTags() });
      if (token !== renderToken) return;
      frontHTML = response.front_html;
      backHTML = response.back_html;
      optionsHTML = response.options_html ?? [];
      optionOrder = response.option_order ?? null;
      editHref = response.edit_href;
      blanks = response.blanks ?? [];
      blankAnswers = blanks.map(() => '');
      await tick();
      // The per-blank inputs only appear with the render response, while the auto-focus on a card change runs before that, so it is topped up here.
      if (blanks.length && !feedback && !gradedRevealed) blankInputs[0]?.focus();
      await typeset([frontSection, answerSection, optionsSection]);
      // Shortcode controls are attached only to the sanitized DOM of this card face: the module reads data attributes, never the raw fields.
      enhanceShortcodes([frontSection, answerSection, optionsSection]);
      // The option rows are interactive controls, so MathJax's keyboard entry to its context menu must not add a nested
      // focus stop there; the prompt and answer sections are ordinary content and keep their tab stop.
      removeMenuTabStops([optionsSection]);
    } catch {
      if (token !== renderToken) return;
      frontHTML = '';
      backHTML = '';
      optionsHTML = [];
      optionOrder = null;
      editHref = '';
    }
  }

  // A card change refetches the sanitized HTML; the old card's content is cleared so the previous card's rich text is not shown before the response arrives.
  $effect(() => {
    const card = current;
    const token = ++renderToken;
    frontHTML = '';
    backHTML = '';
    optionsHTML = [];
    optionOrder = null;
    editHref = '';
    blanks = [];
    blankAnswers = [];
    essayText = '';
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
      const written = answerControl === 'essay' && essayText.trim() !== '' ? essayText : undefined;
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

  /** Reveal the back of a self-graded card: mount the DOM first, then typeset formulas only on the back container. */
  async function reveal(): Promise<void> {
    revealed = true;
    await tick();
    await typeset([answerSection]);
    enhanceShortcodes([answerSection]);
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
      const response = await client.submitGradedReview({
        card_id: current.card_id,
        expected_version: current.version,
        elapsed_ms: Math.max(0, Date.now() - startedAt),
        deck: selectedDecks(),
        tags: selectedTags(),
        answer: answerValue,
      });
      pendingCards = response.cards;
      remaining = response.remaining;
      newRemaining = countNew(response.cards);
      feedback = response.feedback ?? null;
      lastUndo = { cardId: current.card_id, version: response.version, rating: response.feedback?.rating ?? 0 };
      done += 1;
      // Wait for the result panel to mount into the DOM, then typeset only it with the answer HTML returned by the server.
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
      const response = await client.revealGradedAnswer({
        card_id: current.card_id,
        deck: selectedDecks(),
        tags: selectedTags(),
      });
      revealedAnswerHTML = response.answer_html;
      gradedRevealed = true;
      // Typeset the revealed answer HTML once the reveal panel has mounted into the DOM.
      await tick();
      await typeset([revealedSection]);
      enhanceShortcodes([revealedSection]);
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

  /** Edit entry: jump to the edit_href returned by the server (joined from the canonical path when absent). */
  function gotoEdit(): void {
    const href = editHref || fallbackEditHref;
    if (href) window.location.assign(href);
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

  /** Whether focus is on a graded card's options (rendered by bits-ui as role=radio/checkbox buttons). */
  function isChoiceTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) return false;
    const role = target.getAttribute('role');
    return role === 'radio' || role === 'checkbox';
  }

  /** The action shared by space/enter and swipe: a graded card requests the answer reveal, a self-graded card flips. */
  function applyReveal(): void {
    if (gradedKind) {
      if (!gradedRevealed) void revealGraded();
      return;
    }
    if (!revealed) void reveal();
  }

  function onKeydown(event: KeyboardEvent): void {
    // The key mapping is a pure function (review-shortcuts.ts); this only maps the decision onto side effects.
    const action = reviewShortcut(
      event.key,
      { alt: event.altKey, ctrl: event.ctrlKey, meta: event.metaKey },
      {
        typing: isTypingTarget(event.target),
        choosing: isChoiceTarget(event.target),
        // keyCode 229 is the fallback signal in some browsers that do not set isComposing during composition.
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
        // On a graded card awaiting an answer, 1–4 are disabled but the key is still swallowed, so the browser does not treat the digit as a hotkey.
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
      gradedKind,
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
    // Metadata is loaded only once (idempotent within the module); on failure it silently degrades to the loading/empty state without interrupting the review flow.
    void loadCardTypes(client).catch(() => {});
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
          {undoLabel()}
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
        <button class="inline-flex items-center gap-2 px-4 py-2 min-h-10 rounded-lg border border-input hover:bg-muted text-sm font-medium transition-colors cursor-pointer" onclick={() => void loadQueue()}>
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" /></svg>
          <span>{$t('common.retry')}</span>
        </button>
      </div>
    {:else if !current && extraLoading}
      <Skeleton testId="review-extra-loading" label={$t('review.loading')} lines={2} />
    {:else if !current}
      <div class="text-center py-10" data-testid="review-empty">
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
              <Button type="submit" testId="review-extra-submit" loading={extraBusy}>{$t('review.extra_new.submit')}</Button>
            </div>
            {#if extraInvalid}
              <p class="text-xs text-destructive-foreground" role="alert" data-testid="review-extra-invalid">{$t('review.extra_new.invalid', { max: extraMax })}</p>
            {/if}
          </form>
        {/if}
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
          <!-- Metadata not ready (still loading or the fetch failed): render the loading state, do not guess fields and do not print machine vocabulary. -->
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
            {#if answerControl === 'essay'}
              <!-- Short-answer card: first show the answer the learner wrote (plain text, not into the HTML sink), then the reference answer for comparison. -->
              {#if essayText.trim() !== ''}
                <div class="border-t border-input pt-5" data-testid="review-essay-given">
                  <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.given')}</div>
                  <div class="text-lg whitespace-pre-wrap break-words leading-relaxed">{essayText}</div>
                </div>
              {/if}
              <div bind:this={answerSection} class="border-t border-input pt-5 text-lg whitespace-pre-wrap break-words leading-relaxed" data-testid="review-answer">
                <div class="text-xs text-zinc-500 mb-1">{$t('review.essay.reference')}</div>
                {#if backHTML}
                  {@html backHTML}
                {:else if answer.length}
                  {#each answer as [label, value] (label)}<div>{value}</div>{/each}
                {:else}
                  <p class="text-sm text-muted-foreground" data-testid="review-essay-no-reference">{$t('review.essay.no_reference')}</p>
                {/if}
              </div>
            {:else}
              <div bind:this={answerSection} class="border-t border-input pt-6 text-lg whitespace-pre-wrap break-words leading-relaxed" data-testid="review-answer">
                {#if backHTML}
                  {@html backHTML}
                {:else}
                  {#each answer as [label, value] (label)}<div><span class="text-xs text-zinc-500 block">{label}</span>{value}</div>{/each}
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
            {#if answerControl === 'essay'}
              <textarea
                bind:value={essayText}
                rows="4"
                placeholder={$t('review.essay.placeholder')}
                aria-label={$t('review.graded.given')}
                data-testid="review-essay-input"
                class="field-input w-full text-base"
              ></textarea>
            {/if}
            <button
              type="button"
              data-testid="review-reveal"
              class="w-full min-h-12 rounded-xl bg-zinc-900 text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200 font-semibold transition-all btn-press shadow-sm cursor-pointer"
              onclick={() => void reveal()}
            >
              {$t('review.show_answer')}
            </button>
          {/if}
        {:else if gradedKind}
          <!-- The answer card's prompt stays on top in all three states — answering, answer revealed, grading result — with the answer below
               behind a divider, so the learner does not have to recall the original question while reading the answer. -->
          <div class="space-y-5">
            <div bind:this={frontSection} class="min-h-28 text-xl sm:text-2xl whitespace-pre-wrap break-words leading-relaxed" data-testid="review-graded-prompt">
              {#if frontHTML}{@html frontHTML}{:else}{gradedPrompt($cardTypes, current)}{/if}
            </div>
            {#if feedback}
              <div bind:this={feedbackSection} class="space-y-5 border-t border-input pt-5" data-testid="review-graded-result">
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
                <div>
                  <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.answer')}</div>
                  {#if feedback.breakdown && feedback.breakdown.length}
                    <!-- Per-item grading: marks every item as remembered (✓) or missed (✗) in item order. -->
                    <ul class="space-y-1.5 text-lg" data-testid="review-graded-breakdown">
                      {#each feedback.breakdown as item, index (index)}
                        <li class="flex items-start gap-2" data-testid="review-graded-breakdown-item" data-correct={item.correct}>
                          {#if item.correct}
                            <svg class="mt-1.5 w-4 h-4 shrink-0 text-emerald-600 dark:text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-label={$t('review.graded.correct')}><polyline points="20 6 9 17 4 12"/></svg>
                          {:else}
                            <svg class="mt-1.5 w-4 h-4 shrink-0 text-rose-600 dark:text-rose-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-label={$t('review.graded.incorrect')}><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
                          {/if}
                          <div class="min-w-0 break-words font-medium [&_p]:m-0">{@html item.answer_html}</div>
                        </li>
                      {/each}
                    </ul>
                  {:else}
                    <div class="text-lg whitespace-pre-wrap break-words font-medium" data-testid="review-graded-answer">{@html feedback.answer_html}</div>
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
              <div bind:this={revealedSection} class="space-y-5 border-t border-input pt-5" data-testid="review-graded-revealed">
                <div data-testid="review-graded-revealed-answer">
                  <div class="text-xs text-zinc-500 mb-1">{$t('review.graded.answer')}</div>
                  <div class="text-lg whitespace-pre-wrap break-words font-medium" data-testid="review-graded-answer">{@html revealedAnswerHTML}</div>
                </div>
                <button type="button" disabled={submitting} class="w-full min-h-12 rounded-xl border border-rose-300 dark:border-rose-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 font-medium disabled:opacity-50 transition-colors cursor-pointer" onclick={() => void giveUp()} data-testid="review-graded-give-up">{$t('review.graded.give_up')}</button>
              </div>
            {:else}
              <div class="space-y-5">
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
                  <div bind:this={optionsSection}>
                    <RadioGroup
                      testId="review-graded-options"
                      itemTestId="review-graded-option"
                      itemClass="min-h-12"
                      name="graded-single"
                      ariaLabel={$t('review.graded.options')}
                      value={singleChoice === null ? '' : String(singleChoice)}
                      onValueChange={(value) => (singleChoice = value === '' ? null : Number(value))}
                      options={optionSlots.map((option) => ({
                        value: String(option.stored),
                        label: option.text,
                        html: option.html || undefined,
                      }))}
                    />
                  </div>
                {:else if answerControl === 'multi'}
                  <div bind:this={optionsSection} class="space-y-2" data-testid="review-graded-options">
                    {#each optionSlots as option (option.slot)}
                      <!-- The whole row is a label: clicking the option text toggles the checkbox too, consistent with the single-choice row being clickable; the selected style matches single choice. -->
                      <label
                        class="flex min-h-12 cursor-pointer items-center gap-3 rounded-xl border border-zinc-300 px-4 text-sm text-zinc-700 transition-colors hover:border-zinc-400 data-[state=checked]:border-blue-500 data-[state=checked]:bg-blue-50 data-[state=checked]:font-medium data-[state=checked]:text-blue-700 dark:border-zinc-700 dark:text-zinc-300 dark:hover:border-zinc-500 dark:data-[state=checked]:border-blue-600 dark:data-[state=checked]:bg-blue-950/40 dark:data-[state=checked]:text-blue-300"
                        data-state={multiChoice.includes(option.stored) ? 'checked' : 'unchecked'}
                        data-testid="review-graded-option-row"
                      >
                        <Checkbox
                          testId="review-graded-option"
                          checked={multiChoice.includes(option.stored)}
                          onCheckedChange={(checked) =>
                            (multiChoice = checked
                              ? [...multiChoice, option.stored]
                              : multiChoice.filter((item) => item !== option.stored))}
                          labelledBy={`${uid}-option-${option.slot}`}
                        />
                        <span id={`${uid}-option-${option.slot}`}>{#if option.html}{@html option.html}{:else}{option.text}{/if}</span>
                      </label>
                    {/each}
                  </div>
                {:else if answerControl === 'blanks' || answerControl === 'items'}
                  <div class="space-y-3" data-testid="review-graded-blanks">
                    {#each blanks as hint, index (index)}
                      <label class="block">
                        <span class="mb-1 block text-xs text-zinc-500">{$t(answerControl === 'items' ? 'review.graded.item' : 'review.graded.blank', { index: index + 1 })}</span>
                        <input
                          bind:this={blankInputs[index]}
                          type="text"
                          bind:value={blankAnswers[index]}
                          placeholder={hint || $t('review.graded.placeholder')}
                          data-testid="review-graded-blank"
                          class="field-input text-sm w-full min-h-12 text-lg"
                        />
                      </label>
                    {/each}
                  </div>
                {:else if answerControl === 'bool'}
                  <RadioGroup
                    testId="review-graded-options"
                    itemTestId="review-graded-option"
                    class="grid grid-cols-2 gap-3"
                    itemClass="min-h-12 justify-center font-medium"
                    name="graded-bool"
                    ariaLabel={$t('review.graded.options')}
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
          </div>
        {/if}

        {#if !feedback}
          <div class="pt-4 border-t border-border flex items-center text-xs text-muted-foreground">
            <div class="flex items-center gap-2">
              <Button variant="outline" size="sm" testId="review-bury" disabled={submitting} onclick={() => void bury()}>
                {$t('review.bury')}
              </Button>
              <Button variant="outline" size="sm" testId="review-suspend" disabled={submitting} onclick={() => void suspend()}>
                {$t('review.suspend')}
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
    {feedback ? $t('review.shortcuts_result') : gradedKind ? (gradedRevealed ? $t('review.shortcuts_give_up') : $t('review.shortcuts_graded')) : revealed ? $t('review.shortcuts') : $t('review.shortcuts_reveal')}{lastUndo ? ` · ${$t('review.shortcuts_undo')}` : ''}
  </p>
</Page>

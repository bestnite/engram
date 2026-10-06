<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { DueCard } from '../api';

  let cards = $state<DueCard[]>([]);
  let loading = $state(true);
  let error = $state<unknown>(null);
  let revealed = $state(false);
  let submitting = $state(false);
  let done = $state(0);
  let remaining = $state(0);
  let startedAt = Date.now();
  const current = $derived(cards[0] || null);
  const selfAssessable = $derived(current !== null && ['basic', 'basic_both', 'cloze', 'list', 'short_answer'].includes(current.kind));
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

  function selectedDecks(): number[] | undefined {
    const search = new URLSearchParams(window.location.search);
    const values = search.getAll('deck').flatMap((raw) => raw.split(',')).filter(Boolean).map(Number);
    return values.length ? values : undefined;
  }

  async function loadQueue(): Promise<void> {
    loading = true;
    error = null;
    revealed = false;
    try {
      const query = selectedDecks();
      const response = await apiClient.getDueCards(query ? { deck: query, limit: 500 } : { limit: 500 });
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
      const response = await apiClient.submitSelfReview({
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

  function onKeydown(event: KeyboardEvent): void {
    if (event.altKey || event.ctrlKey || event.metaKey || event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement) return;
    if (event.key === ' ' || event.key === 'Enter') {
      event.preventDefault();
      if (!revealed) revealed = true;
      return;
    }
    if (revealed && /^[1-4]$/.test(event.key)) rate(Number(event.key));
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
        {/if}
      </article>
    {/if}
  </div>
  <p class="mt-4 text-center text-xs text-zinc-500 dark:text-zinc-400">{$t('review.spa.shortcuts')}</p>
</section>

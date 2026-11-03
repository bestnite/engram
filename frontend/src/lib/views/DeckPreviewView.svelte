<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import type { DeckPreviewCard, DeckPreviewSort } from '../api';
  import { routeStore } from '../router';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import Button from '../components/ui/Button.svelte';
  import Select from '../components/ui/Select.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import EmptyState from '../components/ui/EmptyState.svelte';
  import CardFrame, { type CardFrameKey } from '../components/CardFrame.svelte';
  import { REVIEW_BACK_CSS, REVIEW_FRONT_CSS, faceStyle } from '../card-frame';
  import { ChevronLeft, ChevronRight, Layers } from '@lucide/svelte';

  /**
   * Deck content preview: every card of one deck with both rendered faces, front above back, and a
   * previous/next cursor. The faces are the review renderer's, so the page shows the same answer
   * controls, audio, TTS and image-occlusion behaviour as the review page; nothing here is graded.
   *
   * The cursor is a position in the whole deck, and one request fetches the request-sized page that
   * contains it. Moving inside a fetched page is local; crossing a page boundary fetches the next
   * one. The order can switch between creation order and note type grouping, which resets the
   * cursor because the card at position N changes.
   */

  const deckId = $derived($routeStore.params.id || '');
  /** Cards per request; the server caps the value at its own maximum. */
  const perPage = 20;

  let sort = $state<DeckPreviewSort>('created');
  // Zero-based position across the whole deck, not inside the fetched page.
  let position = $state(0);
  // Pages already fetched, keyed by their 1-based page number, so moving back inside the loaded
  // window never refetches.
  let pages = $state<Record<number, DeckPreviewCard[]>>({});
  let total = $state(0);
  let loading = $state(true);
  let failed = $state(false);
  // Discards a response that belongs to an older sort or page after the cursor already moved on.
  let requestToken = 0;

  const pageOf = (index: number) => Math.floor(index / perPage) + 1;
  const card = $derived(pages[pageOf(position)]?.[position % perPage] ?? null);
  const atStart = $derived(position <= 0);
  const atEnd = $derived(total === 0 || position >= total - 1);

  async function load(page: number): Promise<void> {
    const token = ++requestToken;
    loading = true;
    failed = false;
    try {
      const response = await apiClient.deckPreviewCards(deckId, { page, perPage, sort }, { frame: true });
      if (token !== requestToken) return;
      pages = { ...pages, [page]: response.cards };
      total = response.total;
    } catch {
      if (token === requestToken) failed = true;
    } finally {
      if (token === requestToken) loading = false;
    }
  }

  onMount(() => {
    if (deckId) void load(1);
  });

  function changeSort(value: string): void {
    const next: DeckPreviewSort = value === 'note_type' ? 'note_type' : 'created';
    if (next === sort) return;
    sort = next;
    position = 0;
    pages = {};
    total = 0;
    void load(1);
  }

  async function move(delta: number): Promise<void> {
    const next = position + delta;
    if (next < 0 || next >= total) return;
    position = next;
    const page = pageOf(next);
    if (!pages[page]) await load(page);
  }

  function onFrameKey(message: CardFrameKey): void {
    if (message.key === 'ArrowLeft') void move(-1);
    if (message.key === 'ArrowRight') void move(1);
  }

  function onWindowKey(event: KeyboardEvent): void {
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
    const target = event.target as HTMLElement | null;
    if (
      target &&
      (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable)
    ) {
      return;
    }
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      void move(-1);
    }
    if (event.key === 'ArrowRight') {
      event.preventDefault();
      void move(1);
    }
  }

  const sortOptions = $derived([
    { value: 'created', label: $t('deck.preview.sort.created') },
    { value: 'note_type', label: $t('deck.preview.sort.note_type') },
  ]);
</script>

<svelte:window onkeydown={onWindowKey} />

<Page>
  <PageHeader
    title={$t('deck.preview.title')}
    back={{ href: `/decks/${encodeURIComponent(deckId)}`, label: $t('deck.preview.back'), testId: 'preview-back-to-deck' }}
    testId="preview-title"
  >
    {#snippet meta()}
      {#if total > 0}
        <p data-testid="preview-position" class="text-[13px] tabular-nums text-muted-foreground">
          {$t('deck.preview.position', { position: position + 1, total })}
        </p>
      {/if}
    {/snippet}
    {#snippet actions()}
      <div class="flex items-center gap-2 text-sm text-muted-foreground">
        <span>{$t('deck.preview.sort')}</span>
        <Select
          class="w-44"
          testId="preview-sort"
          value={sort}
          options={sortOptions}
          ariaLabel={$t('deck.preview.sort')}
          allowDeselect={false}
          onValueChange={changeSort}
        />
      </div>
    {/snippet}
  </PageHeader>

  {#if failed}
    <div class="rounded-lg border border-border p-6 text-center" data-testid="preview-failed">
      <p class="text-sm text-muted-foreground">{$t('deck.preview.failed')}</p>
      <Button class="mt-3" variant="outline" onclick={() => void load(pageOf(position))} testId="preview-retry">
        {$t('deck.preview.retry')}
      </Button>
    </div>
  {:else if loading && !card}
    <Skeleton testId="preview-loading" label={$t('deck.preview.title')} variant="lines" lines={6} />
  {:else if total === 0}
    <EmptyState testId="preview-empty" icon={Layers} text={$t('deck.preview.empty')} />
  {:else if card}
    <div class="space-y-4" data-testid="preview-card">
      <p class="text-[13px] text-muted-foreground" data-testid="preview-meta">
        {$t('deck.preview.meta', { note_type: card.note_type, template: card.template })}
      </p>
      <section class="space-y-2" aria-label={$t('deck.preview.side.front')}>
        <h2 class="text-sm font-medium text-muted-foreground">{$t('deck.preview.side.front')}</h2>
        <CardFrame
          html={card.front.html}
          css={faceStyle(REVIEW_FRONT_CSS, card.front.css)}
          bodyClass={card.front.body_class}
          side="question"
          mediaBase={card.front.media_base}
          autoplay={card.front.autoplay ?? false}
          avTags={card.front.av_tags ?? []}
          ordinal={card.ordinal}
          title={$t('deck.preview.side.front')}
          focusAnswer={false}
          onkey={onFrameKey}
        />
      </section>
      <section class="space-y-2" aria-label={$t('deck.preview.side.back')}>
        <h2 class="text-sm font-medium text-muted-foreground">{$t('deck.preview.side.back')}</h2>
        <CardFrame
          html={card.back.html}
          css={faceStyle(REVIEW_BACK_CSS, card.back.css)}
          bodyClass={card.back.body_class}
          side="answer"
          mediaBase={card.back.media_base}
          autoplay={card.back.autoplay ?? false}
          avTags={card.back.av_tags ?? []}
          ordinal={card.ordinal}
          title={$t('deck.preview.side.back')}
          focusAnswer={false}
          onkey={onFrameKey}
        />
      </section>
    </div>
    <div class="mt-6 flex items-center justify-between gap-3">
      <Button
        variant="outline"
        size="lg"
        disabled={atStart}
        onclick={() => void move(-1)}
        testId="preview-prev"
        label={$t('deck.preview.previous')}
      >
        <ChevronLeft class="size-4" aria-hidden="true" />
        <span>{$t('deck.preview.previous')}</span>
      </Button>
      <Button
        variant="outline"
        size="lg"
        disabled={atEnd}
        onclick={() => void move(1)}
        testId="preview-next"
        label={$t('deck.preview.next')}
      >
        <span>{$t('deck.preview.next')}</span>
        <ChevronRight class="size-4" aria-hidden="true" />
      </Button>
    </div>
  {/if}
</Page>

<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { formatNumber } from '../format';
  import { apiClient } from '../api';
  import type { DeckPreviewCard, DeckPreviewNoteTypeSection, DeckPreviewSort } from '../api';
  import { routeStore } from '../router';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import Button from '../components/ui/Button.svelte';
  import Select from '../components/ui/Select.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import EmptyState from '../components/ui/EmptyState.svelte';
  import { toast } from '../components/ui/toast';
  import CardFrame, { type CardFrameKey } from '../components/CardFrame.svelte';
  import { REVIEW_BACK_CSS, REVIEW_FRONT_CSS, faceStyle } from '../card-frame';
  import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Layers } from '@lucide/svelte';

  /**
   * Deck content preview: every card of one deck with both rendered faces, front above back, and a
   * cursor that moves card by card or note by note. The faces are the review renderer's, so the page
   * shows the same answer controls, audio, TTS and image-occlusion behaviour as the review page;
   * nothing here is graded.
   *
   * The cursor is a position in the whole deck, and one request fetches the page that contains it.
   * Moving inside a fetched page is local; crossing a page boundary fetches the next one. The order
   * can switch between creation order and note type grouping, which resets the cursor because the
   * card at position N changes. The header carries two jumps: the card number, and the note type
   * selector that the note type order adds, whose options are the server's grouping of the deck.
   * A note step skips the remaining cards of a multi-card note; the server resolves where it lands
   * because the boundary can cross a page.
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
  // The note type grouping of the deck; only the note type order sends it.
  let noteTypes = $state<DeckPreviewNoteTypeSection[]>([]);
  // What the reader typed into the card number field; null mirrors the current position.
  let jumpDraft = $state<string | null>(null);
  // True while a note step is being resolved, so a second click cannot start another step.
  let noteStepping = $state(false);
  // A note step that found no other note in one direction disables that direction until the cursor
  // moves again.
  let noPreviousNote = $state(false);
  let noNextNote = $state(false);

  const pageOf = (index: number) => Math.floor(index / perPage) + 1;
  const card = $derived(pages[pageOf(position)]?.[position % perPage] ?? null);
  const atStart = $derived(position <= 0);
  const atEnd = $derived(total === 0 || position >= total - 1);
  // The card number field shows the draft while the reader types and the cursor otherwise.
  const jumpValue = $derived(jumpDraft ?? String(position + 1));
  const typeOptions = $derived(
    noteTypes.map((section) => ({
      value: section.name,
      label: $t('deck.preview.type.option', {
        name: section.name,
        cards: formatNumber(section.count, $localeStore),
      }),
    })),
  );
  const backNoteBlocked = $derived(atStart || noPreviousNote || noteStepping);
  const forwardNoteBlocked = $derived(atEnd || noNextNote || noteStepping);

  async function load(page: number): Promise<void> {
    const token = ++requestToken;
    loading = true;
    failed = false;
    try {
      const response = await apiClient.deckPreviewCards(deckId, { page, perPage, sort }, { frame: true });
      if (token !== requestToken) return;
      pages = { ...pages, [page]: response.cards };
      total = response.total;
      noteTypes = response.note_types ?? [];
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
    noteTypes = [];
    jumpDraft = null;
    noPreviousNote = false;
    noNextNote = false;
    void load(1);
  }

  /** Moves the cursor to a zero-based position, fetching its page when that page is not cached. */
  async function goTo(next: number): Promise<void> {
    position = next;
    jumpDraft = null;
    noPreviousNote = false;
    noNextNote = false;
    const page = pageOf(next);
    if (!pages[page]) await load(page);
  }

  async function move(delta: number): Promise<void> {
    const next = position + delta;
    if (next < 0 || next >= total) return;
    await goTo(next);
  }

  /**
   * Jumps to the card number the reader typed. An out-of-range number clamps to the deck, junk
   * restores the cursor, and a value that equals the cursor keeps it. Both Enter and leaving the
   * field commit, so typing a number and clicking elsewhere still lands on that card.
   */
  function commitJump(): void {
    const draft = jumpDraft;
    if (draft === null) return;
    jumpDraft = null;
    const typed = Number.parseInt(draft, 10);
    if (!Number.isFinite(typed)) return;
    const target = Math.min(Math.max(typed, 1), total) - 1;
    if (target !== position) void goTo(target);
  }

  function onJumpKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      jumpDraft = null;
      return;
    }
    if (event.key !== 'Enter') return;
    event.preventDefault();
    commitJump();
  }

  /** Jumps to the first card of one note type's block. */
  function jumpToType(name: string): void {
    const section = noteTypes.find((entry) => entry.name === name);
    if (!section) return;
    void goTo(section.first_position - 1);
  }

  /**
   * Steps to the first card of the adjacent note. The server answers where that card is because the
   * boundary can cross a page; a null answer means the deck has no other note in that direction.
   */
  async function stepNote(forward: boolean): Promise<void> {
    if (noteStepping || total === 0 || !card) return;
    const from = position + 1;
    const requestSort = sort;
    noteStepping = true;
    try {
      const response = await apiClient.deckPreviewNoteStep(deckId, {
        from,
        direction: forward ? 'next' : 'prev',
        sort: requestSort,
      });
      // The cursor or the order changed while the request was in flight, so this answer no longer
      // applies: a position means something else in another order.
      if (sort !== requestSort || position + 1 !== from) return;
      if (response.position === null) {
        if (forward) noNextNote = true;
        else noPreviousNote = true;
        return;
      }
      await goTo(response.position - 1);
    } catch {
      toast.error($t('deck.preview.step_failed'));
    } finally {
      noteStepping = false;
    }
  }

  function onFrameKey(message: CardFrameKey): void {
    if (message.key === 'ArrowLeft') {
      if (message.shift) void stepNote(false);
      else void move(-1);
    }
    if (message.key === 'ArrowRight') {
      if (message.shift) void stepNote(true);
      else void move(1);
    }
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
      if (event.shiftKey) void stepNote(false);
      else void move(-1);
    }
    if (event.key === 'ArrowRight') {
      event.preventDefault();
      if (event.shiftKey) void stepNote(true);
      else void move(1);
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
        <div class="flex items-center gap-1.5 text-[13px] text-muted-foreground">
          <label for="preview-jump" class="cursor-text">{$t('deck.preview.jump.label')}</label>
          <input
            id="preview-jump"
            data-testid="preview-jump"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            enterkeyhint="go"
            class="field-input h-9 w-20 text-center text-[13px] tabular-nums"
            value={jumpValue}
            oninput={(event) => (jumpDraft = event.currentTarget.value)}
            onkeydown={onJumpKeydown}
            onblur={commitJump}
          />
          <span data-testid="preview-position" class="tabular-nums">
            {$t('deck.preview.total', { total: formatNumber(total, $localeStore) })}
          </span>
        </div>
      {/if}
    {/snippet}
    {#snippet actions()}
      <div class="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
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
        {#if sort === 'note_type' && noteTypes.length > 0}
          <span class="ms-2">{$t('deck.preview.type.label')}</span>
          <Select
            class="w-52"
            testId="preview-type"
            value={card?.note_type ?? ''}
            options={typeOptions}
            placeholder={$t('deck.preview.type.label')}
            ariaLabel={$t('deck.preview.type.aria')}
            allowDeselect={false}
            onValueChange={jumpToType}
          />
        {/if}
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
    <div class="mt-6 space-y-2">
      <div class="flex items-center justify-between gap-3">
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
      <div class="flex items-center justify-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          disabled={backNoteBlocked}
          onclick={() => void stepNote(false)}
          testId="preview-note-prev"
          label={$t('deck.preview.note.previous')}
        >
          <ChevronsLeft class="size-4" aria-hidden="true" />
          <span>{$t('deck.preview.note.previous')}</span>
        </Button>
        <Button
          variant="ghost"
          size="sm"
          disabled={forwardNoteBlocked}
          onclick={() => void stepNote(true)}
          testId="preview-note-next"
          label={$t('deck.preview.note.next')}
        >
          <span>{$t('deck.preview.note.next')}</span>
          <ChevronsRight class="size-4" aria-hidden="true" />
        </Button>
      </div>
    </div>
  {/if}
</Page>

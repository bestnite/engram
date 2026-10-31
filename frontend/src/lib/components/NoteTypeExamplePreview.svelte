<script lang="ts">
  /**
   * The rendered cards of one worked example on the note type help page.
   *
   * The example is sent to the server as an unsaved note type with a sample note, so the faces come from the same renderer
   * and card generation a real note goes through: a conditional template that stays empty produces no card, and a cloze
   * produces one card per number. The faces only enter the sandboxed card frame. The request waits until the example is
   * near the viewport, because the page holds many examples and each shown card mounts two frames.
   */
  import { onMount } from 'svelte';
  import { ChevronLeft, ChevronRight, RefreshCw } from '@lucide/svelte';
  import { t } from '../i18n';
  import { ApiClientError, type ApiClient, type NotePreviewCard, type NoteTypeConfig } from '../api';
  import { NOTE_PREVIEW_CSS, faceStyle } from '../card-frame';
  import CardFrame from './CardFrame.svelte';
  import Skeleton from './ui/Skeleton.svelte';
  import Button from './ui/Button.svelte';

  interface Props {
    client: ApiClient;
    deckId: string;
    config: NoteTypeConfig;
    /** The sample note, keyed by field name. */
    fields: Record<string, string>;
    tags?: string[];
    testId?: string;
  }

  let { client, deckId, config, fields, tags = [], testId }: Props = $props();

  type Status = 'idle' | 'loading' | 'ready' | 'unavailable' | 'failed';
  let status = $state<Status>('idle');
  let cards = $state<NotePreviewCard[]>([]);
  let index = $state(0);
  let root = $state<HTMLElement | null>(null);

  const count = $derived(cards.length);
  const shown = $derived(cards[Math.min(index, Math.max(count - 1, 0))] ?? null);

  async function load(): Promise<void> {
    status = 'loading';
    try {
      const response = await client.previewNoteDraft(deckId, config, fields, tags, { frame: true });
      cards = response.cards;
      index = 0;
      status = 'ready';
    } catch (cause) {
      // The preview endpoint belongs to the deck's editors; a reader is told why there is no preview instead of being
      // offered a retry that cannot succeed.
      status = cause instanceof ApiClientError && cause.isForbidden ? 'unavailable' : 'failed';
    }
  }

  function cardName(card: NotePreviewCard): string {
    return card.cloze_ordinal > 0
      ? $t('note_preview.cloze_name', { template: card.template, ordinal: card.cloze_ordinal })
      : card.template;
  }

  /** Steps by one card; the buttons are disabled at both ends, so the index never wraps. */
  function step(delta: number): void {
    index = Math.min(Math.max(index + delta, 0), Math.max(count - 1, 0));
  }

  onMount(() => {
    const Observer = typeof IntersectionObserver === 'function' ? IntersectionObserver : null;
    if (!Observer || !root) {
      void load();
      return;
    }
    const observer = new Observer(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return;
        observer.disconnect();
        void load();
      },
      { rootMargin: '400px 0px' },
    );
    observer.observe(root);
    return () => observer.disconnect();
  });

  const pagerButton =
    'inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-default disabled:opacity-40 disabled:hover:bg-transparent';
</script>

<div bind:this={root} class="space-y-2" data-testid={testId} data-status={status}>
  <div class="flex min-h-7 items-center justify-between gap-2">
    <span class="text-xs text-muted-foreground">{$t('notetype_help.label.preview')}</span>
    <!-- One card at a time, as in the note editor: the bar names the shown card and steps through the cards. -->
    {#if shown && count > 1}
      <div class="flex min-w-0 items-center gap-1" data-testid="note-type-help-preview-pager">
        <button
          type="button"
          class={pagerButton}
          aria-label={$t('note_preview.prev')}
          title={$t('note_preview.prev')}
          disabled={index === 0}
          onclick={() => step(-1)}
          data-testid="note-type-help-preview-prev"
        >
          <ChevronLeft class="size-4" aria-hidden="true" />
        </button>
        <p class="min-w-0 truncate text-xs" aria-live="polite">
          <span class="font-medium text-foreground tabular-nums">{$t('note_preview.position', { current: index + 1, total: count })}</span>
          <span class="text-muted-foreground" aria-hidden="true"> · </span>
          <span class="text-muted-foreground" data-testid="note-type-help-preview-card-name">{cardName(shown)}</span>
        </p>
        <button
          type="button"
          class={pagerButton}
          aria-label={$t('note_preview.next')}
          title={$t('note_preview.next')}
          disabled={index >= count - 1}
          onclick={() => step(1)}
          data-testid="note-type-help-preview-next"
        >
          <ChevronRight class="size-4" aria-hidden="true" />
        </button>
      </div>
    {/if}
  </div>

  {#if status === 'idle' || status === 'loading'}
    <Skeleton testId="note-type-help-preview-loading" label={$t('note_preview.loading')} lines={2} />
  {:else if status === 'unavailable'}
    <p class="text-xs text-muted-foreground" data-testid="note-type-help-preview-unavailable">{$t('notetype_help.preview.unavailable')}</p>
  {:else if status === 'failed'}
    <div class="flex items-center gap-3 text-xs text-muted-foreground" data-testid="note-type-help-preview-failed">
      <span>{$t('note_preview.failed')}</span>
      <Button variant="outline" size="sm" onclick={() => void load()}>
        <RefreshCw class="size-3.5" aria-hidden="true" />
        <span>{$t('common.retry')}</span>
      </Button>
    </div>
  {:else if shown}
    {@const card = shown}
    <div class="grid gap-2 sm:grid-cols-2">
      <div class="min-w-0 space-y-1.5 rounded-md border border-border bg-background p-3" data-testid="note-type-help-preview-front">
        <div class="text-xs text-muted-foreground">{$t('note_preview.front')}</div>
        <CardFrame
          html={card.front.html}
          css={faceStyle(NOTE_PREVIEW_CSS, card.front.css)}
          bodyClass={card.front.body_class}
          side="question"
          mediaBase={card.front.media_base}
          avTags={card.front.av_tags ?? []}
          title={$t('note_preview.front')}
          focusAnswer={false}
        />
      </div>
      <div class="min-w-0 space-y-1.5 rounded-md border border-border bg-background p-3" data-testid="note-type-help-preview-back">
        <div class="text-xs text-muted-foreground">{$t('note_preview.back')}</div>
        <CardFrame
          html={card.back.html}
          css={faceStyle(NOTE_PREVIEW_CSS, card.back.css)}
          bodyClass={card.back.body_class}
          side="answer"
          mediaBase={card.back.media_base}
          avTags={card.back.av_tags ?? []}
          title={$t('note_preview.back')}
        />
      </div>
    </div>
  {/if}
</div>

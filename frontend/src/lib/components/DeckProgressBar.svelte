<script lang="ts">
  import { Tooltip } from 'bits-ui';
  import { t } from '../i18n';
  import { cn } from './ui/utils';

  /**
   * A deck's study progress as a bar: studied cards over studied plus remaining new cards.
   * The exact numbers ("10/100") live in a hover tooltip and in aria-valuetext, so every row keeps the same width
   * whatever the counts are. Touch devices get no hover, so a view that needs the numbers on a phone renders them itself.
   * The fill uses the foreground colour, matching the page's black, white and grey palette; a finished deck is a full bar.
   */
  interface Props {
    studied: number;
    remaining: number;
    testId?: string;
    class?: string;
  }

  let { studied, remaining, testId, class: klass = '' }: Props = $props();

  const total = $derived(studied + remaining);
  const value = $derived($t('deck_progress.value', { studied, total }));
</script>

<Tooltip.Provider delayDuration={150}>
  <Tooltip.Root>
    <Tooltip.Trigger>
      {#snippet child({ props })}
        <!-- The trigger is a div, not the default button: on the home list it sits inside the row link, and a click on the bar
             must still reach that link. It is not focusable either, so a row stays one tab stop; keyboard and screen reader
             users get the numbers from aria-valuetext. The vertical padding widens the hover target beyond the 6px bar. -->
        {@const { tabindex: _tabindex, type: _type, ...triggerProps } = props}
        <div {...triggerProps} class={cn('-my-2 shrink-0 py-2', klass)}>
          <div
            role="progressbar"
            data-testid={testId}
            aria-label={$t('deck_progress.label')}
            aria-valuemin={0}
            aria-valuemax={total}
            aria-valuenow={studied}
            aria-valuetext={value}
            class="h-1.5 w-full overflow-hidden rounded-full bg-border"
          >
            <div
              class="h-full rounded-full bg-foreground transition-[width] duration-300"
              style:width="{total > 0 ? (studied / total) * 100 : 0}%"
            ></div>
          </div>
        </div>
      {/snippet}
    </Tooltip.Trigger>
    <Tooltip.Portal>
      <Tooltip.Content
        sideOffset={6}
        class="z-50 rounded-md bg-primary px-2 py-1 text-xs tabular-nums text-primary-foreground shadow-md data-[state=delayed-open]:animate-in data-[state=delayed-open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0"
      >
        {value}
      </Tooltip.Content>
    </Tooltip.Portal>
  </Tooltip.Root>
</Tooltip.Provider>

<script lang="ts">
  import { Popover } from 'bits-ui';
  import { CircleHelp } from '@lucide/svelte';
  import { t } from '../../i18n';
  import { cn } from './utils';

  /**
   * The "what does this mean?" help next to a metric: a small question-mark button that opens a popover with a sentence or two on how it is counted.
   *
   * Uses bits-ui Popover instead of Tooltip: there is no hover on phones, and a hover-only tip cannot be opened on touch at all;
   * Popover opens on tap and closes on outside click or Esc, and the trigger is a native button, so Enter/Space works naturally.
   * Pointer devices additionally get hover-to-open (the library ignores hover on touch events), so on desktop a glance is enough to see the explanation.
   */
  interface Props {
    /** The localized explanation body. */
    text: string;
    /** The name of the metric being explained (localized); spliced into the button's aria-label so screen readers know which item is being asked about. */
    label: string;
    testId?: string;
    class?: string;
  }

  let { text, label, testId, class: klass = '' }: Props = $props();

  let open = $state(false);
</script>

<Popover.Root bind:open>
  <Popover.Trigger
    type="button"
    data-testid={testId}
    aria-label={$t('help.trigger', { label })}
    openOnHover
    openDelay={250}
    class={cn(
      // Visually just a 14px icon, but the button itself is 24px so fingers can hit it; a negative margin cancels the extra height so it does not stretch the row.
      'inline-flex size-6 -my-1 shrink-0 cursor-pointer items-center justify-center rounded-full align-middle text-muted-foreground/70 transition-colors hover:text-foreground data-[state=open]:text-foreground',
      klass
    )}
  >
    <CircleHelp class="size-3.5" aria-hidden="true" />
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      aria-label={label}
      sideOffset={4}
      collisionPadding={16}
      class="z-50 w-max max-w-[min(20rem,calc(100vw-2rem))] rounded-lg border border-border bg-popover px-3 py-2 text-left text-xs leading-relaxed font-normal text-foreground shadow-lg shadow-black/5 outline-hidden data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95"
    >
      <!-- pre-line：多条规则的说明用换行分条，单句说明不受影响。 -->
      <p class="whitespace-pre-line" data-testid={testId ? `${testId}-text` : undefined}>{text}</p>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>

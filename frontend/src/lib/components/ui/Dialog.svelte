<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Dialog as BitsDialog } from 'bits-ui';
  import { cn } from './utils';

  /**
   * The site's only modal dialog (interactive widgets use a mature component library).
   *
   * Previously 4 hand-written modals (create deck, delete deck, delete preset, export) each hand-rolled the overlay, ESC, focus and
   * click-outside-to-close, with behavior and styling never fully consistent. After switching the base to bits-ui's Dialog,
   * the focus trap, ESC/overlay closing, scroll locking and aria wiring are all handled by the library.
   */
  interface Props {
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    title: string;
    description?: string;
    /** Given separately from title: screen readers should read the heading first, then the description. */
    testId?: string;
    size?: 'sm' | 'md' | 'lg';
    class?: string;
    children: Snippet;
  }

  let { open = $bindable(false), onOpenChange, title, description, testId, size = 'md', class: klass = '', children }: Props = $props();

  const width = { sm: 'max-w-sm', md: 'max-w-md', lg: 'max-w-lg' } as const;
</script>

<BitsDialog.Root bind:open {onOpenChange}>
  <BitsDialog.Portal>
    <BitsDialog.Overlay
      class="fixed inset-0 z-50 bg-overlay backdrop-blur-[2px] data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0"
    />
    <BitsDialog.Content
      data-testid={testId}
      class={cn(
        'fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border border-border bg-popover p-6 text-foreground shadow-2xl shadow-black/10',
        // Enter/exit: fade in + a slight zoom, short enough not to slow the action but enough for the dialog to "appear" rather than "flash".
        'duration-150 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95',
        width[size],
        klass
      )}
    >
      <BitsDialog.Title class="text-base font-semibold text-foreground">
        {title}
      </BitsDialog.Title>
      {#if description}
        <BitsDialog.Description class="mt-1 text-sm text-muted-foreground">
          {description}
        </BitsDialog.Description>
      {/if}
      <div class="mt-4">
        {@render children()}
      </div>
    </BitsDialog.Content>
  </BitsDialog.Portal>
</BitsDialog.Root>

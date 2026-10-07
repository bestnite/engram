<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from './utils';
  import { badgeVariants } from './variants';

  interface Props {
    variant?: 'neutral' | 'info' | 'success' | 'warning' | 'danger';
    class?: string;
    testId?: string;
    children: Snippet;
  }

  let { variant = 'neutral', class: klass = '', testId, children }: Props = $props();
</script>

<span class={cn(badgeVariants({ variant }), klass)} data-testid={testId}>
  {@render children()}
</span>

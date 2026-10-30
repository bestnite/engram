<script lang="ts">
  import CardFrame from '../lib/components/CardFrame.svelte';

  // Test harness: `show` must be resent when a prop changes, which needs a parent that owns the prop.
  let { html = '<p>one</p>' }: { html?: string } = $props();
  // svelte-ignore state_referenced_locally
  let current = $state(html);
  const faceCss = '#qa{font-size:1rem}';
</script>

<CardFrame html={current} css={faceCss} bodyClass="card" side="question" mediaBase="/fm/token/" title="Card face" />
<button type="button" data-testid="harness-next" onclick={() => (current = '<p>two</p>')}>next</button>

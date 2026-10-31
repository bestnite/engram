<script lang="ts">
  import RichTextField from '../lib/components/RichTextField.svelte';

  // Test harness: the media base arrives after the field mounted, which needs a parent that owns the props.
  let value = $state('<p>start</p>');
  let mediaBase = $state('');
</script>

<RichTextField {value} label="Front" testId="field" {mediaBase} onvalue={(next) => (value = next)} />
<button type="button" data-testid="harness-base" onclick={() => (mediaBase = '/fm/token/')}>base</button>

<script lang="ts">
  import ClozeAlternativesEditor from '../lib/components/ClozeAlternativesEditor.svelte';

  let {
    textFieldValue = '{{c1::Paris}}',
    metaValue = '',
    onchange = () => {},
  }: {
    textFieldValue?: string;
    metaValue?: string;
    onchange?: (next: string) => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  let text = $state(textFieldValue);
  // svelte-ignore state_referenced_locally
  let meta = $state(metaValue);
</script>

<input data-testid="harness-text" value={text} oninput={(event) => (text = event.currentTarget.value)} />

<ClozeAlternativesEditor
  textFieldValue={text}
  metaValue={meta}
  {onchange}
/>

<button type="button" data-testid="harness-change-text" onclick={() => (text = '{{c1::Rome}}')}>
  Change text
</button>

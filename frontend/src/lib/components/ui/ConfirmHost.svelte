<script lang="ts">
  import { t } from '../../i18n';
  import Dialog from './Dialog.svelte';
  import Button from './Button.svelte';
  import { confirmRequest } from './confirm';

  /** askConfirm 的渲染端：读 confirmRequest，答复后清空。 */
  let open = $state(false);
  let current = $derived($confirmRequest);

  $effect(() => {
    open = current !== null;
  });

  function answer(ok: boolean): void {
    const request = current;
    confirmRequest.set(null);
    request?.resolve(ok);
  }
</script>

<Dialog
  bind:open
  onOpenChange={(next) => { if (!next && current) answer(false); }}
  title={current?.title ?? ''}
  description={current?.description}
  size="sm"
  testId="confirm-dialog"
>
  <div class="mt-2 flex items-center justify-end gap-2">
    <Button variant="outline" size="lg" testId="confirm-dialog-cancel" onclick={() => answer(false)}>{$t('common.cancel')}</Button>
    <Button variant={current?.destructive ? 'danger' : 'primary'} size="lg" testId="confirm-dialog-confirm" onclick={() => answer(true)}>
      {current?.confirmLabel ?? $t('common.confirm')}
    </Button>
  </div>
</Dialog>

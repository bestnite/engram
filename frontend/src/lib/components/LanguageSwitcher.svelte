<script lang="ts">
  import { Globe } from '@lucide/svelte';
  import { localeStore, t } from '../i18n';
  import Select from './ui/Select.svelte';
  import { switchLocale } from './language-switch';
  import { toast } from './ui/toast';

  /**
   * The language switcher in the logged-out header; see language-switch.ts for the switching logic (persistence target, rollback).
   *
   * The dropdown itself is ui/Select (bits-ui) rather than a native select element: with the native control, under both the light and dark themes
   * the appearance is decided by the system, and its height and colors never align with the other header controls.
   */
  const options = $derived([
    { value: 'zh-CN', label: $t('language.zh-CN') },
    { value: 'en', label: $t('language.en') },
  ]);

  let saving = $state(false);

  async function handleSelect(value: string) {
    saving = true;
    try {
      if (!(await switchLocale(value))) toast.error($t('language.save_failed'));
    } finally {
      saving = false;
    }
  }
</script>

<div class="inline-flex h-8 items-center gap-1.5 rounded-md border border-input bg-background px-2 transition-colors">
  <Globe class="size-3.5 shrink-0 opacity-70" aria-hidden="true" />
  <Select
    value={$localeStore}
    {options}
    onValueChange={handleSelect}
    disabled={saving}
    ariaLabel={$t('language.label')}
    testId="language-select"
    size="sm"
    class="w-20 border-0 bg-transparent px-0 hover:bg-transparent dark:bg-transparent dark:hover:bg-transparent"
  />
</div>


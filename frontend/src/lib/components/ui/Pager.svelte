<script lang="ts">
  import { t } from '../../i18n';
  import Button from './Button.svelte';
  import { ChevronLeft, ChevronRight } from '@lucide/svelte';

  /**
   * The pager below a table: right-aligned "page number / previous / next".
   * Several admin panel tables each had their own copy with different styling and disabled logic; this consolidates them into one place.
   */
  interface Props {
    page: number;
    pages: number;
    onPage: (page: number) => void;
    /** Generates the three data-testids `<prefix>-pager`, `<prefix>-prev` and `<prefix>-next`. */
    testIdPrefix: string;
  }

  let { page, pages, onPage, testIdPrefix }: Props = $props();
</script>

<div class="mt-3 flex items-center justify-end gap-2 text-[13px] text-muted-foreground" data-testid="{testIdPrefix}-pager">
  <span class="tabular-nums">{page} / {pages}</span>
  <Button testId="{testIdPrefix}-prev" variant="outline" size="sm" disabled={page <= 1} onclick={() => onPage(page - 1)}><ChevronLeft class="size-4" aria-hidden="true" /><span>{$t('admin.common.prev')}</span></Button>
  <Button testId="{testIdPrefix}-next" variant="outline" size="sm" disabled={page >= pages} onclick={() => onPage(page + 1)}><span>{$t('admin.common.next')}</span><ChevronRight class="size-4" aria-hidden="true" /></Button>
</div>

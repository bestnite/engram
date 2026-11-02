<script lang="ts">
  import { t } from '../i18n';
  import Badge from './ui/Badge.svelte';
  import Button from './ui/Button.svelte';
  import Select from './ui/Select.svelte';
  import Dialog from './ui/Dialog.svelte';
  import {
    extractPhysicalBlanks,
    groupPhysicalBlanks,
    parseClozeMetadata,
    computeUnlinkedAlternatives,
    serializeClozeMetadata,
    reassociateUnlinked,
    deleteUnlinked,
    normalizeAnswer,
    createDict,
    isEntryInvalid,
    isGroupInvalid,
    repairInvalidGroup,
    repairInvalidEntry,
    type BlankGroup,
    type ParsedClozeMetadata,
  } from '../cloze-alternatives';
  import { TriangleAlert, Plus, X, RotateCcw } from '@lucide/svelte';

  interface Props {
    textFieldValue: string;
    metaValue: string;
    onchange: (nextMeta: string) => void;
  }

  let { textFieldValue, metaValue, onchange }: Props = $props();

  const blanks = $derived(extractPhysicalBlanks(textFieldValue));
  const activeGroups = $derived(groupPhysicalBlanks(blanks));

  let lastSerialized = $state('');
  // svelte-ignore state_referenced_locally
  let lastIncomingMeta = $state(metaValue);
  // svelte-ignore state_referenced_locally
  let meta = $state<ParsedClozeMetadata>(parseClozeMetadata(metaValue));
  let metaVersion = $state(0);

  $effect(() => {
    if (metaValue !== lastIncomingMeta) {
      lastIncomingMeta = metaValue;
      if (metaValue !== lastSerialized) {
        meta = parseClozeMetadata(metaValue);
        metaVersion++;
      }
    }
  });

  const unlinked = $derived.by(() => {
    void metaVersion;
    return computeUnlinkedAlternatives(meta, activeGroups);
  });
  const unlinkedInvalidGroups = $derived.by(() => {
    void metaVersion;
    return Object.keys(meta.invalidGroups).filter((num) => !activeGroups.some((group) => String(group.clozeOrdinal) === num));
  });

  let resetConfirmOpen = $state(false);

  // Track inputs and errors per group key
  let candidateInputs = $state<Record<string, string>>({});
  let candidateErrors = $state<Record<string, string>>({});
  let composing = $state(false);
  let reassociateError = $state('');

  function emitUpdate(): void {
    metaVersion++;
    const nextStr = serializeClozeMetadata(meta);
    lastSerialized = nextStr;
    onchange(nextStr);
  }

  function handleResetMalformed(): void {
    const unknownRootKeys = meta.invalidCapability !== undefined ? meta.unknownRootKeys : createDict();
    meta = {
      valid: true,
      raw: '',
      unknownRootKeys,
      invalidGroups: createDict(),
      invalidEntries: createDict(),
      clozeAccept: createDict(),
    };
    resetConfirmOpen = false;
    emitUpdate();
  }

  function addAlternative(group: BlankGroup): void {
    if (isGroupInvalid(meta, String(group.clozeOrdinal)) || isEntryInvalid(meta, String(group.clozeOrdinal), group.canonical)) return;
    const inputVal = (candidateInputs[group.key] || '').trim();
    if (!inputVal) return;

    // Check duplicate with canonical
    if (normalizeAnswer(inputVal) === normalizeAnswer(group.canonical)) {
      candidateErrors[group.key] = 'note_edit.cloze_alternatives.err_canonical';
      return;
    }

    const numKey = String(group.clozeOrdinal);
    if (!meta.clozeAccept[numKey]) {
      meta.clozeAccept[numKey] = createDict<string[]>();
    }
    const existing = meta.clozeAccept[numKey][group.canonical] || [];

    // Check duplicate with existing alternatives
    if (existing.some((alt) => normalizeAnswer(alt) === normalizeAnswer(inputVal))) {
      candidateErrors[group.key] = 'note_edit.cloze_alternatives.err_duplicate';
      return;
    }

    candidateErrors[group.key] = '';
    meta.clozeAccept[numKey][group.canonical] = [...existing, inputVal];
    candidateInputs[group.key] = '';
    emitUpdate();
  }

  function removeAlternative(group: BlankGroup, index: number): void {
    const numKey = String(group.clozeOrdinal);
    if (isGroupInvalid(meta, numKey) || isEntryInvalid(meta, numKey, group.canonical)) return;
    const existing = meta.clozeAccept[numKey]?.[group.canonical] || [];
    const next = [...existing];
    next.splice(index, 1);
    if (!meta.clozeAccept[numKey]) {
      meta.clozeAccept[numKey] = createDict<string[]>();
    }
    meta.clozeAccept[numKey][group.canonical] = next;
    emitUpdate();
  }

  function handleRepairEntry(group: BlankGroup): void {
    const numKey = String(group.clozeOrdinal);
    repairInvalidEntry(meta, numKey, group.canonical);
    emitUpdate();
  }

  function onKeyDown(event: KeyboardEvent, group: BlankGroup): void {
    if (event.key === 'Enter') {
      event.preventDefault();
      if (composing || event.isComposing || event.keyCode === 229) return;
      addAlternative(group);
    }
  }

  function handleReassociate(unlinkedItem: { clozeNumber: string; canonical: string; alternatives: string[] }, targetKey: string): void {
    const targetGroup = activeGroups.find((g) => g.key === targetKey);
    if (!targetGroup) return;
    if (isEntryInvalid(meta, unlinkedItem.clozeNumber, unlinkedItem.canonical)) {
      reassociateError = 'note_edit.cloze_alternatives.err_reassociate_invalid_source';
      return;
    }
    try {
      reassociateUnlinked(meta, unlinkedItem, targetGroup);
      reassociateError = '';
      emitUpdate();
    } catch {
      reassociateError = 'note_edit.cloze_alternatives.err_reassociate_invalid_target';
    }
  }

  function handleDeleteUnlinked(unlinkedItem: { clozeNumber: string; canonical: string; alternatives: string[] }): void {
    deleteUnlinked(meta, unlinkedItem);
    emitUpdate();
  }
</script>

<div class="rounded-lg border border-border bg-card p-4 text-card-foreground shadow-sm" data-testid="cloze-alternatives-editor">
  <div class="mb-3">
    <h3 class="text-sm font-semibold text-foreground">{$t('note_edit.cloze_alternatives.title')}</h3>
    <p class="mt-0.5 text-xs text-muted-foreground">{$t('note_edit.cloze_alternatives.hint')}</p>
  </div>

  {#if !meta.valid}
    <div class="rounded-md border border-amber-500/20 bg-amber-500/10 p-3 text-sm text-amber-900 dark:text-amber-200" data-testid="cloze-alt-malformed-banner">
      <div class="flex items-start gap-2">
        <TriangleAlert class="size-4 shrink-0 text-amber-600 dark:text-amber-400 mt-0.5" />
        <div class="flex-1 min-w-0">
          <p class="text-xs font-medium">{$t('note_edit.cloze_alternatives.malformed_warning')}</p>
          <div class="mt-2">
            <Button
              variant="outline"
              size="sm"
              class="h-7 text-xs gap-1.5"
              testId="cloze-alt-reset-btn"
              onclick={() => { resetConfirmOpen = true; }}
            >
              <RotateCcw class="size-3" />
              <span>{$t(meta.invalidCapability !== undefined ? 'note_edit.cloze_alternatives.reset_capability' : 'note_edit.cloze_alternatives.malformed_reset')}</span>
            </Button>
          </div>
        </div>
      </div>
    </div>
  {:else if activeGroups.length === 0}
    <p class="text-xs text-muted-foreground py-2 italic">
      {$t('note_edit.cloze_alternatives.no_alternatives')}
    </p>
  {:else}
    {#key metaVersion}
      <div class="space-y-4">
        {#each activeGroups as group, groupIdx (group.key)}
          {@const alts = meta.clozeAccept[String(group.clozeOrdinal)]?.[group.canonical] ?? []}
          {@const err = candidateErrors[group.key]}
          <div class="rounded-md border border-border/80 bg-background/50 p-3" data-testid={`cloze-alt-group-${group.clozeOrdinal}-${groupIdx}`}>
            <div class="flex items-center gap-2">
              <Badge variant="neutral" class="font-mono text-xs">
                c{group.clozeOrdinal}
              </Badge>
              <span class="text-xs font-medium text-muted-foreground">{$t('note_edit.cloze_alternatives.canonical')}:</span>
              <span class="text-sm font-semibold text-foreground" data-testid="cloze-alt-canonical">
                {group.canonical}
              </span>
            </div>

            {#if group.occurrences.length >= 2}
              <div class="mt-2 rounded bg-muted/60 px-2.5 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-300" data-testid="cloze-alt-shared-notice">
                {group.occurrences.length === 2
                  ? $t('note_edit.cloze_alternatives.shared_notice_two')
                  : $t('note_edit.cloze_alternatives.shared_notice_other', { count: group.occurrences.length })}
              </div>
            {/if}

            {#if isEntryInvalid(meta, String(group.clozeOrdinal), group.canonical)}
              <div class="mt-2 rounded bg-amber-500/10 border border-amber-500/20 p-2 text-xs text-amber-900 dark:text-amber-200 flex items-center justify-between gap-2" data-testid="cloze-alt-invalid-entry-warning">
                <span>{$t('note_edit.cloze_alternatives.entry_invalid_warning')}</span>
                <Button
                  variant="outline"
                  size="sm"
                  class="h-6 text-xs text-foreground shrink-0"
                  testId="cloze-alt-repair-entry-btn"
                  onclick={() => handleRepairEntry(group)}
                >
                  {$t('note_edit.cloze_alternatives.repair_entry')}
                </Button>
              </div>
            {/if}
            {#if isGroupInvalid(meta, String(group.clozeOrdinal))}
              <div class="mt-2 rounded border border-amber-500/20 bg-amber-500/10 p-2 text-xs" data-testid="cloze-alt-invalid-group-warning">
                <span>{$t('note_edit.cloze_alternatives.group_invalid_warning')}</span>
                <Button variant="outline" size="sm" testId="cloze-alt-repair-group-btn" onclick={() => { repairInvalidGroup(meta, String(group.clozeOrdinal)); emitUpdate(); }}>
                  {$t('note_edit.cloze_alternatives.repair_group')}
                </Button>
              </div>
            {/if}

            <!-- Context lines per physical occurrence -->
            <ul class="mt-2 space-y-1 text-xs text-muted-foreground border-l-2 border-border/60 pl-2.5">
              {#each group.occurrences as occ (occ.occurrenceIndex)}
                <li class="flex flex-wrap items-center gap-1.5">
                  <span class="font-medium text-foreground/80">{$t('note_edit.cloze_alternatives.position', { index: occ.occurrenceIndex })}:</span>
                  <span class="font-mono text-[11px] text-muted-foreground">
                    "{occ.contextBefore}<strong class="text-foreground">[{group.canonical}]</strong>{occ.contextAfter}"
                  </span>
                  {#if occ.hint}
                    <span class="text-muted-foreground/70">({$t('note_edit.cloze_alternatives.hint_label')}: {occ.hint})</span>
                  {/if}
                </li>
              {/each}
            </ul>

            <!-- Existing alternative pills -->
            <div class="mt-3">
              {#if alts.length > 0}
                <div class="flex flex-wrap gap-1.5" data-testid={`cloze-alt-pills-${group.clozeOrdinal}-${groupIdx}`}>
                  {#each alts as alt, altIdx (altIdx)}
                    <span
                      class="inline-flex items-center gap-1 rounded-md bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground"
                      data-testid="cloze-alt-pill"
                    >
                      <span>{alt}</span>
                      <button
                        type="button"
                        class="text-muted-foreground hover:text-foreground cursor-pointer"
                         onclick={() => removeAlternative(group, altIdx)}
                        disabled={isGroupInvalid(meta, String(group.clozeOrdinal)) || isEntryInvalid(meta, String(group.clozeOrdinal), group.canonical)}
                        aria-label={$t('note_edit.cloze_alternatives.remove_pill')}
                        data-testid={`cloze-alt-remove-pill-${altIdx}`}
                      >
                        <X class="size-3" />
                      </button>
                    </span>
                  {/each}
                </div>
              {:else}
                <p class="text-xs text-muted-foreground/60 italic">{$t('note_edit.cloze_alternatives.no_alternatives')}</p>
              {/if}
            </div>

            <!-- Add new alternative -->
            <div class="mt-2.5 flex items-center gap-2">
              <input
                type="text"
                data-testid={`cloze-alt-input-${group.clozeOrdinal}-${groupIdx}`}
                value={candidateInputs[group.key] || ''}
                oninput={(e) => (candidateInputs[group.key] = e.currentTarget.value)}
                 onkeydown={(e) => onKeyDown(e, group)}
                oncompositionstart={() => (composing = true)}
                oncompositionend={() => (composing = false)}
                disabled={isGroupInvalid(meta, String(group.clozeOrdinal)) || isEntryInvalid(meta, String(group.clozeOrdinal), group.canonical)}
                placeholder={$t('note_edit.cloze_alternatives.add_placeholder')}
                aria-label={$t('note_edit.cloze_alternatives.add_placeholder')}
                class="field-input h-8 text-xs flex-1 max-w-sm"
              />
              <Button
                variant="outline"
                size="sm"
                class="h-8 text-xs gap-1"
                testId={`cloze-alt-add-btn-${group.clozeOrdinal}-${groupIdx}`}
                 onclick={() => addAlternative(group)}
                disabled={isGroupInvalid(meta, String(group.clozeOrdinal)) || isEntryInvalid(meta, String(group.clozeOrdinal), group.canonical)}
              >
                <Plus class="size-3.5" />
                <span>{$t('note_edit.cloze_alternatives.add_btn')}</span>
              </Button>
            </div>

            {#if err}
              <p class="mt-1 text-xs text-destructive-foreground font-medium" data-testid="cloze-alt-error">
                {$t(err)}
              </p>
            {/if}
          </div>
        {/each}
      </div>
    {/key}
  {/if}

  {#if meta.valid && unlinkedInvalidGroups.length > 0}
    <div class="mt-4 space-y-2" data-testid="cloze-alt-unlinked-invalid-groups">
      {#each unlinkedInvalidGroups as number (number)}
        <div class="rounded border border-amber-500/20 bg-amber-500/10 p-2 text-xs" data-testid="cloze-alt-unlinked-invalid-group">
          <span>c{number}: {$t('note_edit.cloze_alternatives.unlinked_invalid_group_warning')}</span>
          <Button variant="outline" size="sm" testId={`cloze-alt-remove-invalid-group-${number}`} onclick={() => { delete meta.invalidGroups[number]; emitUpdate(); }}>
            {$t('note_edit.cloze_alternatives.unlinked_delete')}
          </Button>
        </div>
      {/each}
    </div>
  {/if}

  <!-- Unlinked alternatives -->
  {#if meta.valid && unlinked.length > 0}
    <div class="mt-6 border-t border-border pt-4" data-testid="cloze-alt-unlinked-section">
      <h4 class="text-xs font-semibold text-foreground">{$t('note_edit.cloze_alternatives.unlinked_title')}</h4>
      <p class="mt-0.5 text-xs text-muted-foreground">{$t('note_edit.cloze_alternatives.unlinked_desc')}</p>
      {#if reassociateError}<p class="mt-2 text-xs text-destructive-foreground" role="alert">{$t(reassociateError)}</p>{/if}
      <div class="mt-3 space-y-2">
        {#each unlinked as unlinkedItem, unlinkedIdx (`${unlinkedItem.clozeNumber}:::${unlinkedItem.canonical}`)}
          <div class="flex flex-wrap items-center justify-between gap-3 rounded-md border border-dashed border-border p-2.5 bg-muted/20" data-testid="cloze-alt-unlinked-item">
            <div class="flex flex-wrap items-center gap-2 min-w-0">
              <Badge variant="neutral" class="font-mono text-xs">c{unlinkedItem.clozeNumber}</Badge>
              <span class="text-xs font-medium text-foreground">{unlinkedItem.canonical}:</span>
              {#if unlinkedItem.isInvalid}<span class="text-xs text-amber-700">{$t('note_edit.cloze_alternatives.unlinked_invalid_warning')}</span>{/if}
              <div class="flex flex-wrap gap-1">
                {#each unlinkedItem.alternatives as alt}
                  <span class="rounded bg-secondary/80 px-1.5 py-0.5 text-[11px] text-secondary-foreground">{alt}</span>
                {/each}
              </div>
            </div>
            <div class="flex items-center gap-2">
              {#if activeGroups.length > 0}
                <Select
                  class="h-7 text-xs w-36"
                  ariaLabel={$t('note_edit.cloze_alternatives.unlinked_target_placeholder')}
                  placeholder={$t('note_edit.cloze_alternatives.unlinked_target_placeholder')}
                  testId={`cloze-alt-reassociate-select-${unlinkedIdx}`}
                  options={activeGroups.map((g) => ({
                    value: g.key,
                    label: `c${g.clozeOrdinal}: ${g.canonical}`,
                  }))}
                  onValueChange={(val) => handleReassociate(unlinkedItem, val)}
                />
              {/if}
              <Button
                variant="ghost"
                size="sm"
                class="h-7 text-xs text-muted-foreground hover:text-destructive"
                testId={`cloze-alt-unlinked-delete-${unlinkedIdx}`}
                onclick={() => handleDeleteUnlinked(unlinkedItem)}
              >
                {$t('note_edit.cloze_alternatives.unlinked_delete')}
              </Button>
            </div>
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <Dialog
    open={resetConfirmOpen}
    onOpenChange={(open) => { if (!open) resetConfirmOpen = false; }}
    title={$t(meta.invalidCapability !== undefined ? 'note_edit.cloze_alternatives.reset_capability_title' : 'note_edit.cloze_alternatives.reset_title')}
    description={$t(meta.invalidCapability !== undefined ? 'note_edit.cloze_alternatives.reset_capability_warning' : 'note_edit.cloze_alternatives.reset_warning')}
    testId="cloze-alt-reset-dialog"
  >
    <div class="mt-4 flex justify-end gap-2">
      <Button variant="outline" onclick={() => (resetConfirmOpen = false)} testId="cloze-alt-reset-cancel">
        <span>{$t('common.cancel')}</span>
      </Button>
      <Button variant="danger" onclick={handleResetMalformed} testId="cloze-alt-reset-confirm">
        <span>{$t(meta.invalidCapability !== undefined ? 'note_edit.cloze_alternatives.reset_capability' : 'note_edit.cloze_alternatives.reset_confirm')}</span>
      </Button>
    </div>
  </Dialog>
</div>

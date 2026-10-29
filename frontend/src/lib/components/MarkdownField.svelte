<script lang="ts">
  import { tick } from 'svelte';
  import { t } from '../i18n';
  import {
    Bold,
    Braces,
    Code,
    ImagePlus,
    Italic,
    Link,
    List,
    ListOrdered,
    SquareCode,
    Strikethrough,
    Table,
    TextQuote,
  } from '@lucide/svelte';
  import {
    insertCodeBlock,
    insertLink,
    insertTable,
    markerForShortcut,
    toggleLinePrefix,
    toggleMarkdown,
    type MarkdownEdit,
  } from '../markdown-format';

  /**
   * A Markdown source field with its tools on top: one framed box whose header row holds every
   * formatting and insertion action and whose body is the textarea itself.
   *
   * The frame draws the only border, so the textarea is not a bordered box inside another one. The
   * actions write into this field alone, which is why the dialogs the host opens need no field selector.
   */
  interface Props {
    value: string;
    /** The field's visible name, used to name each action for assistive technology. */
    label: string;
    testId: string;
    rows?: number;
    /** Receives the new source after typing or after an action. */
    onvalue: (value: string) => void;
    /** Reports the caret as (selection start, selection end) on focus moves, clicks, key presses and edits. */
    oncaret?: (start: number, end: number) => void;
    /** Opens the image dialog; the button is handed over so the host can return focus to it. */
    oninsertimage?: (trigger: HTMLButtonElement) => void;
    /** Opens the shortcode dialog; the same trigger hand-back as `oninsertimage`. */
    oninsertshortcode?: (trigger: HTMLButtonElement) => void;
  }

  let { value, label, testId, rows = 5, onvalue, oncaret, oninsertimage, oninsertshortcode }: Props = $props();

  let area = $state<HTMLTextAreaElement | null>(null);

  // Small enough that a 390px row holds most of the set, but a 28px target so a finger still hits it.
  const toolbarButton =
    'inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring';

  const separator = 'h-4 w-px shrink-0 bg-input @max-sm:hidden';

  type Edit = (text: string, start: number, end: number) => MarkdownEdit;

  interface Action {
    id: string;
    Icon: typeof Bold;
    edit: Edit;
    shortcut?: string;
  }

  /**
   * The actions in two groups, in the order a writer scans them: inline marks first, then the blocks
   * that take whole lines. The table's header cells are read from the catalog when it is inserted, so
   * they follow the language active at that moment.
   */
  const actionGroups: Action[][] = [
    [
      { id: 'bold', Icon: Bold, edit: (s, a, b) => toggleMarkdown(s, a, b, '**'), shortcut: 'Control+B Meta+B' },
      { id: 'italic', Icon: Italic, edit: (s, a, b) => toggleMarkdown(s, a, b, '*'), shortcut: 'Control+I Meta+I' },
      {
        id: 'strike',
        Icon: Strikethrough,
        edit: (s, a, b) => toggleMarkdown(s, a, b, '~~'),
        shortcut: 'Control+Shift+X Meta+Shift+X',
      },
      { id: 'code', Icon: Code, edit: (s, a, b) => toggleMarkdown(s, a, b, '`') },
      { id: 'link', Icon: Link, edit: insertLink },
    ],
    [
      { id: 'quote', Icon: TextQuote, edit: (s, a, b) => toggleLinePrefix(s, a, b, 'quote') },
      { id: 'bullet', Icon: List, edit: (s, a, b) => toggleLinePrefix(s, a, b, 'bullet') },
      { id: 'ordered', Icon: ListOrdered, edit: (s, a, b) => toggleLinePrefix(s, a, b, 'ordered') },
      { id: 'code_block', Icon: SquareCode, edit: insertCodeBlock },
      {
        id: 'table',
        Icon: Table,
        edit: (s, a, b) =>
          insertTable(s, a, b, [
            $t('note_edit.format.table_header', { n: 1 }),
            $t('note_edit.format.table_header', { n: 2 }),
          ]),
      },
    ],
  ];

  const hasInsert = $derived(Boolean(oninsertimage || oninsertshortcode));

  function reportCaret(): void {
    if (!oncaret || !area) return;
    oncaret(area.selectionStart, area.selectionEnd);
  }

  /**
   * Applies one edit to the current selection.
   *
   * The textarea's own value is the source of truth here rather than the prop: while the user types,
   * the DOM value is the newest text, and reading it also gives the live selection. The new text goes
   * to the host first, then the DOM value and the selection are restored after Svelte has written that
   * change — restoring them before the state flush would let the framework move the caret to the end.
   */
  async function apply(edit: Edit): Promise<void> {
    const control = area;
    if (!control) return;
    const result = edit(control.value, control.selectionStart, control.selectionEnd);
    onvalue(result.text);
    await tick();
    control.value = result.text;
    control.focus();
    control.setSelectionRange(result.start, result.end);
    oncaret?.(result.start, result.end);
  }

  /**
   * Formatting shortcuts belong to this textarea only; the default is prevented only when a binding
   * matched, so a plain letter, a paste or a browser shortcut still behaves normally.
   */
  function handleKeydown(event: KeyboardEvent): void {
    if (event.isComposing) return;
    const marker = markerForShortcut(event);
    if (!marker) return;
    event.preventDefault();
    void apply((s, a, b) => toggleMarkdown(s, a, b, marker));
  }
</script>

<!-- The frame is a size container: the header decides its spacing from the field's own width, which at the
     two-column breakpoint is half the page, not from the viewport. -->
<div class="field-frame @container" data-testid="{testId}-frame">
  <!-- Each group wraps as one unit, so a narrow field breaks between groups and never strands a separator at
       a row end. Below 24rem the separators give way to a wider gap, which carries the grouping on any row. -->
  <div
    class="field-frame-header flex flex-wrap items-center gap-x-1 gap-y-1 px-1.5 py-1 @max-sm:gap-x-3"
    role="group"
    aria-label={$t('note_edit.format.group', { field: label })}
    data-testid="{testId}-toolbar"
  >
    {#each actionGroups as group, index (index)}
      {#if index > 0}
        <span class={separator} aria-hidden="true"></span>
      {/if}
      <div class="flex items-center gap-0.5">
        {#each group as action (action.id)}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-format-{action.id}"
            aria-label={$t('note_edit.format.' + action.id, { field: label })}
            title={$t('note_edit.format.' + action.id, { field: label })}
            aria-keyshortcuts={action.shortcut}
            onmousedown={(event) => event.preventDefault()}
            onclick={() => void apply(action.edit)}
          >
            <action.Icon class="size-4" aria-hidden="true" />
          </button>
        {/each}
      </div>
    {/each}
    {#if hasInsert}
      <span class={separator} aria-hidden="true"></span>
      <div class="flex items-center gap-0.5">
        {#if oninsertimage}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-insert-image"
            aria-label={$t('note_edit.insert_image', { field: label })}
            title={$t('note_edit.insert_image', { field: label })}
            onclick={(event) => oninsertimage(event.currentTarget)}
          >
            <ImagePlus class="size-4" aria-hidden="true" />
          </button>
        {/if}
        {#if oninsertshortcode}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-insert-shortcode"
            aria-label={$t('note_edit.insert_shortcode', { field: label })}
            title={$t('note_edit.insert_shortcode', { field: label })}
            onclick={(event) => oninsertshortcode(event.currentTarget)}
          >
            <Braces class="size-4" aria-hidden="true" />
          </button>
        {/if}
      </div>
    {/if}
  </div>
  <textarea
    data-testid={testId}
    {rows}
    spellcheck="false"
    aria-label={label}
    bind:this={area}
    {value}
    onkeydown={handleKeydown}
    oninput={(event) => {
      onvalue(event.currentTarget.value);
      reportCaret();
    }}
    onselect={reportCaret}
    onkeyup={reportCaret}
    onclick={reportCaret}
    class="field-frame-body resize-y text-sm"></textarea>
</div>

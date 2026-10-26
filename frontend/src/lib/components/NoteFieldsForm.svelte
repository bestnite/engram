<script lang="ts">
  import { tick } from 'svelte';
  import { t } from '../i18n';
  import { arrayToLines, linesToArray, type FieldSpec } from '../card-fields';
  import { Bold, Braces, ImagePlus, Italic, Strikethrough } from '@lucide/svelte';
  import { markerForShortcut, toggleMarkdown, type MarkdownMarker } from '../markdown-format';
  import Checkbox from './ui/Checkbox.svelte';
  import Select from './ui/Select.svelte';
  import HelpTip from './ui/HelpTip.svelte';

  /**
   * Renders the field form per card kind.
   *
   * Field order and control shape come from the server's card-kind self-description, and this component contains no per-kind branching
   * business logic; views therefore no longer hand fields to the user as raw JSON to edit.
   *
   * A single-string field also carries its own insertion and Markdown formatting actions. The tools sit
   * on the field they write into, so the author never leaves the field to find them, and a view that does
   * not pass the two insertion callbacks still renders the same form with formatting only.
   */
  interface Props {
    specs: FieldSpec[];
    /** Form model: written in place directly (the parent holds it with $state, deeply reactive). */
    fields: Record<string, unknown>;
    /** Prefix for generating data-testid: `<prefix>-<field name>`. */
    testIdPrefix?: string;
    /**
     * Reports the caret of a text control as (field key, selection start, selection end) on focus moves,
     * clicks, key presses and edits. A view that inserts at the caret keeps the last report per field; a
     * view that always appends simply omits the prop.
     */
    oncaret?: (key: string, start: number, end: number) => void;
    /**
     * Opens the image insertion dialog for one field. The button is handed over as the second argument so
     * the view can put focus back on it when the dialog closes.
     */
    oninsertimage?: (key: string, trigger: HTMLButtonElement) => void;
    /** Opens the shortcode dialog for one field; the same trigger hand-back as `oninsertimage`. */
    oninsertshortcode?: (key: string, trigger: HTMLButtonElement) => void;
  }

  let { specs, fields, testIdPrefix = 'note-field', oncaret, oninsertimage, oninsertshortcode }: Props = $props();

  // The compact insertion and formatting controls share one look: small enough for a 390px row, but a
  // 28px target so a finger still hits it.
  const toolbarButton =
    'inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring';

  /**
   * The live control per single-string field. A formatting action reads its selection, writes the
   * toggled source into the form model, and puts the resulting selection back on the same element.
   */
  let textControls = $state<Record<string, HTMLInputElement | HTMLTextAreaElement | null>>({});

  /** The three formatting actions, in the order a writer scans them: weight, slant, strike. */
  const formatActions = [
    { id: 'bold', marker: '**', Icon: Bold, shortcut: 'Control+B Meta+B' },
    { id: 'italic', marker: '*', Icon: Italic, shortcut: 'Control+I Meta+I' },
    { id: 'strike', marker: '~~', Icon: Strikethrough, shortcut: 'Control+Shift+X Meta+Shift+X' },
  ] as const satisfies ReadonlyArray<{
    id: string;
    marker: MarkdownMarker;
    Icon: typeof Bold;
    shortcut: string;
  }>;


  /**
   * Fields needing syntax help → the language pack key for the help text. The help goes into a question-mark popover next to the label instead of into the label:
   * cloze syntax has several rules, and putting them in parentheses in the label stretches it into a paragraph that must be read every time you edit.
   */
  const fieldHelp: Record<string, string> = {
    text: 'note_help.text',
  };


  /** The options field is used both for editing "choices" and for picking the index of a multiple-choice answer. */
  const options = $derived(
    Array.isArray(fields.options) ? fields.options.map((item) => String(item)).filter((item) => item !== '') : []
  );

  function asString(key: string): string {
    const value = fields[key];
    return typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value);
  }

  /**
   * Display value of the number box. Strings are shown as-is: they are what the user has not finished typing (e.g. "-", "1e"),
   * and stay stored as-is when setNumber cannot parse them; if this displayed as empty, the input would be cleared on the first keystroke.
   */
  function asNumber(key: string): string {
    const value = fields[key];
    if (typeof value === 'number') return String(value);
    return typeof value === 'string' ? value : '';
  }

  /** Numeric input: an empty string deletes the key (the "not filled" semantics of an optional numeric field), otherwise the number is written. */
  function setNumber(key: string, raw: string): void {
    if (raw.trim() === '') {
      delete fields[key];
      return;
    }
    const parsed = Number(raw);
    fields[key] = Number.isFinite(parsed) ? parsed : raw;
  }

  function setString(key: string, raw: string): void {
    if (raw === '') delete fields[key];
    else fields[key] = raw;
  }

  /**
   * Reports where the caret sits in one text control. Only single-string controls report: a "lines"
   * field is edited as several lines in one box, so its offsets do not map onto the stored array.
   */
  function reportCaret(
    event: Event & { currentTarget: HTMLInputElement | HTMLTextAreaElement },
    key: string
  ): void {
    if (!oncaret) return;
    const { selectionStart, selectionEnd } = event.currentTarget;
    if (selectionStart === null || selectionEnd === null) return;
    oncaret(key, selectionStart, selectionEnd);
  }

  /**
   * Applies one formatting marker to a field's current selection.
   *
   * The control's own value is the source of truth here rather than the form model: while the user
   * types, the DOM value is the newest text, and reading it also gives the live selection. The new
   * text goes into the model first, then the DOM value and the selection are restored after Svelte
   * has written that change — restoring them before the state flush would let the framework move the
   * caret to the end of the textarea.
   */
  async function applyFormat(key: string, marker: MarkdownMarker): Promise<void> {
    const control = textControls[key];
    if (!control) return;
    const start = control.selectionStart ?? 0;
    const end = control.selectionEnd ?? start;
    const edit = toggleMarkdown(control.value, start, end, marker);
    setString(key, edit.text);
    await tick();
    control.value = edit.text;
    control.focus();
    control.setSelectionRange(edit.start, edit.end);
    oncaret?.(key, edit.start, edit.end);
  }

  /**
   * Formatting shortcuts belong to the focused text control only; a key press anywhere else keeps
   * its browser meaning. The default is prevented only when a binding matched, so a plain letter,
   * a paste or a browser shortcut still behaves normally.
   */
  function handleFormatKeydown(event: KeyboardEvent, key: string): void {
    if (event.isComposing) return;
    const marker = markerForShortcut(event);
    if (!marker) return;
    event.preventDefault();
    void applyFormat(key, marker);
  }

  function selectedIndexes(key: string): number[] {
    const value = fields[key];
    return Array.isArray(value) ? value.map((item) => Number(item)).filter((n) => Number.isInteger(n)) : [];
  }


  function toggleIndex(key: string, index: number, checked: boolean): void {
    const current = selectedIndexes(key);
    const next = checked ? [...current, index] : current.filter((item) => item !== index);
    fields[key] = next.sort((a, b) => a - b);
  }
</script>

{#each specs as spec (spec.key)}
  {@const helpKey = fieldHelp[spec.key]}
  <!-- Only a control that holds one string can take an inline insertion: a `lines` field stores an
       array, and neither an image reference nor a shortcode can address one element of it. -->
  {@const inlineInsertable = spec.control === 'text' || spec.control === 'textarea'}
  {@const showToolbar = inlineInsertable && Boolean(oninsertimage || oninsertshortcode)}
  {@const fieldLabel = $t('note.fields.' + spec.key)}
  <div class="block">
    {#if spec.control === 'bool'}
      <div class="flex items-center gap-2">
        <Checkbox
          testId="{testIdPrefix}-{spec.key}"
          checked={Boolean(fields[spec.key])}
          onCheckedChange={(checked) => (fields[spec.key] = checked)}
          label={fieldLabel}
        />
        <span class="text-sm text-foreground/80">{fieldLabel}</span>
      </div>
    {:else}
      <div class="flex items-center gap-1">
        <span class="min-w-0 text-sm font-medium text-foreground">{fieldLabel}</span>
        {#if helpKey}
          <HelpTip
            testId="{testIdPrefix}-{spec.key}-help"
            label={fieldLabel}
            text={$t(helpKey)}
          />
        {/if}
        {#if showToolbar}
          <div class="ml-auto flex shrink-0 items-center gap-0.5" data-testid="{testIdPrefix}-{spec.key}-toolbar">
            {#if oninsertimage}
              <button
                type="button"
                class={toolbarButton}
                data-testid="{testIdPrefix}-{spec.key}-insert-image"
                aria-label={$t('note_edit.insert_image', { field: fieldLabel })}
                title={$t('note_edit.insert_image', { field: fieldLabel })}
                onclick={(event) => oninsertimage(spec.key, event.currentTarget)}
              >
                <ImagePlus class="size-4" aria-hidden="true" />
              </button>
            {/if}
            {#if oninsertshortcode}
              <button
                type="button"
                class={toolbarButton}
                data-testid="{testIdPrefix}-{spec.key}-insert-shortcode"
                aria-label={$t('note_edit.insert_shortcode', { field: fieldLabel })}
                title={$t('note_edit.insert_shortcode', { field: fieldLabel })}
                onclick={(event) => oninsertshortcode(spec.key, event.currentTarget)}
              >
                <Braces class="size-4" aria-hidden="true" />
              </button>
            {/if}
          </div>
        {/if}
      </div>
      <!-- The formatting actions sit on a row of their own: the label row already carries the field
           name, the help button and up to two insertion tools, and five icons in one row cannot stay
           readable next to a label at 390px. Keeping the row below the label also keeps every action
           one click away instead of behind a popover. -->
      {#if inlineInsertable}
        <div
          class="mt-1 flex items-center gap-0.5"
          role="group"
          aria-label={$t('note_edit.format.group', { field: fieldLabel })}
          data-testid="{testIdPrefix}-{spec.key}-format-toolbar"
        >
          {#each formatActions as action (action.id)}
            <button
              type="button"
              class={toolbarButton}
              data-testid="{testIdPrefix}-{spec.key}-format-{action.id}"
              aria-label={$t('note_edit.format.' + action.id, { field: fieldLabel })}
              title={$t('note_edit.format.' + action.id, { field: fieldLabel })}
              aria-keyshortcuts={action.shortcut}
              onmousedown={(event) => event.preventDefault()}
              onclick={() => void applyFormat(spec.key, action.marker)}
            >
              <action.Icon class="size-4" aria-hidden="true" />
            </button>
          {/each}
        </div>
      {/if}
      {#if spec.control === 'textarea'}
        <textarea
          data-testid="{testIdPrefix}-{spec.key}"
          rows={spec.key === 'extra' ? 3 : 5}
          spellcheck="false"
          bind:this={textControls[spec.key]}
          value={asString(spec.key)}
          onkeydown={(event) => handleFormatKeydown(event, spec.key)}
          oninput={(event) => {
            setString(spec.key, event.currentTarget.value);
            reportCaret(event, spec.key);
          }}
          onselect={(event) => reportCaret(event, spec.key)}
          onkeyup={(event) => reportCaret(event, spec.key)}
          onclick={(event) => reportCaret(event, spec.key)}
          class="field-input text-sm mt-1 block w-full"></textarea>
      {:else if spec.control === 'lines'}
        <textarea
          data-testid="{testIdPrefix}-{spec.key}"
          rows="4"
          spellcheck="false"
          value={arrayToLines(fields[spec.key])}
          oninput={(event) => (fields[spec.key] = linesToArray(event.currentTarget.value))}
          class="field-input text-sm mt-1.5 block w-full"></textarea>
      {:else if spec.control === 'number'}
        <input
          type="text"
          inputmode="decimal"
          data-testid="{testIdPrefix}-{spec.key}"
          value={asNumber(spec.key)}
          oninput={(event) => setNumber(spec.key, event.currentTarget.value)}
          class="field-input text-sm mt-1.5 block w-full"
        />
      {:else if spec.control === 'index'}
        {#if options.length === 0}
          <p class="mt-1.5 text-xs text-muted-foreground/70">{$t('note_edit.options_first')}</p>
        {:else}
          <Select
            class="mt-1.5"
            testId="{testIdPrefix}-{spec.key}"
            ariaLabel={$t('note_edit.answer_index')}
            value={fields[spec.key] === undefined || fields[spec.key] === '' ? '' : String(fields[spec.key])}
            onValueChange={(value) => (fields[spec.key] = Number(value))}
            options={options.map((option, index) => ({ value: String(index), label: `${index + 1}. ${option}` }))}
          />
        {/if}
      {:else if spec.control === 'indexes'}
        {#if options.length === 0}
          <p class="mt-1.5 text-xs text-muted-foreground/70">{$t('note_edit.options_first')}</p>
        {:else}
          <ul class="mt-1.5 space-y-1.5">
            {#each options as option, index (index)}
              <li class="flex items-center gap-2">
                <Checkbox
                  testId="{testIdPrefix}-{spec.key}"
                  checked={selectedIndexes(spec.key).includes(index)}
                  onCheckedChange={(checked) => toggleIndex(spec.key, index, checked)}
                  label={`${index + 1}. ${option}`}
                />
                <span class="text-sm text-foreground/80">{index + 1}. {option}</span>
              </li>
            {/each}
          </ul>
        {/if}
      {:else}
        <input
          type="text"
          data-testid="{testIdPrefix}-{spec.key}"
          bind:this={textControls[spec.key]}
          value={asString(spec.key)}
          onkeydown={(event) => handleFormatKeydown(event, spec.key)}
          oninput={(event) => {
            setString(spec.key, event.currentTarget.value);
            reportCaret(event, spec.key);
          }}
          onselect={(event) => reportCaret(event, spec.key)}
          onkeyup={(event) => reportCaret(event, spec.key)}
          onclick={(event) => reportCaret(event, spec.key)}
          class="field-input text-sm mt-1 block w-full"
        />
      {/if}
    {/if}
  </div>
{/each}

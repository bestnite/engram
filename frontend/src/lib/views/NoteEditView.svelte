<script lang="ts">
  import Skeleton from '../components/ui/Skeleton.svelte';
  import { onMount, tick, untrack } from 'svelte';
  import { routeStore, navigate, matchRoute } from '../router';
  import { RETURN_PARAM, safeReturnPath } from '../return-to';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Note, NotePreviewCard, NoteType, NoteTypeField } from '../api';
  import NoteFieldsForm from '../components/NoteFieldsForm.svelte';
  import Select from '../components/ui/Select.svelte';
  import Button from '../components/ui/Button.svelte';
  import Dialog from '../components/ui/Dialog.svelte';
  import { toast } from '../components/ui/toast';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import CardFrame from '../components/CardFrame.svelte';
  import { NOTE_PREVIEW_CSS, faceStyle } from '../card-frame';
  import { emptyFields, type FieldSpec } from '../card-fields';
  import type { RichFieldOptions } from '../components/RichTextField.svelte';
  import type { MediaReference } from '../../editframe/runtime';
  import { Check, Eye, Plus, Trash2, X } from '@lucide/svelte';

  // One implementation covers both create and edit: whether the address carries a noteId decides the mode, and every other part of the UI is identical item by item
  // (note type, fields with their own insertion tools, tags and live preview). A successful create always leaves the editor, leaving no second "create another" path.
  //
  // The fields come from the note type the deck stores, so any type - including one that matches no stock kind - is edited
  // and previewed with its own definitions. The editor never reads the card-kind catalog and never sends a kind.
  const deckId = $derived($routeStore.params.id || '');
  const noteId = $derived($routeStore.params.noteId || '');
  const isCreate = $derived(noteId === '');

  // Where leaving the editor goes: back, cancel and a finished create all lead to the page that opened the editor when
  // it said so in the address (the review page does), and to the deck page otherwise.
  const returnTo = $derived(
    safeReturnPath($routeStore.query[RETURN_PARAM]) ?? `/decks/${encodeURIComponent(deckId)}`
  );
  const returnsToReview = $derived(matchRoute(returnTo).route?.path === '/review');

  // Debounce window of the live preview: the request fires about half a second after typing in the fields stops, avoiding a request per character.
  const PREVIEW_DEBOUNCE_MS = 500;

  let note = $state<Note | null>(null);
  // The deck's note types, in the order the server returns them; the selected one supplies the fields.
  let noteTypes = $state<NoteType[]>([]);
  let noteTypeId = $state('');
  let fields = $state<Record<string, unknown>>({});
  let tagsText = $state('');

  let loading = $state(true);
  let saving = $state(false);
  let loadError = $state(false);
  let metaError = $state(false);
  // Why the note type is unusable: the deck has none, or the edited note names one the deck no longer lists.
  let typeProblem = $state<'' | 'empty' | 'missing'>('');

  // Delete confirmation: open, in flight, and the catalog key of the last failure shown inside the dialog.
  let deleteOpen = $state(false);
  let deleting = $state(false);
  let deleteError = $state('');

  // Preview drawer below the desktop breakpoint. At lg and wider the same panel is the sticky second
  // column, so this state only decides whether it is on screen; it never adds a second HTML sink.
  let previewOpen = $state(false);
  let previewTrigger = $state<HTMLButtonElement | null>(null);

  // Preview state machine: empty nothing entered yet / loading generating (the previous result may still be attached) / ready has a result /
  // incomplete fields are not complete (server 400) / error fetching failed.
  type PreviewStatus = 'empty' | 'loading' | 'ready' | 'incomplete' | 'error';
  let previewStatus = $state<PreviewStatus>('empty');
  let previewCards = $state<NotePreviewCard[] | null>(null);
  let previewReady = $state(false);

  // Non-reactive control slots: no state may be written after unmount; generation guarantees only the latest request can land.
  let disposed = false;
  let previewGeneration = 0;
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  /** The selected note type, or null while the list is still empty. */
  const selectedType = $derived(noteTypes.find((entry) => entry.id === noteTypeId) ?? null);

  /** The selected type's fields in manifest order; the form and the save payload follow this order. */
  const fieldDefs = $derived(selectedType?.config.fields ?? []);

  // Every note-type field is an HTML value, so the form uses one text control per field and every field gets the
  // rich-text editor. A field whose option says plain_text still renders as plain text inside that editor.
  const specs = $derived<FieldSpec[]>(
    fieldDefs.map((field) => ({ key: field.name, control: 'textarea', required: false }))
  );
  const fieldLabels = $derived(
    Object.fromEntries(fieldDefs.map((field) => [field.name, field.name]))
  );
  const fieldOptions = $derived(
    Object.fromEntries(fieldDefs.map((field) => [field.name, optionsFor(field)]))
  );
  const faceFields = $derived(fieldDefs.map((field) => field.name));

  // Note type dropdown: the list and its order come from the server, empty while the metadata is not ready.
  const typeOptions = $derived(noteTypes.map((entry) => ({ value: entry.id, label: entry.name })));

  // Stable signature of the field contents: a change to the note type or any field yields a new string, and the live preview reschedules off it.
  const signature = $derived(JSON.stringify({ type: noteTypeId, fields: collectFields() }));

  // Draft signature: a change to the note type, the submitted fields or the tags yields a new string (tags use the parsed array, so changing only whitespace
  // does not count as a new change either). Saving is only responsible for the snapshot at the moment of submission, so it is used to tell whether the draft was already changed when the response lands.
  const draftSignature = $derived(
    JSON.stringify({ type: noteTypeId, fields: collectFields(), tags: parseTags() })
  );

  /**
   * The field's presentation options. Each one comes from the stored note type, so a field keeps the
   * behaviour its author configured instead of a form-wide default.
   */
  function optionsFor(field: NoteTypeField): RichFieldOptions {
    return {
      sticky: field.sticky,
      collapsed: field.collapsed,
      plainText: field.plain_text,
      rtl: field.rtl,
      font: field.font || undefined,
      size: field.font_size > 0 ? field.font_size : undefined,
    };
  }

  /** Blank form model for one set of fields, keyed by field name. */
  function emptyNoteFields(defs: readonly NoteTypeField[]): Record<string, unknown> {
    return emptyFields(defs.map((field) => ({ key: field.name, control: 'textarea', required: false })));
  }

  function parseTags(): string[] {
    return tagsText.split(',').map((tag) => tag.trim()).filter(Boolean);
  }

  /**
   * Form model → fields for submission. The keys are the selected note type's field names, which is what the note write
   * resolves to field ids. A field value is the HTML string the editor holds, sent unchanged; an empty field is omitted
   * because the note write fills a field the payload omits with the empty string.
   */
  function collectFields(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const [key, value] of Object.entries(fields)) {
      if (typeof value === 'string' && value !== '') out[key] = value;
    }
    return out;
  }

  /**
   * Whether there is already previewable content: at least one field of the selected note type holds something.
   * An empty form sends no preview request; an incomplete one reaches the server and is reported as incomplete.
   */
  function hasContent(): boolean {
    return Object.keys(collectFields()).length > 0;
  }

  /**
   * Load one note's content into the form; the selected type comes from the note itself.
   *
   * A note response carries each field as its stored HTML string. A value of any other type is not converted: a response
   * from before this shape is left blank rather than turned into text, so saving cannot overwrite the stored HTML.
   */
  function adoptFields(loaded: Note, type: NoteType): void {
    noteTypeId = type.id;
    const next: Record<string, unknown> = {};
    for (const field of type.config.fields) {
      // The response keys field values by field id; accepting the name too keeps an older payload readable.
      const value = loaded.fields[field.id] ?? loaded.fields[field.name];
      next[field.name] = typeof value === 'string' ? value : '';
    }
    fields = next;
    tagsText = loaded.tags.join(', ');
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = false;
    metaError = false;
    typeProblem = '';
    // The deck's note types are the only field metadata: without them the form cannot be built.
    try {
      const response = await apiClient.listNoteTypes(deckId);
      if (disposed) return;
      noteTypes = response.note_types ?? [];
    } catch {
      if (disposed) return;
      metaError = true;
      previewReady = false;
      loading = false;
      return;
    }
    // The form is no longer initialized after unmount/a route change: late-arriving metadata must not write state or trigger the first-screen preview.
    if (disposed) return;
    try {
      if (isCreate) {
        // Creating never GETs with an empty id. The first note type in the server's order is the starting point.
        const initial = noteTypes[0];
        if (!initial) {
          typeProblem = 'empty';
        } else {
          noteTypeId = initial.id;
          fields = emptyNoteFields(initial.config.fields);
          previewReady = true;
        }
      } else {
        // Read the target note directly by its public id; a note that does not belong to the deck in the address is treated as "does not exist", and cross-deck editing is forbidden.
        const loaded = await apiClient.getNote(noteId);
        if (disposed) return;
        const loadedNote = loaded && String(loaded.deck_id) === deckId ? loaded : null;
        note = loadedNote;
        if (!loadedNote) {
          loadError = true;
        } else {
          const type = noteTypes.find((entry) => entry.id === loadedNote.note_type);
          if (!type) {
            typeProblem = 'missing';
          } else {
            adoptFields(loadedNote, type);
            previewReady = true;
          }
        }
      }
    } catch {
      if (disposed) return;
      loadError = true;
    } finally {
      if (!disposed) loading = false;
    }
  }

  /**
   * Switch note type: reinitialize the fields for the new type. Old field names do not transfer, because a name means a
   * different field (or no field) in the new type, and keeping them would only submit content the new type cannot show.
   */
  function changeNoteType(next: string): void {
    const type = noteTypes.find((entry) => entry.id === next);
    if (!type) return;
    noteTypeId = type.id;
    fields = emptyNoteFields(type.config.fields);
  }

  /**
   * Uploads one file the rich-text editor's toolbar or a paste offered, and hands back the URL, the
   * deck media name and the mime type the server detected. The editor decides between an image and a
   * sound from the mime type, so a file whose name lies about its type is still inserted correctly.
   */
  async function uploadFieldMedia(file: File): Promise<MediaReference> {
    const uploaded = await apiClient.uploadDeckMedia(deckId, file);
    return { url: uploaded.url, name: uploaded.sha256, mime: uploaded.mime };
  }

  /** Opens the preview drawer below the desktop breakpoint, leaving focus where the author was typing. */
  function openPreview(trigger: HTMLButtonElement): void {
    previewTrigger = trigger;
    previewOpen = true;
  }

  /** Closes the preview drawer and returns focus to the header button that opened it. */
  function closePreview(): void {
    if (!previewOpen) return;
    previewOpen = false;
    const trigger = previewTrigger;
    previewTrigger = null;
    if (trigger) void tick().then(() => trigger.focus());
  }

  function handleWindowKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !previewOpen) return;
    // The delete dialog closes itself on Esc; a key press it already answered must not also shut the drawer.
    if (deleteOpen) return;
    closePreview();
  }

  /**
   * Preview scheduling: clear the pending one, advance generation, then queue a new one after the debounce window.
   * When there is a previous result it is kept and only the state is marked as generating — the preview never flashes blank while rescheduling.
   */
  function schedulePreview(): void {
    if (debounceTimer !== null) {
      clearTimeout(debounceTimer);
      debounceTimer = null;
    }
    const generation = ++previewGeneration;
    if (!hasContent()) {
      previewStatus = 'empty';
      previewCards = null;
      return;
    }
    previewStatus = 'loading';
    debounceTimer = setTimeout(() => {
      debounceTimer = null;
      void runPreview(generation);
    }, PREVIEW_DEBOUNCE_MS);
  }

  /** The landing result of one preview request: a success carries the cards, whose faces carry their own media base; a failure carries the state (400 = incomplete fields, anything else = fetch failure). */
  type PreviewOutcome =
    | { ok: true; cards: NotePreviewCard[] }
    | { ok: false; status: 'incomplete' | 'error' };

  /**
   * Lands one preview result if it is still the newest generation. The faces are rendered by the frame, which typesets and
   * enhances its own document, so a stale result is simply discarded here: the newest generation is always the last one to
   * write the state, and no later round can rewrite the DOM a newer card already replaced.
   */
  function commitPreview(generation: number, outcome: PreviewOutcome): void {
    if (disposed || generation !== previewGeneration) return;
    if (!outcome.ok) {
      previewCards = null;
      previewStatus = outcome.status;
      return;
    }
    previewCards = outcome.cards;
    previewStatus = 'ready';
  }

  /** Fetch one preview. Before landing it first confirms it is still the newest generation; stale results and stale errors are both discarded. */
  async function runPreview(generation: number): Promise<void> {
    if (disposed || generation !== previewGeneration) return;
    const requestType = noteTypeId;
    const requestFields = collectFields();
    let outcome: PreviewOutcome;
    try {
      // The preview faces are rendered by the frame, so the request asks for frame-ready HTML and its media base.
      const response = await apiClient.previewNote(deckId, requestType, requestFields, { frame: true });
      if (disposed || generation !== previewGeneration) return;
      outcome = { ok: true, cards: response.cards };
    } catch (err) {
      if (disposed || generation !== previewGeneration) return;
      // 400 means "the fields are not complete yet / the note type cannot render", expressed separately from a fetch failure.
      outcome = { ok: false, status: err instanceof ApiClientError && err.status === 400 ? 'incomplete' : 'error' };
    }
    commitPreview(generation, outcome);
  }

  /** Submission entry: create and edit share one path and fork only in the landing action; duplicate submissions are ignored outright while in flight. */
  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (saving) return;
    if (isCreate) await create();
    else await save();
  }

  /** Edit save: update in place, keeping the card identity and the tag state, without navigating. */
  async function save(): Promise<void> {
    if (!note) return;
    // Freeze the draft at the instant of submission: the response is only responsible for this snapshot.
    const submitted = draftSignature;
    saving = true;
    try {
      const updated = await apiClient.updateNote(note.id, {
        note_type: noteTypeId,
        fields: collectFields(),
        tags: parseTags(),
      });
      if (disposed) return;
      // Identity lands first: even if the draft was changed while the request was in flight, later saves still point at the same card.
      note = updated;
      if (draftSignature === submitted) {
        // No new input during the request: the response can safely be collected into the form and reported as saved.
        const type = noteTypes.find((entry) => entry.id === updated.note_type) ?? selectedType;
        if (type) adoptFields(updated, type);
        toast.success($t('note_edit.saved'));
      }
      // The draft has already been changed: keep what the user is typing (do not adopt it) and do not falsely report "saved".
    } catch (err) {
      if (disposed) return;
      reportSaveError(err);
    } finally {
      if (!disposed) saving = false;
    }
  }

  /**
   * Deletes the note being edited, then leaves for the return page. The editor's own history entry is
   * replaced rather than kept: going back to it would only show a note that no longer exists.
   */
  async function deleteCurrent(): Promise<void> {
    if (!note || deleting) return;
    deleting = true;
    deleteError = '';
    try {
      await apiClient.deleteNote(note.id);
      if (disposed) return;
      deleteOpen = false;
      toast.success($t('notes.delete_success'));
      navigate(returnTo, true);
    } catch (err) {
      if (disposed) return;
      deleteError =
        err instanceof ApiClientError && err.code === 'insufficient_role'
          ? 'error.forbidden'
          : err instanceof ApiClientError && err.code === 'not_found'
            ? 'error.not_found'
            : 'error.unknown';
    } finally {
      if (!disposed) deleting = false;
    }
  }

  /** Save failure message: 400 means invalid content, so an actionable reason is given; everything else reports the generic failure. */
  function reportSaveError(err: unknown): void {
    const invalid = err instanceof ApiClientError && err.status === 400;
    if (isCreate) toast.error($t(invalid ? 'note_create.invalid' : 'note_create.failed'));
    else toast.error($t(invalid ? 'note_edit.invalid' : 'note_edit.failed'));
  }

  /** Create: it only leaves the editor once creation is confirmed successful (not dry_run, created==1, no per-row errors). */
  async function create(): Promise<void> {
    saving = true;
    try {
      const result = await apiClient.createNotes(deckId, {
        notes: [{ note_type: noteTypeId, fields: collectFields(), tags: parseTags() }],
      });
      // The user left while the request was in flight (route change/unmount): no more state writes, and do not pull them to the return page either.
      if (disposed) return;
      if (result.dry_run || result.created !== 1 || result.errors.length > 0) {
        reportSaveError(null);
        return;
      }
      navigate(returnTo);
    } catch (err) {
      if (disposed) return;
      reportSaveError(err);
    } finally {
      if (!disposed) saving = false;
    }
  }

  // Live preview trigger point: after loading completes (the first-screen preview), every change to the note type or the field contents reschedules once.
  // The scheduling is wrapped in untrack: scheduling reads previewCards/previewStatus, and if those were recorded as dependencies it would form
  // a self-exciting loop of "preview finished → schedule another preview"; the only real dependencies are previewReady and signature.
  $effect(() => {
    if (!previewReady) return;
    const current = signature;
    void current;
    untrack(() => schedulePreview());
  });

  onMount(() => {
    void load();
    return () => {
      // Unmount/route change: stop the pending debounce and make every in-flight callback exit before writing state.
      disposed = true;
      if (debounceTimer !== null) {
        clearTimeout(debounceTimer);
        debounceTimer = null;
      }
    };
  });
</script>

<!-- Esc closes the preview drawer. The drawer is a panel of this page rather than a dialog of its own, so its
     keyboard dismissal is handled here; an open delete dialog keeps its own Esc. -->
<svelte:window onkeydown={handleWindowKeydown} />

<Page as="section">
  <PageHeader
    title={isCreate ? $t('note_create.title') : $t('note_edit.title')}
    back={{
      href: returnTo,
      label: returnsToReview ? $t('note_edit.back_review') : $t('note_edit.back'),
      testId: 'note-edit-back',
    }}
  >
    <!-- Below lg the preview is not a column but a drawer, so it needs a way in. The button is absent at lg,
         where the preview is already on screen. -->
    {#snippet actions()}
      <Button
        variant="outline"
        size="lg"
        testId="note-preview-toggle"
        class="lg:hidden"
        ariaExpanded={previewOpen}
        ariaControls="note-preview-panel"
        onclick={(event) => openPreview(event.currentTarget as HTMLButtonElement)}
      >
        <Eye class="size-4" aria-hidden="true" />
        <span>{$t('note_preview.title')}</span>
      </Button>
    {/snippet}
  </PageHeader>
  {#if loading}
    <Skeleton testId="note-edit-loading" label={$t('note_edit.loading')} lines={6} />
  {:else if !isCreate && loadError && !note}
    <p role="alert" data-testid="note-edit-not-found" class="text-sm text-destructive-foreground">{$t('note_edit.not_found')}</p>
  {:else if metaError}
    <p role="alert" data-testid="note-edit-metadata-error" class="text-sm text-destructive-foreground">{$t('common.error')}</p>
  {:else if typeProblem === 'empty'}
    <p role="alert" data-testid="note-edit-no-types" class="text-sm text-muted-foreground">{$t('note_edit.type.empty')}</p>
  {:else if typeProblem === 'missing'}
    <p role="alert" data-testid="note-edit-type-missing" class="text-sm text-destructive-foreground">{$t('note_edit.type.missing')}</p>
  {:else}
    <form onsubmit={submit} data-testid="note-editor-form">
      <div class="grid gap-8 lg:grid-cols-2 lg:items-start">
        <!-- Left column: input. Note type, its fields (every field carries its own formatting and insertion tools)
             and the tags; the tools sit on the field they write into, so nothing has to be scrolled to on a phone. -->
        <div class="min-w-0 space-y-8">
          <div class="space-y-5">
            <div>
              <span class="block text-sm font-medium text-foreground">{$t('note_edit.type.label')}</span>
              <Select
                class="mt-1.5 w-full"
                testId="note-edit-type"
                value={noteTypeId}
                onValueChange={changeNoteType}
                options={typeOptions}
              />
              <p class="mt-1.5 text-xs text-muted-foreground">{$t('note_edit.type.hint')}</p>
            </div>

            <NoteFieldsForm
              {specs}
              {fields}
              {faceFields}
              fieldOptions={fieldOptions}
              labels={fieldLabels}
              testIdPrefix="note-field"
              upload={uploadFieldMedia}
            />

            <label class="block text-sm font-medium text-foreground">{$t('note_edit.tags')}
              <input data-testid="note-tags-editor" bind:value={tagsText} class="field-input text-sm mt-1.5 w-full" />
            </label>
          </div>
        </div>

        <!-- Right column: live preview, rescheduled on input, card type and insertion changes. At lg and wider it is the
             sticky second column, its height capped at one screen and scrolling inside itself. Below lg it is a drawer:
             one and the same panel, laid over the page while open and out of the layout while closed, so the form keeps
             the whole width and no second copy of the preview HTML is ever rendered. -->
        <div class={previewOpen ? 'min-w-0' : 'hidden min-w-0 lg:sticky lg:top-6 lg:block'}>
          {#if previewOpen}
            <!-- Below lg only: the drawer covers the form, and this closes it. Escape is handled by the window handler. -->
            <div
              class="fixed inset-0 z-30 bg-overlay backdrop-blur-[2px] lg:hidden"
              data-testid="note-preview-backdrop"
              aria-hidden="true"
              onclick={() => closePreview()}
            ></div>
          {/if}
          <div
            id="note-preview-panel"
            data-testid="note-preview-panel"
            data-open={previewOpen ? 'true' : undefined}
            role="region"
            aria-labelledby="note-preview-heading"
            class={previewOpen
              ? 'fixed inset-x-0 bottom-0 z-40 flex max-h-[85vh] flex-col overflow-hidden rounded-t-xl border-t border-border bg-surface shadow-2xl max-lg:animate-in max-lg:fade-in-0 max-lg:slide-in-from-bottom-4 max-lg:duration-200 lg:static lg:z-auto lg:max-h-[calc(100vh-3rem)] lg:rounded-lg lg:border lg:shadow-none'
              : 'hidden max-h-[calc(100vh-3rem)] flex-col overflow-hidden rounded-lg border border-border bg-surface lg:flex'}
          >
            <div class="flex shrink-0 items-center gap-3 border-b border-border px-4 py-2.5">
              <!-- The panel is a region named by this heading at both breakpoints; it is not a modal dialog, since
                   focus is never trapped inside it. -->
              <h2 id="note-preview-heading" class="text-sm font-semibold text-foreground">{$t('note_preview.title')}</h2>
              <!-- The status copy gets a polite live region of its own: it only announces short phrases like "generating/incomplete/failed", while
                   the card body sits outside the live region so a reschedule does not read the whole HTML out again. -->
              <div aria-live="polite" class="ml-auto min-w-0 truncate text-right text-xs">
                {#if previewStatus === 'incomplete'}
                  <p data-testid="note-preview-incomplete" role="status" class="text-warning">{$t('note_preview.incomplete')}</p>
                {:else if previewStatus === 'error'}
                  <p data-testid="note-preview-error" role="alert" class="text-destructive-foreground">{$t('note_preview.failed')}</p>
                {:else if previewStatus === 'loading' && !previewCards}
                  <p data-testid="note-preview-loading" role="status" class="text-muted-foreground">{$t('note_preview.loading')}</p>
                {/if}
                {#if previewStatus === 'loading' && previewCards}
                  <p data-testid="note-preview-updating" role="status" class="text-muted-foreground">{$t('note_preview.updating')}</p>
                {/if}
              </div>
              <!-- Drawer only: at lg the preview is a column and needs no dismissal. -->
              <button
                type="button"
                data-testid="note-preview-close"
                class="inline-flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring lg:hidden"
                aria-label={$t('note_preview.close')}
                title={$t('note_preview.close')}
                onclick={() => closePreview()}
              >
                <X class="size-4" aria-hidden="true" />
              </button>
            </div>

            <div class="min-h-0 flex-1 overflow-y-auto p-4">
              {#if previewStatus === 'empty'}
                <p data-testid="note-preview-empty" class="py-8 text-center text-sm text-muted-foreground">{$t('note_preview.empty')}</p>
              {/if}
              {#if previewCards}
                <!-- When the previous result is kept it is dimmed and aria-busy is set, making "stale but still rescheduling" visible instead of a blank flash. -->
                <div
                  data-testid="note-preview-result"
                  aria-busy={previewStatus === 'loading'}
                  class="space-y-4 transition-opacity duration-150 {previewStatus === 'loading' ? 'opacity-60' : ''}"
                >
                  {#each previewCards as card, index}
                    <div class="divide-y divide-border overflow-hidden rounded-md border border-border bg-background">
                      <div data-testid="note-preview-front-{index}" class="space-y-2 p-4">
                        <h3 class="text-xs font-medium text-muted-foreground">{$t('note_preview.front')}</h3>
                        <!-- Only the HTML returned by the preview API reaches the frame: the editor's raw fields never do. -->
                        <CardFrame
                          html={card.front.html}
                          css={faceStyle(NOTE_PREVIEW_CSS, card.front.css)}
                          bodyClass={card.front.body_class}
                          side="question"
                          mediaBase={card.front.media_base}
                          autoplay={card.front.autoplay ?? false}
                          avTags={card.front.av_tags ?? []}
                          title={$t('note_preview.front')}
                        />
                      </div>
                      <div data-testid="note-preview-back-{index}" class="space-y-2 p-4">
                        <h3 class="text-xs font-medium text-muted-foreground">{$t('note_preview.back')}</h3>
                        <!-- Only the HTML returned by the preview API reaches the frame: the editor's raw fields never do. -->
                        <CardFrame
                          html={card.back.html}
                          css={faceStyle(NOTE_PREVIEW_CSS, card.back.css)}
                          bodyClass={card.back.body_class}
                          side="answer"
                          mediaBase={card.back.media_base}
                          autoplay={card.back.autoplay ?? false}
                          avTags={card.back.av_tags ?? []}
                          title={$t('note_preview.back')}
                        />
                      </div>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        </div>
      </div>

      <!-- The action bar sticks to the bottom of the viewport: a long form does not have to be scrolled to the end before saving. -->
      <div class="sticky bottom-0 z-10 -mx-4 mt-8 flex flex-wrap items-center gap-3 border-t border-border bg-background/90 px-4 py-3 backdrop-blur-md sm:-mx-8 sm:px-8">
        <Button type="submit" size="lg" testId="note-submit" loading={saving}>
          {#if isCreate}
            <Plus class="size-4" aria-hidden="true" />
          {:else}
            <Check class="size-4" aria-hidden="true" />
          {/if}
          <span>{isCreate ? $t('note_create.submit') : $t('note_edit.save')}</span>
        </Button>
        <Button variant="ghost" size="lg" testId="note-cancel" onclick={() => navigate(returnTo)}>
          {$t('note_edit.cancel')}
        </Button>
        {#if !isCreate && note}
          <!-- Kept apart from save and cancel at the far end of the bar, so a destructive action is never the next
               button a hurried click lands on. -->
          <Button
            variant="danger-outline"
            size="lg"
            class="ml-auto"
            testId="note-delete"
            disabled={saving}
            onclick={() => {
              deleteError = '';
              deleteOpen = true;
            }}
          >
            <Trash2 class="size-4" aria-hidden="true" />
            <span>{$t('notes.delete')}</span>
          </Button>
        {/if}
      </div>
    </form>

    <Dialog
      open={deleteOpen}
      onOpenChange={(open) => {
        if (!open && !deleting) deleteOpen = false;
      }}
      title={$t('notes.delete_confirm')}
      description={$t('notes.delete_desc')}
      testId="note-edit-delete-dialog"
      size="sm"
    >
      {#if deleteError}
        <p role="alert" data-testid="note-edit-delete-error" class="text-sm text-destructive-foreground">{$t(deleteError)}</p>
      {/if}
      <div class="mt-2 flex items-center justify-end gap-2">
        <Button type="button" variant="outline" size="lg" disabled={deleting} onclick={() => (deleteOpen = false)}>
          {$t('note_edit.cancel')}
        </Button>
        <Button
          type="button"
          variant="danger"
          size="lg"
          testId="note-edit-delete-confirm"
          loading={deleting}
          onclick={() => void deleteCurrent()}
        >
          <Trash2 class="size-4" aria-hidden="true" />
          <span>{$t('notes.delete')}</span>
        </Button>
      </div>
    </Dialog>

  {/if}
</Page>

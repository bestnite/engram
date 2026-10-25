<script lang="ts">
  import Skeleton from '../components/ui/Skeleton.svelte';
  import { onMount, tick, untrack } from 'svelte';
  import { routeStore, navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Note } from '../api';
  import { typeset } from '../mathjax';
  import { enhanceShortcodes, stopShortcodePlayback } from '../shortcodes';
  import MediaPicker from '../components/MediaPicker.svelte';
  import NoteFieldsForm from '../components/NoteFieldsForm.svelte';
  import Select from '../components/ui/Select.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import Dropzone from '../components/ui/Dropzone.svelte';
  import { emptyFields, toFormFields, toPayloadFields } from '../card-fields';
  import { cardTypes, fieldsForKind, kindOrder, loadCardTypes } from '../card-types';

  // One implementation covers both create and edit: whether the address carries a noteId decides the mode, and every other part of the UI is identical item by item
  // (card type, fields, tags, media and live preview). A successful create always jumps back to the deck page, leaving no second "create another" path.
  const deckId = $derived($routeStore.params.id || '');
  const noteId = $derived($routeStore.params.noteId || '');
  const isCreate = $derived(noteId === '');

  // Debounce window of the live preview: the request fires about half a second after typing in the fields stops, avoiding a request per character.
  const PREVIEW_DEBOUNCE_MS = 500;

  let note = $state<Note | null>(null);
  let kind = $state('basic');
  let fields = $state<Record<string, unknown>>({});
  // Keys outside this card type's field table (custom keys written externally) are kept as-is and merged back on save, never silently dropped.
  let unknownKeys = $state<Record<string, unknown>>({});
  let tagsText = $state('');

  let loading = $state(true);
  let saving = $state(false);
  let loadError = $state(false);
  let metaError = $state(false);

  let selectedField = $state('');
  // Upload control state: it shares insertMedia's insertion path with the media library picker (the editor's media facet).
  let uploading = $state(false);

  // Preview state machine: empty nothing entered yet / loading generating (the previous result may still be attached) / ready has a result /
  // incomplete fields are not complete (server 400) / error fetching failed.
  type PreviewStatus = 'empty' | 'loading' | 'ready' | 'incomplete' | 'error';
  let previewStatus = $state<PreviewStatus>('empty');
  let previewCards = $state<Array<{ front_html: string; back_html: string }> | null>(null);
  // MathJax typesetting is only invoked on it: it is the only container carrying server-sanitized HTML.
  let previewSection = $state<HTMLElement | null>(null);
  let previewReady = $state(false);

  // Non-reactive control slots: no state may be written after unmount; generation guarantees only the latest request can land.
  let disposed = false;
  let previewGeneration = 0;
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  let typesetChain: Promise<void> = Promise.resolve();

  // Card type dropdown: the list and its order come from the server's self-description (kind in lexicographic order), empty while the metadata is not ready.
  const kindOptions = $derived(
    kindOrder($cardTypes).map((value) => ({ value, label: $t(`notes.kind.${value}` as never) }))
  );

  // Fields that can take media: only text-type fields can carry Markdown image references.
  const textFields = $derived(
    fieldsForKind($cardTypes, kind)
      .filter((spec) => spec.control === 'text' || spec.control === 'textarea' || spec.control === 'lines')
      .map((spec) => ({ value: spec.key, label: $t('note.fields.' + spec.key) }))
  );

  // Stable signature of the field contents: a change to kind or any field yields a new string, and the live preview reschedules off it.
  const signature = $derived(JSON.stringify({ kind, fields: collectFields() }));

  // Draft signature: a change to kind, the submitted fields or the tags yields a new string (tags use the parsed array, so changing only whitespace
  // does not count as a new change either). Saving is only responsible for the snapshot at the moment of submission, so it is used to tell whether the draft was already changed when the response lands.
  const draftSignature = $derived(
    JSON.stringify({ kind, fields: collectFields(), tags: parseTags() })
  );

  function firstTextField(nextKind: string): string {
    const spec = fieldsForKind($cardTypes, nextKind).find(
      (item) => item.control === 'text' || item.control === 'textarea' || item.control === 'lines'
    );
    return spec ? spec.key : '';
  }

  function parseTags(): string[] {
    return tagsText.split(',').map((tag) => tag.trim()).filter(Boolean);
  }

  /** Form model → fields for submission: recognized keys are cleaned per the table, unknown keys are carried back as-is. */
  function collectFields(): Record<string, unknown> {
    return { ...unknownKeys, ...toPayloadFields(fields) };
  }

  /**
   * Whether there is already previewable content: it only looks at the text / array / numeric fields in this card type's
   * field table and judges whether they deviate from the empty-form baseline. Boolean fields have no renderable body,
   * and their defaults (e.g. typed's ignore_case=true, list's ordered=false, true_false's answer=false) come from the card
   * type definition rather than user input, so they never count; keys outside the card type table (unknownKeys) are not
   * this input either and do not count the same way. The numeric value 0 is real input and must count.
   */
  function hasContent(): boolean {
    const specs = fieldsForKind($cardTypes, kind);
    const baseline = toPayloadFields(emptyFields(specs));
    const current = toPayloadFields(fields);
    for (const spec of specs) {
      if (spec.control === 'bool') continue;
      if (!(spec.key in current)) continue;
      if (JSON.stringify(current[spec.key]) !== JSON.stringify(baseline[spec.key])) return true;
    }
    return false;
  }

  /** Load the card and collect the server fields into the form model; unknown keys are returned to be stored separately and merged on save. */
  function adoptFields(loaded: Note): void {
    kind = loaded.kind;
    fields = toFormFields(fieldsForKind($cardTypes, loaded.kind), loaded.fields);
    const known = new Set(fieldsForKind($cardTypes, loaded.kind).map((spec) => spec.key));
    unknownKeys = Object.fromEntries(Object.entries(loaded.fields).filter(([key]) => !known.has(key)));
    tagsText = loaded.tags.join(', ');
    selectedField = firstTextField(loaded.kind);
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = false;
    // First make sure the card type metadata is ready: the field table arrives together with the metadata, and an unknown card type cannot initialize the form.
    let catalogReady = true;
    try {
      await loadCardTypes(apiClient);
    } catch {
      if (disposed) return;
      metaError = true;
      catalogReady = false;
    }
    // The form is no longer initialized after unmount/a route change: late-arriving metadata must not write state or trigger the first-screen preview.
    if (disposed) return;
    try {
      if (isCreate) {
        // Creating does not read a remote card, so it never GETs with an empty id. Defaults to basic, falling back to the first entry when the list lacks that card type.
        const kinds = kindOrder($cardTypes);
        const initialKind = kinds.includes('basic') ? 'basic' : kinds[0] ?? 'basic';
        kind = initialKind;
        fields = emptyFields(fieldsForKind($cardTypes, initialKind));
        unknownKeys = {};
        selectedField = firstTextField(initialKind);
        previewReady = catalogReady;
      } else {
        // Read the target card directly by its public id; a card that does not belong to the deck in the address is treated as "does not exist", and cross-deck editing is forbidden.
        const loaded = await apiClient.getNote(noteId);
        if (disposed) return;
        const loadedNote = loaded && String(loaded.deck_id) === deckId ? loaded : null;
        note = loadedNote;
        if (loadedNote) {
          adoptFields(loadedNote);
          previewReady = catalogReady;
        } else {
          loadError = true;
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
   * Switch card type: reinitialize the fields for the new card type (old field names do not transfer, keeping them would only produce half a card).
   * Unknown keys stay untouched: they are data on the original card that belongs to no field table, and switching card type should not drop them on the way either.
   */
  function changeKind(next: string): void {
    kind = next;
    fields = emptyFields(fieldsForKind($cardTypes, next));
    selectedField = firstTextField(next);
  }

  // insertMedia appends a Markdown image reference to the currently selected field; upload and the picker share the same piece of logic.
  // String concatenation only, never render url as HTML; returns whether it actually inserted (content is left untouched when the field is invalid).
  function insertMedia(url: string): boolean {
    const current = fields[selectedField];
    if (typeof current !== 'string') return false;
    fields[selectedField] = `${current}${current && !current.endsWith('\n') ? '\n' : ''}![](${url})`;
    return true;
  }

  // uploadErrorKeyFor maps the server's stable error code to a localized key; unknown codes fall back to the generic failure message.
  function uploadErrorKeyFor(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      media_too_large: 'media.upload.too_large',
      media_mime_not_allowed: 'media.upload.mime_not_allowed',
      media_magic_mismatch: 'media.upload.magic_mismatch',
      media_quota_exceeded: 'media.upload.quota_exceeded',
      media_missing_file: 'media.upload.file_required',
    };
    return map[code] || 'media.upload.failed';
  }

  // Selecting or dropping a file uploads right away: on success it is inserted into the selected field, sharing the same insertMedia as the media library picker.
  async function uploadMedia(file: File): Promise<void> {
    uploading = true;
    try {
      const result = await apiClient.uploadDeckMedia(deckId, file);
      if (disposed) return;
      if (insertMedia(result.url)) toast.success($t('media.upload.inserted'));
    } catch (err) {
      if (disposed) return;
      toast.error($t(uploadErrorKeyFor(err)));
    } finally {
      if (!disposed) uploading = false;
    }
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

  /** The landing result of one preview request: success carries a card, failure carries the state (400 = incomplete fields, anything else = fetch failure). */
  type PreviewOutcome =
    | { ok: true; cards: Array<{ front_html: string; back_html: string }> }
    | { ok: false; status: 'incomplete' | 'error' };

  /**
   * Chain all three steps — "write the new card → wait for Svelte to mount the DOM → MathJax typesetting" — at the tail of one serial chain.
   *
   * Serializing only the typesetting is not enough: if a new response replaces the container's content while the previous
   * round is still typesetting, the previous round's MathJax will rewrite the DOM that has already been swapped (old
   * formulas inserted into the new card). With all three steps serialized together, each round of typesetting is guaranteed
   * to see the container its own generation committed, and the newest generation lands last. Every step in the chain confirms
   * it is still the newest generation; when the latest one failed, no typesetting runs.
   */
  function commitPreview(generation: number, outcome: PreviewOutcome): void {
    typesetChain = typesetChain.then(async () => {
      if (disposed || generation !== previewGeneration) return;
      if (!outcome.ok) {
        previewCards = null;
        previewStatus = outcome.status;
        return;
      }
      previewCards = outcome.cards;
      previewStatus = 'ready';
      await tick();
      if (disposed || generation !== previewGeneration) return;
      const container = previewSection;
      if (container) {
        await typeset([container]);
        // The preview is the only sanitized HTML container in the editor: controls are attached to it, never to the raw fields.
        enhanceShortcodes([container]);
      }
    });
  }

  /** Fetch one preview. Before landing it first confirms it is still the newest generation; stale results and stale errors are both discarded. */
  async function runPreview(generation: number): Promise<void> {
    if (disposed || generation !== previewGeneration) return;
    const requestKind = kind;
    const requestFields = collectFields();
    let outcome: PreviewOutcome;
    try {
      const response = await apiClient.previewNote(deckId, requestKind, requestFields);
      if (disposed || generation !== previewGeneration) return;
      outcome = { ok: true, cards: response.cards };
    } catch (err) {
      if (disposed || generation !== previewGeneration) return;
      // 400 means "the fields are not complete yet / the card type cannot render", expressed separately from a fetch failure.
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
        kind,
        fields: collectFields(),
        tags: parseTags(),
      });
      if (disposed) return;
      // Identity lands first: even if the draft was changed while the request was in flight, later saves still point at the same card.
      note = updated;
      if (draftSignature === submitted) {
        // No new input during the request: the response can safely be collected into the form and reported as saved.
        adoptFields(updated);
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

  /** Save failure message: 400 means invalid content, so an actionable reason is given; everything else reports the generic failure. */
  function reportSaveError(err: unknown): void {
    const invalid = err instanceof ApiClientError && err.status === 400;
    if (isCreate) toast.error($t(invalid ? 'note_create.invalid' : 'note_create.failed'));
    else toast.error($t(invalid ? 'note_edit.invalid' : 'note_edit.failed'));
  }

  /** Create: it only jumps back to the deck page once creation is confirmed successful (not dry_run, created==1, no per-row errors). */
  async function create(): Promise<void> {
    saving = true;
    try {
      const result = await apiClient.createNotes(deckId, {
        notes: [{ kind, fields: collectFields(), tags: parseTags() }],
      });
      // The user left while the request was in flight (route change/unmount): no more state writes, and do not pull them to the deck page either.
      if (disposed) return;
      if (result.dry_run || result.created !== 1 || result.errors.length > 0) {
        reportSaveError(null);
        return;
      }
      navigate(`/decks/${encodeURIComponent(deckId)}`);
    } catch (err) {
      if (disposed) return;
      reportSaveError(err);
    } finally {
      if (!disposed) saving = false;
    }
  }

  // Live preview trigger point: after loading completes (the first-screen preview), every change to kind or the field contents reschedules once.
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
      // Speech and audio started from the preview must not outlive the editor.
      stopShortcodePlayback();
    };
  });
</script>

<Page as="section">
  <PageHeader
    title={isCreate ? $t('note_create.title') : $t('note_edit.title')}
    back={{ href: `/decks/${encodeURIComponent(deckId)}`, label: $t('note_edit.back') }}
  />
  {#if loading || (note && !$cardTypes && !metaError)}
    <Skeleton testId="note-edit-loading" label={$t('note_edit.loading')} lines={6} />
  {:else if !isCreate && loadError && !note}
    <p role="alert" data-testid="note-edit-not-found" class="text-sm text-destructive-foreground">{$t('note_edit.not_found')}</p>
  {:else if metaError && !$cardTypes}
    <p role="alert" data-testid="note-edit-metadata-error" class="text-sm text-destructive-foreground">{$t('common.error')}</p>
  {:else}
    <form onsubmit={submit} data-testid="note-editor-form">
      <div class="grid gap-8 lg:grid-cols-2 lg:items-start">
        <!-- Left column: input. Card type, fields, tags and media, separated into sections by headings and dividers, no longer nested in cards. -->
        <div class="min-w-0 space-y-8">
          <div class="space-y-5">
            <div>
              <span class="block text-sm font-medium text-foreground">{$t('note_edit.kind')}</span>
              <Select
                class="mt-1.5 w-full"
                testId="note-edit-kind"
                value={kind}
                onValueChange={changeKind}
                options={kindOptions}
              />
              <p class="mt-1.5 text-xs text-muted-foreground">{$t('note_edit.kind_hint')}</p>
            </div>

            <NoteFieldsForm specs={fieldsForKind($cardTypes, kind)} {fields} testIdPrefix="note-field" />

            <label class="block text-sm font-medium text-foreground">{$t('note_edit.tags')}
              <input data-testid="note-tags-editor" bind:value={tagsText} class="field-input text-sm mt-1.5 w-full" />
            </label>
          </div>

          <!-- The picker only references the original image; lazy loading and the height cap are handled by CSS. -->
          <div class="space-y-4 border-t border-border pt-6">
            <label class="block text-sm font-medium text-foreground">{$t('media.field')}
              <Select
                class="mt-1.5 w-full"
                bind:value={selectedField}
                testId="media-field"
                options={textFields}
              />
            </label>
            <!-- Upload area: the outer element is already the note form, so a form cannot be nested here; selecting uploads immediately, and success goes through the same insertMedia as the picker. -->
            <div class="space-y-2">
              <span class="block text-sm font-medium text-foreground">{$t('media.upload.label')}</span>
              <Dropzone
                testId="media-upload-input"
                name="file"
                disabled={uploading}
                hint={uploading ? $t('media.upload.uploading') : $t('dropzone.media_hint')}
                onfile={uploadMedia}
              />
            </div>
            <!-- Media library picker: data comes from GET /api/v1/media JSON; after selection it shares the same insertMedia as upload. -->
            <MediaPicker onselect={insertMedia} />
          </div>
        </div>

        <!-- Right column: live preview. Rescheduled on input, card type and insertion changes; on desktop it is sticky with
             its height capped at one screen, scrolling inside the column when the preview is longer than the screen so the
             heading stays visible; on mobile it flows right below the input. -->
        <div class="min-w-0 lg:sticky lg:top-6 lg:flex lg:max-h-[calc(100vh-3rem)] lg:flex-col">
          <div class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-surface">
            <div class="flex shrink-0 items-center justify-between gap-3 border-b border-border px-4 py-2.5">
              <h2 class="text-sm font-semibold text-foreground">{$t('note_preview.title')}</h2>
              <!-- The status copy gets a polite live region of its own: it only announces short phrases like "generating/incomplete/failed", while
                   the card body sits outside the live region so a reschedule does not read the whole HTML out again. -->
              <div aria-live="polite" class="min-w-0 truncate text-right text-xs">
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
            </div>

            <div class="min-h-0 flex-1 overflow-y-auto p-4">
              {#if previewStatus === 'empty'}
                <p data-testid="note-preview-empty" class="py-8 text-center text-sm text-muted-foreground">{$t('note_preview.empty')}</p>
              {/if}
              {#if previewCards}
                <!-- When the previous result is kept it is dimmed and aria-busy is set, making "stale but still rescheduling" visible instead of a blank flash. -->
                <div
                  bind:this={previewSection}
                  data-testid="note-preview-result"
                  aria-busy={previewStatus === 'loading'}
                  class="space-y-4 transition-opacity duration-150 {previewStatus === 'loading' ? 'opacity-60' : ''}"
                >
                  {#each previewCards as card, index}
                    <div class="divide-y divide-border overflow-hidden rounded-md border border-border bg-background">
                      <div data-testid="note-preview-front-{index}" class="space-y-2 p-4">
                        <h3 class="text-xs font-medium text-muted-foreground">{$t('note_preview.front')}</h3>
                        <!-- Only the HTML returned by the preview API is used; the backend sanitizes it via RenderMarkdown and bluemonday, and passing the editor's raw fields into an HTML sink is forbidden. -->
                        <div class="prose dark:prose-invert">{@html card.front_html}</div>
                      </div>
                      <div data-testid="note-preview-back-{index}" class="space-y-2 p-4">
                        <h3 class="text-xs font-medium text-muted-foreground">{$t('note_preview.back')}</h3>
                        <!-- Only the HTML returned by the preview API is used; the backend sanitizes it via RenderMarkdown and bluemonday, and passing the editor's raw fields into an HTML sink is forbidden. -->
                        <div class="prose dark:prose-invert">{@html card.back_html}</div>
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
          {isCreate ? $t('note_create.submit') : $t('note_edit.save')}
        </Button>
        <Button variant="ghost" size="lg" testId="note-cancel" onclick={() => navigate(`/decks/${encodeURIComponent(deckId)}`)}>
          {$t('note_edit.cancel')}
        </Button>
      </div>
    </form>
  {/if}
</Page>

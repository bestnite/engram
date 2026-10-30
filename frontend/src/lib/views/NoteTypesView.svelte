<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type {
    AnswerControl,
    AnswerControlRole,
    ChangeNoteTypeResult,
    EmptyCard,
    EmptyCardCleanupResult,
    NoteType,
    NoteTypeConfig,
    NoteTypeField,
    NoteTypeImpact,
    NoteTypePreviewResponse,
    NoteTypeStock,
    NoteTypeTemplate,
  } from '../api';
  import Button from '../components/ui/Button.svelte';
  import Dialog from '../components/ui/Dialog.svelte';
  import Select from '../components/ui/Select.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import CardFrame from '../components/CardFrame.svelte';
  import { toast } from '../components/ui/toast';
  import { NOTE_PREVIEW_CSS, faceStyle } from '../card-frame';
  import { rewriteFieldReferences } from '../note-type-template';
  import { ArrowDown, ArrowUp, Plus, RefreshCw, Trash2 } from '@lucide/svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialNoteTypes?: NoteType[] | null;
    initialStocks?: NoteTypeStock[] | null;
    initialControls?: AnswerControl[] | null;
  }

  let {
    initialLoading = true,
    initialError = null,
    initialNoteTypes = null,
    initialStocks = null,
    initialControls = null,
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let noteTypes = $state<NoteType[]>(initialNoteTypes ?? []);
  // svelte-ignore state_referenced_locally
  let stocks = $state<NoteTypeStock[]>(initialStocks ?? []);
  // svelte-ignore state_referenced_locally
  let answerControls = $state<AnswerControl[]>(initialControls ?? []);

  const deckId = $derived($routeStore.params.id || '');

  // The editor works on a clone, so leaving a type without saving never changes the list.
  interface Draft {
    id: string;
    name: string;
    version: number;
    config: NoteTypeConfig;
  }

  // The first type is opened during initialization, so an SSR render and a test fixture show the
  // editor without waiting for onMount; onMount only runs for the real, data-fetching page.
  // svelte-ignore state_referenced_locally
  const initialEntry = initialNoteTypes?.[0] ?? null;
  // svelte-ignore state_referenced_locally
  let draft = $state<Draft | null>(
    initialEntry
      ? { id: initialEntry.id, name: initialEntry.name, version: initialEntry.version, config: structuredClone(initialEntry.config) }
      : null,
  );
  // svelte-ignore state_referenced_locally
  let selectedTemplateId = $state(initialEntry?.config.templates[0]?.id ?? '');
  let saving = $state(false);
  let saveError = $state('');
  let deleting = $state(false);
  let deleteOpen = $state(false);
  let deleteError = $state('');

  let impact = $state<NoteTypeImpact | null>(null);
  let impactOpen = $state(false);

  // Deck-level empty-card cleanup. The list is read when the dialog opens; the dry run reports the
  // counts and the confirmation removes exactly those cards.
  let emptyOpen = $state(false);
  let emptyLoading = $state(false);
  let emptyError = $state('');
  let emptyCards = $state<EmptyCard[]>([]);
  let emptyImpact = $state<EmptyCardCleanupResult | null>(null);
  let emptyRemoving = $state(false);

  // Change-note-type flow for the open type. The field map is keyed by target field id and holds a
  // source field id; the template map is keyed by source template id and holds a target template id.
  let changeOpen = $state(false);
  let changeRunning = $state(false);
  let changeError = $state('');
  let changeTargetId = $state('');
  let changeFieldMap = $state<Record<string, string>>({});
  let changeTemplateMap = $state<Record<string, string>>({});
  let changeImpact = $state<ChangeNoteTypeResult | null>(null);
  let changeConfirmOpen = $state(false);

  // Create dialog: a stock definition, or a copy of one of the deck's existing types.
  let createOpen = $state(false);
  let createMode = $state<'stock' | 'copy'>('stock');
  let createName = $state('');
  let createStockId = $state('');
  let createSourceId = $state('');
  let createError = $state('');
  let creating = $state(false);

  const selectedTemplate = $derived(
    draft?.config.templates.find((entry) => entry.id === selectedTemplateId) ??
      draft?.config.templates[0] ??
      null,
  );

  // Stock picker labels come from the translation catalog through the server's label_key; the
  // stock's canonical English name is only the default note type name.
  const stockOptions = $derived(
    stocks.map((stock) => ({ value: stock.id, label: $t(stock.label_key) })),
  );

  const copyOptions = $derived(
    noteTypes.map((entry) => ({ value: entry.id, label: entry.name })),
  );

  const controlOptions = $derived([
    { value: '', label: $t('notetype.answer.none') },
    ...answerControls.map((control) => ({ value: control.name, label: control.name })),
  ]);

  // A change stays inside one deck and one kind: the store refuses a cross-deck target and a kind
  // change, so the picker never offers one.
  const changeTargets = $derived(
    noteTypes.filter(
      (entry) => entry.id !== draft?.id && entry.config.kind === draft?.config.kind,
    ),
  );
  const changeTarget = $derived(noteTypes.find((entry) => entry.id === changeTargetId) ?? null);
  const changeTargetFields = $derived(changeTarget?.config.fields ?? []);
  const changeTargetTemplates = $derived(changeTarget?.config.templates ?? []);
  const changeTargetOptions = $derived(
    changeTargets.map((entry) => ({ value: entry.id, label: entry.name })),
  );
  const changeTemplateRows = $derived(
    draft?.config.kind === 'normal' ? (draft?.config.templates ?? []) : [],
  );

  // The preview is rendered by the server: the request carries the whole unsaved config and the selected template id,
  // so the frames show the same faces a stored note type produces instead of a second renderer in this page. Every field
  // is rendered with its own name as the value. The returned HTML only enters the sandboxed frame, never this page.
  let preview = $state<NoteTypePreviewResponse | null>(null);
  let previewError = $state('');
  // The config is serialised into one key so a nested edit (a template, a field, the CSS) reruns the effect below; reading
  // the object alone would only notice a replaced config.
  const previewKey = $derived(
    draft && selectedTemplate
      ? JSON.stringify({ config: draft.config, template_id: selectedTemplate.id })
      : '',
  );

  $effect(() => {
    const key = previewKey;
    const config = draft?.config ?? null;
    const templateId = selectedTemplate?.id ?? '';
    if (!key || !deckId || !config || !templateId) {
      preview = null;
      previewError = '';
      return;
    }
    // Edits arrive keystroke by keystroke; one request per pause keeps the preview responsive without one render per
    // character. The cleanup drops both a pending timer and a response that arrives after the config changed again.
    let cancelled = false;
    const timer = setTimeout(() => {
      void (async () => {
        try {
          const result = await apiClient.previewNoteType(deckId, config, templateId, { frame: true });
          if (!cancelled) {
            preview = result;
            previewError = '';
          }
        } catch {
          if (!cancelled) {
            preview = null;
            previewError = 'notetype.preview.failed';
          }
        }
      })();
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  });

  const previewCSS = $derived(faceStyle(NOTE_PREVIEW_CSS, draft?.config.css ?? ''));

  const clozeKind = $derived(draft?.config.kind === 'cloze');
  const fromStock = $derived(draft?.config.original_stock_kind ?? '');
  const kindLabel = $derived(draft ? $t(`notetype.kind.${draft.config.kind}`) : '');
  const stockLabel = $derived(fromStock ? $t(`notetype.stock.anki.${fromStock}`) : '');

  function cloneConfig(config: NoteTypeConfig): NoteTypeConfig {
    return structuredClone(config);
  }

  /** Select a stored type into the editor. The stored list entry is never mutated. */
  function selectType(entry: NoteType): void {
    draft = {
      id: entry.id,
      name: entry.name,
      version: entry.version,
      config: cloneConfig(entry.config),
    };
    selectedTemplateId = entry.config.templates[0]?.id ?? '';
    saveError = '';
    deleteError = '';
  }

  async function load(): Promise<void> {
    if (!deckId) return;
    loading = true;
    loadError = null;
    try {
      const [types, stockList, controls] = await Promise.all([
        apiClient.listNoteTypes(deckId),
        apiClient.listNoteTypeStocks(),
        apiClient.listAnswerControls(),
      ]);
      noteTypes = types.note_types;
      stocks = stockList.stocks;
      answerControls = controls.controls;
      const first = noteTypes[0];
      if (first) selectType(first);
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && loadError.isUnauthorized) return 'error.unauthorized';
    if (loadError instanceof ApiClientError && loadError.isForbidden) return 'notetype.forbidden';
    if (loadError instanceof ApiClientError && loadError.isNotFound) return 'error.not_found';
    return 'notetype.failed';
  }

  function nextID(existing: Array<{ id: string }>, prefix: string): string {
    let index = existing.length;
    const used = new Set(existing.map((entry) => entry.id));
    while (used.has(`${prefix}${index}`)) index += 1;
    return `${prefix}${index}`;
  }

  function addField(): void {
    if (!draft) return;
    const fields = draft.config.fields;
    const field: NoteTypeField = {
      id: nextID(fields, 'f'),
      name: $t('notetype.fields.new_name', { index: fields.length + 1 }),
      sticky: false,
      rtl: false,
      font: '',
      font_size: 0,
      description: '',
      plain_text: false,
      collapsed: false,
      exclude_from_search: false,
      tag: null,
      prevent_deletion: false,
    };
    fields.push(field);
    if (!draft.config.sort_field_id) draft.config.sort_field_id = field.id;
  }

  function moveField(index: number, delta: number): void {
    if (!draft) return;
    const fields = draft.config.fields;
    const target = index + delta;
    if (target < 0 || target >= fields.length) return;
    const [field] = fields.splice(index, 1);
    if (!field) return;
    fields.splice(target, 0, field);
  }

  /**
   * Rename a field and rewrite every template reference, as Anki does. The field id stays the
   * same, so note values and answer-spec bindings need no rewrite.
   */
  function renameField(index: number, rawName: string): void {
    if (!draft) return;
    const field = draft.config.fields[index];
    if (!field) return;
    const name = rawName.trim();
    if (name === '' || name === field.name) return;
    const oldName = field.name;
    field.name = name;
    for (const template of draft.config.templates) {
      template.qfmt = rewriteFieldReferences(template.qfmt, oldName, name);
      template.afmt = rewriteFieldReferences(template.afmt, oldName, name);
      if (template.bqfmt) template.bqfmt = rewriteFieldReferences(template.bqfmt, oldName, name);
      if (template.bafmt) template.bafmt = rewriteFieldReferences(template.bafmt, oldName, name);
    }
  }

  function deleteField(index: number): void {
    if (!draft) return;
    const field = draft.config.fields[index];
    if (!field || field.prevent_deletion || draft.config.fields.length <= 1) return;
    draft.config.fields.splice(index, 1);
    if (draft.config.sort_field_id === field.id) {
      draft.config.sort_field_id = draft.config.fields[0]?.id ?? '';
    }
  }

  function addTemplate(): void {
    if (!draft) return;
    // A cloze note type declares exactly one template; the server rejects a second one.
    if (clozeKind && draft.config.templates.length >= 1) return;
    const fields = draft.config.fields;
    const first = fields[0]?.name ?? '';
    const second = fields[1]?.name ?? first;
    const template: NoteTypeTemplate = {
      id: nextID(draft.config.templates, 't'),
      name: $t('notetype.templates.new_name', { index: draft.config.templates.length + 1 }),
      qfmt: `{{${first}}}`,
      afmt: `{{FrontSide}}\n\n<hr id=answer>\n\n{{${second}}}`,
      bqfmt: '',
      bafmt: '',
      browser_font: '',
      browser_font_size: 0,
      answer: null,
    };
    draft.config.templates.push(template);
    selectedTemplateId = template.id;
  }

  function moveTemplate(index: number, delta: number): void {
    if (!draft) return;
    const templates = draft.config.templates;
    const target = index + delta;
    if (target < 0 || target >= templates.length) return;
    const [template] = templates.splice(index, 1);
    if (!template) return;
    templates.splice(target, 0, template);
  }

  function deleteTemplate(index: number): void {
    if (!draft) return;
    // One template is the minimum, and a cloze type is fixed at one.
    if (clozeKind || draft.config.templates.length <= 1) return;
    const removed = draft.config.templates.splice(index, 1)[0];
    if (removed && selectedTemplateId === removed.id) {
      selectedTemplateId = draft.config.templates[0]?.id ?? '';
    }
  }

  function controlFor(name: string | undefined): AnswerControl | undefined {
    return answerControls.find((control) => control.name === name);
  }

  function rolesFor(template: NoteTypeTemplate): AnswerControlRole[] {
    return controlFor(template.answer?.control)?.roles ?? [];
  }

  function answerControlValue(template: NoteTypeTemplate): string {
    return template.answer?.control ?? '';
  }

  /** Switching the control keeps matching bindings and drops roles the new control does not read. */
  function setAnswerControl(template: NoteTypeTemplate, controlName: string): void {
    if (controlName === '') {
      template.answer = null;
      return;
    }
    const control = controlFor(controlName);
    if (!control) return;
    const previous = template.answer?.bindings ?? {};
    const bindings: Record<string, string> = {};
    for (const role of control.roles) {
      const kept = previous[role.name];
      if (kept && draft?.config.fields.some((field) => field.id === kept)) {
        bindings[role.name] = kept;
        continue;
      }
      // Pre-fill a role with the field of the same name, which is how the stock types bind them.
      const matching = draft?.config.fields.find(
        (field) => field.name === role.name || field.id === role.name,
      );
      if (matching) bindings[role.name] = matching.id;
    }
    template.answer = { control: controlName, bindings };
  }

  function bindingValue(template: NoteTypeTemplate, role: string): string {
    return template.answer?.bindings[role] ?? '';
  }

  function setBinding(template: NoteTypeTemplate, role: string, fieldID: string): void {
    if (!template.answer) return;
    if (fieldID === '') delete template.answer.bindings[role];
    else template.answer.bindings[role] = fieldID;
  }

  /** The counts of a 409 card_removal_unconfirmed body; the client keeps the whole body in details. */
  function impactFromError(err: ApiClientError): NoteTypeImpact | null {
    const body = err.details as Partial<NoteTypeImpact> | undefined;
    if (
      !body ||
      typeof body.cards_added !== 'number' ||
      typeof body.cards_removed !== 'number' ||
      typeof body.reviews_on_removed_cards !== 'number'
    ) {
      return null;
    }
    return {
      cards_added: body.cards_added,
      cards_removed: body.cards_removed,
      reviews_on_removed_cards: body.reviews_on_removed_cards,
    };
  }

  function saveErrorKey(err: unknown): string {
    if (err instanceof ApiClientError) {
      if (err.code === 'version_conflict') return 'notetype.error.version';
      if (err.isForbidden) return 'notetype.error.forbidden';
      if (err.code === 'invalid_request') return 'notetype.error.invalid';
    }
    return 'notetype.error.failed';
  }

  /**
   * Save through a dry run first. A change that adds or removes cards must show the counts before
   * anything is written; a change with no card impact is saved immediately after the dry run.
   */
  async function requestSave(): Promise<void> {
    if (!draft) return;
    if (draft.name.trim() === '') {
      saveError = 'notetype.error.name';
      return;
    }
    saving = true;
    saveError = '';
    try {
      const preview = await apiClient.updateNoteType(deckId, draft.id, {
        name: draft.name,
        config: draft.config,
        version: draft.version,
        dry_run: true,
      });
      if (preview.cards_added !== 0 || preview.cards_removed !== 0) {
        impact = {
          cards_added: preview.cards_added,
          cards_removed: preview.cards_removed,
          reviews_on_removed_cards: preview.reviews_on_removed_cards,
        };
        impactOpen = true;
        return;
      }
      await commitSave(false);
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'card_removal_unconfirmed') {
        const counts = impactFromError(err);
        if (counts) {
          impact = counts;
          impactOpen = true;
          return;
        }
      }
      saveError = saveErrorKey(err);
    } finally {
      saving = false;
    }
  }

  /** Write the clone. confirmRemoval is required exactly when cards would be removed. */
  async function commitSave(confirmRemoval: boolean): Promise<void> {
    if (!draft) return;
    try {
      const updated = await apiClient.updateNoteType(deckId, draft.id, {
        name: draft.name,
        config: draft.config,
        version: draft.version,
        dry_run: false,
        confirm_card_removal: confirmRemoval,
      });
      noteTypes = noteTypes.map((entry) => (entry.id === updated.id ? updated : entry));
      draft = {
        id: updated.id,
        name: updated.name,
        version: updated.version,
        config: cloneConfig(updated.config),
      };
      selectedTemplateId =
        updated.config.templates.find((entry) => entry.id === selectedTemplateId)?.id ??
        updated.config.templates[0]?.id ??
        '';
      impactOpen = false;
      impact = null;
      saveError = '';
      toast.success($t('notetype.type.saved'));
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'card_removal_unconfirmed') {
        const counts = impactFromError(err);
        if (counts) {
          impact = counts;
          impactOpen = true;
          return;
        }
      }
      saveError = saveErrorKey(err);
    }
  }

  async function confirmImpact(): Promise<void> {
    if (!impact) return;
    saving = true;
    try {
      await commitSave(impact.cards_removed > 0);
    } finally {
      saving = false;
    }
  }

  function deleteErrorKey(err: unknown): string {
    if (err instanceof ApiClientError && err.code === 'note_type_in_use') return 'notetype.type.delete.in_use';
    if (err instanceof ApiClientError && err.isForbidden) return 'notetype.error.forbidden';
    return 'notetype.type.delete.failed';
  }

  async function confirmDelete(): Promise<void> {
    if (!draft || deleting) return;
    deleting = true;
    deleteError = '';
    try {
      await apiClient.deleteNoteType(deckId, draft.id);
      const remaining = noteTypes.filter((entry) => entry.id !== draft?.id);
      noteTypes = remaining;
      draft = null;
      deleteOpen = false;
      const next = remaining[0];
      if (next) selectType(next);
    } catch (err) {
      deleteError = deleteErrorKey(err);
    } finally {
      deleting = false;
    }
  }

  /** Reload the deck's types after a content operation, keeping the open type selected when it still exists. */
  async function reloadTypes(): Promise<void> {
    if (!deckId) return;
    try {
      const types = await apiClient.listNoteTypes(deckId);
      noteTypes = types.note_types;
      const open = draft ? noteTypes.find((entry) => entry.id === draft?.id) : undefined;
      if (open) selectType(open);
      else {
        draft = null;
        const first = noteTypes[0];
        if (first) selectType(first);
      }
    } catch {
      saveError = 'notetype.error.failed';
    }
  }

  /** The display name of a note type id, falling back to the id when the row is gone. */
  function noteTypeName(id: string): string {
    return noteTypes.find((entry) => entry.id === id)?.name ?? id;
  }

  /** The display name of a template of one note type, falling back to the template id. */
  function templateName(noteTypeId: string, templateId: string): string {
    const type = noteTypes.find((entry) => entry.id === noteTypeId);
    return type?.config.templates.find((template) => template.id === templateId)?.name ?? templateId;
  }

  function emptyErrorKey(err: unknown): string {
    if (err instanceof ApiClientError && err.isForbidden) return 'notetype.error.forbidden';
    return 'notetype.empty.cleanup.error';
  }

  /** Open the deck-level cleanup and read the list it acts on. */
  async function openEmptyCards(): Promise<void> {
    emptyOpen = true;
    emptyLoading = true;
    emptyError = '';
    emptyImpact = null;
    emptyCards = [];
    try {
      const listed = await apiClient.listEmptyCards(deckId);
      emptyCards = listed.cards;
    } catch (err) {
      emptyError = emptyErrorKey(err);
    } finally {
      emptyLoading = false;
    }
  }

  /** Report what the cleanup would remove without writing. */
  async function previewEmptyCards(): Promise<void> {
    if (emptyLoading) return;
    emptyError = '';
    try {
      emptyImpact = await apiClient.cleanupEmptyCards(deckId, { dry_run: true });
    } catch (err) {
      emptyError = emptyErrorKey(err);
    }
  }

  /** Remove the listed cards, then refresh the type list the deck page shows. */
  async function confirmEmptyCards(): Promise<void> {
    if (emptyRemoving) return;
    emptyRemoving = true;
    emptyError = '';
    try {
      emptyImpact = await apiClient.cleanupEmptyCards(deckId, { confirm_card_removal: true });
      emptyCards = [];
      toast.success($t('notetype.empty.cleanup.done'));
      await reloadTypes();
    } catch (err) {
      emptyError = emptyErrorKey(err);
    } finally {
      emptyRemoving = false;
    }
  }

  /** Open the change dialog on the first eligible target, with mappings prefilled by field and template name. */
  function openChange(): void {
    if (!draft) return;
    changeOpen = true;
    changeError = '';
    changeImpact = null;
    changeConfirmOpen = false;
    chooseChangeTarget(changeTargets[0]?.id ?? '');
  }

  /** Select the target type and rebuild the mapping from names the two types share. */
  function chooseChangeTarget(targetId: string): void {
    changeTargetId = targetId;
    changeFieldMap = {};
    changeTemplateMap = {};
    changeImpact = null;
    const target = noteTypes.find((entry) => entry.id === targetId);
    if (!target || !draft) return;
    const used = new Set<string>();
    for (const field of target.config.fields) {
      const source = draft.config.fields.find(
        (entry) => entry.name === field.name && !used.has(entry.id),
      );
      if (!source) continue;
      used.add(source.id);
      changeFieldMap = { ...changeFieldMap, [field.id]: source.id };
    }
    if (draft.config.kind !== 'normal') return;
    for (const template of draft.config.templates) {
      const match = target.config.templates.find((entry) => entry.name === template.name);
      if (match) changeTemplateMap = { ...changeTemplateMap, [template.id]: match.id };
    }
  }

  /** Assign the source field that fills one target field, or clear the mapping. */
  function setChangeField(targetFieldId: string, sourceFieldId: string): void {
    const next = { ...changeFieldMap };
    if (sourceFieldId === '') delete next[targetFieldId];
    else next[targetFieldId] = sourceFieldId;
    changeFieldMap = next;
    changeImpact = null;
  }

  /** Send one source template's cards to a target template, or clear the mapping so they are removed. */
  function setChangeTemplate(sourceTemplateId: string, targetTemplateId: string): void {
    const next = { ...changeTemplateMap };
    if (targetTemplateId === '') delete next[sourceTemplateId];
    else next[sourceTemplateId] = targetTemplateId;
    changeTemplateMap = next;
    changeImpact = null;
  }

  /** Source fields offered to one target field; a field already feeding another target stays out of the list. */
  function sourceFieldOptions(targetFieldId: string): Array<{ value: string; label: string }> {
    const used = new Set(
      Object.entries(changeFieldMap)
        .filter(([target]) => target !== targetFieldId)
        .map(([, source]) => source),
    );
    return [
      { value: '', label: $t('notetype.change.no_source') },
      ...(draft?.config.fields ?? [])
        .filter((field) => !used.has(field.id))
        .map((field) => ({ value: field.id, label: field.name })),
    ];
  }

  /** Target templates offered to one source template; a target already taken by another source stays out. */
  function targetTemplateOptions(sourceTemplateId: string): Array<{ value: string; label: string }> {
    const used = new Set(
      Object.entries(changeTemplateMap)
        .filter(([source]) => source !== sourceTemplateId)
        .map(([, target]) => target),
    );
    return [
      { value: '', label: $t('notetype.change.no_template') },
      ...changeTargetTemplates
        .filter((template) => !used.has(template.id))
        .map((template) => ({ value: template.id, label: template.name })),
    ];
  }

  /** Build the request body. The server reads the field map source-to-target, so the target-keyed map is inverted. */
  function changeRequestBody(dryRun: boolean, confirmRemoval: boolean) {
    const fieldMapping: Record<string, string> = {};
    for (const [targetField, sourceField] of Object.entries(changeFieldMap)) {
      if (sourceField) fieldMapping[sourceField] = targetField;
    }
    return {
      target_note_type: changeTargetId,
      field_mapping: fieldMapping,
      template_mapping: draft?.config.kind === 'normal' ? { ...changeTemplateMap } : {},
      dry_run: dryRun,
      confirm_card_removal: confirmRemoval,
    };
  }

  function changeErrorKey(err: unknown): string {
    if (err instanceof ApiClientError) {
      if (err.isForbidden) return 'notetype.error.forbidden';
      if (err.code === 'invalid_request') return 'notetype.change.invalid';
      if (err.isNotFound) return 'notetype.change.not_found';
    }
    return 'notetype.change.error';
  }

  /** The counts of a 409 card_removal_unconfirmed body; the client keeps the whole body in details. */
  function changeImpactFromError(err: ApiClientError): ChangeNoteTypeResult | null {
    const body = err.details as Partial<ChangeNoteTypeResult> | undefined;
    if (
      !body ||
      typeof body.notes_moved !== 'number' ||
      typeof body.cards_moved !== 'number' ||
      typeof body.cards_removed !== 'number' ||
      typeof body.reviews_on_removed_cards !== 'number'
    ) {
      return null;
    }
    return {
      dry_run: false,
      notes_moved: body.notes_moved,
      cards_moved: body.cards_moved,
      cards_removed: body.cards_removed,
      reviews_on_removed_cards: body.reviews_on_removed_cards,
    };
  }

  /** Report the impact without writing. */
  async function previewChange(): Promise<void> {
    if (!draft || !changeTarget) {
      changeError = 'notetype.change.no_targets';
      return;
    }
    changeRunning = true;
    changeError = '';
    try {
      changeImpact = await apiClient.changeNoteType(deckId, draft.id, changeRequestBody(true, false));
    } catch (err) {
      changeError = changeErrorKey(err);
    } finally {
      changeRunning = false;
    }
  }

  /**
   * Run the dry run that gates the write, then either ask for the extra confirmation or commit.
   * A change that removes cards always needs the second confirmation before anything is written.
   */
  async function requestChange(): Promise<void> {
    if (!draft || !changeTarget) {
      changeError = 'notetype.change.no_targets';
      return;
    }
    changeRunning = true;
    changeError = '';
    try {
      const preview =
        changeImpact ?? (await apiClient.changeNoteType(deckId, draft.id, changeRequestBody(true, false)));
      changeImpact = preview;
      if (preview.cards_removed > 0) {
        changeConfirmOpen = true;
        return;
      }
      await commitChange(false);
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'card_removal_unconfirmed') {
        const counts = changeImpactFromError(err);
        if (counts) {
          changeImpact = counts;
          changeConfirmOpen = true;
          return;
        }
      }
      changeError = changeErrorKey(err);
    } finally {
      changeRunning = false;
    }
  }

  /** Write the change. confirmRemoval is required exactly when cards would be removed. */
  async function commitChange(confirmRemoval: boolean): Promise<void> {
    if (!draft || !changeTarget) return;
    changeRunning = true;
    changeError = '';
    try {
      changeImpact = await apiClient.changeNoteType(
        deckId,
        draft.id,
        changeRequestBody(false, confirmRemoval),
      );
      changeConfirmOpen = false;
      changeOpen = false;
      toast.success($t('notetype.change.done'));
      await reloadTypes();
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'card_removal_unconfirmed') {
        const counts = changeImpactFromError(err);
        if (counts) {
          changeImpact = counts;
          changeConfirmOpen = true;
          return;
        }
      }
      changeError = changeErrorKey(err);
    } finally {
      changeRunning = false;
    }
  }

  function openCreate(): void {
    createOpen = true;
    createMode = 'stock';
    createError = '';
    createStockId = stocks[0]?.id ?? '';
    createName = stocks[0]?.name ?? '';
    createSourceId = noteTypes[0]?.id ?? '';
  }

  function chooseCreateMode(mode: 'stock' | 'copy'): void {
    createMode = mode;
    createError = '';
    if (mode === 'stock') {
      const stock = stocks.find((entry) => entry.id === createStockId) ?? stocks[0];
      createStockId = stock?.id ?? '';
      createName = stock?.name ?? '';
    } else {
      createSourceId = noteTypes[0]?.id ?? '';
      createName = noteTypes[0] ? $t('notetype.create.copy_name', { name: noteTypes[0].name }) : '';
    }
  }

  function chooseStock(stockId: string): void {
    createStockId = stockId;
    const stock = stocks.find((entry) => entry.id === stockId);
    if (stock) createName = stock.name;
  }

  function chooseCopySource(sourceId: string): void {
    createSourceId = sourceId;
    const source = noteTypes.find((entry) => entry.id === sourceId);
    if (source) createName = $t('notetype.create.copy_name', { name: source.name });
  }

  async function submitCreate(): Promise<void> {
    if (creating) return;
    const name = createName.trim();
    if (name === '') {
      createError = 'notetype.error.name';
      return;
    }
    creating = true;
    createError = '';
    try {
      let created: NoteType;
      if (createMode === 'stock') {
        created = await apiClient.createNoteType(deckId, { name, stock_id: createStockId });
      } else {
        const source = noteTypes.find((entry) => entry.id === createSourceId);
        if (!source) {
          createError = 'notetype.error.copy_source';
          return;
        }
        const config = cloneConfig(source.config);
        // A copy is its own type, not a stock type, and carries no Anki import origin.
        config.original_stock_kind = null;
        created = await apiClient.createNoteType(deckId, { name, config });
      }
      noteTypes = [...noteTypes, created];
      createOpen = false;
      selectType(created);
      toast.success($t('notetype.create.created'));
    } catch (err) {
      createError = saveErrorKey(err);
    } finally {
      creating = false;
    }
  }

  onMount(() => {
    // A view rendered with fixture data in a test does not fetch; the real shell always has none.
    if (initialNoteTypes === null && initialError === null) void load();
    else if (noteTypes.length > 0 && draft === null) {
      const first = noteTypes[0];
      if (first) selectType(first);
    }
  });
</script>

<Page testId="note-types-view">
  <PageHeader
    title={$t('notetype.title')}
    back={{ href: `/decks/${encodeURIComponent(deckId)}`, label: $t('notetype.back'), testId: 'notetype-back' }}
  >
    {#snippet actions()}
      <Button variant="outline" size="lg" onclick={() => void openEmptyCards()} testId="notetype-empty-open">
        <span>{$t('notetype.empty.cleanup.action')}</span>
      </Button>
      <Button variant="outline" size="lg" onclick={openCreate} testId="notetype-create-open">
        <Plus class="size-4" aria-hidden="true" />
        <span>{$t('notetype.create')}</span>
      </Button>
    {/snippet}
  </PageHeader>

  {#if loading}
    <Skeleton testId="note-types-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div
      data-testid={loadError instanceof ApiClientError && loadError.isForbidden ? 'note-types-forbidden' : 'note-types-failed'}
      class="py-16 text-center"
    >
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="note-types-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('common.retry')}</span>
      </Button>
    </div>
  {:else if noteTypes.length === 0}
    <div data-testid="note-types-empty" class="rounded-lg border border-border bg-surface/40 px-6 py-12 text-center">
      <h2 class="text-base font-semibold text-foreground">{$t('notetype.empty.title')}</h2>
      <p class="mx-auto mt-1 max-w-lg text-sm text-muted-foreground">{$t('notetype.empty.hint')}</p>
      <Button class="mt-4" onclick={openCreate} testId="note-types-empty-create">
        <Plus class="size-4" aria-hidden="true" />
        <span>{$t('notetype.create')}</span>
      </Button>
    </div>
  {:else}
    <div class="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
      <nav aria-label={$t('notetype.list')} data-testid="notetype-list" class="space-y-1 self-start rounded-lg border border-border p-2">
        {#each noteTypes as entry (entry.id)}
          <button
            type="button"
            data-testid={`notetype-item-${entry.id}`}
            aria-current={entry.id === draft?.id ? 'true' : undefined}
            class="flex w-full items-center justify-between gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors {entry.id === draft?.id ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}"
            onclick={() => selectType(entry)}
          >
            <span class="truncate">{entry.name}</span>
            <span class="shrink-0 font-mono text-[11px] text-muted-foreground">v{entry.version}</span>
          </button>
        {/each}
      </nav>

      {#if draft}
        <section data-testid="notetype-editor" class="min-w-0 space-y-6">
          <header class="flex flex-wrap items-end justify-between gap-3">
            <div class="min-w-0">
              <label class="block">
                <span class="text-xs font-medium text-muted-foreground">{$t('notetype.type.name')}</span>
                <input
                  type="text"
                  value={draft.name}
                  oninput={(event) => (draft!.name = event.currentTarget.value)}
                  data-testid="notetype-name"
                  class="field-input mt-1 block w-full max-w-sm text-base font-semibold"
                />
              </label>
              <p class="mt-1 text-xs text-muted-foreground">
                {$t('notetype.type.kind')}: {kindLabel}
                {#if stockLabel}&middot; {$t('notetype.type.stock')}: {stockLabel}{/if}
                &middot; {$t('notetype.type.version', { version: draft.version })}
              </p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <Button variant="outline" onclick={openChange} disabled={changeTargets.length === 0} testId="notetype-change-open">
                <span>{$t('notetype.change.action')}</span>
              </Button>
              <Button variant="danger-outline" onclick={() => { deleteError = ''; deleteOpen = true; }} testId="notetype-delete-open">
                <Trash2 class="size-4" aria-hidden="true" />
                <span>{$t('notetype.type.delete')}</span>
              </Button>
              <Button onclick={() => void requestSave()} loading={saving} testId="notetype-save">
                <span>{$t('notetype.type.save')}</span>
              </Button>
            </div>
          </header>

          {#if saveError}
            <p role="alert" data-testid="notetype-save-error" class="rounded-md bg-destructive-soft px-3 py-2 text-sm text-destructive-foreground">
              {$t(saveError)}
            </p>
          {/if}

          <!-- Fields -->
          <section class="rounded-lg border border-border" data-testid="notetype-fields">
            <header class="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
              <div>
                <h2 class="text-sm font-semibold text-foreground">{$t('notetype.fields.title')}</h2>
                <p class="mt-0.5 text-xs text-muted-foreground">{$t('notetype.fields.hint')}</p>
              </div>
              <Button variant="outline" size="sm" onclick={addField} testId="notetype-field-add">
                <Plus class="size-3.5" aria-hidden="true" />
                <span>{$t('notetype.fields.add')}</span>
              </Button>
            </header>
            <ul class="divide-y divide-border">
              {#each draft.config.fields as field, index (field.id)}
                <li class="px-4 py-3" data-testid={`notetype-field-${field.id}`}>
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="w-6 text-center text-xs tabular-nums text-muted-foreground">{index + 1}</span>
                    <input
                      type="text"
                      value={field.name}
                      onchange={(event) => renameField(index, event.currentTarget.value)}
                      aria-label={`${$t('notetype.fields.name')} ${index + 1}`}
                      data-testid={`notetype-field-name-${field.id}`}
                      class="field-input min-w-40 flex-1 text-sm"
                    />
                    <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">{field.id}</code>
                    <Button variant="ghost" size="icon" label={$t('notetype.fields.up')} disabled={index === 0} onclick={() => moveField(index, -1)} testId={`notetype-field-up-${field.id}`}>
                      <ArrowUp class="size-4" aria-hidden="true" />
                    </Button>
                    <Button variant="ghost" size="icon" label={$t('notetype.fields.down')} disabled={index === draft!.config.fields.length - 1} onclick={() => moveField(index, 1)} testId={`notetype-field-down-${field.id}`}>
                      <ArrowDown class="size-4" aria-hidden="true" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      label={$t('notetype.fields.delete')}
                      disabled={field.prevent_deletion || draft!.config.fields.length <= 1}
                      onclick={() => deleteField(index)}
                      testId={`notetype-field-delete-${field.id}`}
                    >
                      <Trash2 class="size-4" aria-hidden="true" />
                    </Button>
                  </div>
                  <details class="mt-2">
                    <summary class="cursor-pointer text-xs text-muted-foreground">{$t('notetype.fields.options')}</summary>
                    <div class="mt-2 grid gap-3 sm:grid-cols-2">
                      <label class="flex items-center gap-2 text-xs text-foreground">
                        <input type="checkbox" checked={field.sticky} onchange={(event) => (field.sticky = event.currentTarget.checked)} />
                        {$t('notetype.fields.sticky')}
                      </label>
                      <label class="flex items-center gap-2 text-xs text-foreground">
                        <input type="checkbox" checked={field.rtl} onchange={(event) => (field.rtl = event.currentTarget.checked)} />
                        {$t('notetype.fields.rtl')}
                      </label>
                      <label class="flex items-center gap-2 text-xs text-foreground">
                        <input type="checkbox" checked={field.plain_text} onchange={(event) => (field.plain_text = event.currentTarget.checked)} />
                        {$t('notetype.fields.plain_text')}
                      </label>
                      <label class="flex items-center gap-2 text-xs text-foreground">
                        <input type="checkbox" checked={field.collapsed} onchange={(event) => (field.collapsed = event.currentTarget.checked)} />
                        {$t('notetype.fields.collapsed')}
                      </label>
                      <label class="flex items-center gap-2 text-xs text-foreground">
                        <input type="checkbox" checked={field.exclude_from_search ?? false} onchange={(event) => (field.exclude_from_search = event.currentTarget.checked)} />
                        {$t('notetype.fields.exclude_from_search')}
                      </label>
                      <label class="flex items-center gap-2 text-xs text-foreground">
                        <input type="checkbox" checked={field.prevent_deletion ?? false} onchange={(event) => (field.prevent_deletion = event.currentTarget.checked)} />
                        {$t('notetype.fields.prevent_deletion')}
                      </label>
                      <label class="block text-xs text-foreground">
                        <span class="text-muted-foreground">{$t('notetype.fields.font')}</span>
                        <input type="text" value={field.font} oninput={(event) => (field.font = event.currentTarget.value)} class="field-input mt-1 block w-full text-xs" />
                      </label>
                      <label class="block text-xs text-foreground">
                        <span class="text-muted-foreground">{$t('notetype.fields.font_size')}</span>
                        <input type="number" min="0" value={field.font_size} oninput={(event) => (field.font_size = Number(event.currentTarget.value) || 0)} class="field-input mt-1 block w-full text-xs" />
                      </label>
                      <label class="block text-xs text-foreground sm:col-span-2">
                        <span class="text-muted-foreground">{$t('notetype.fields.description')}</span>
                        <input type="text" value={field.description} oninput={(event) => (field.description = event.currentTarget.value)} class="field-input mt-1 block w-full text-xs" />
                      </label>
                    </div>
                  </details>
                </li>
              {/each}
            </ul>
          </section>

          <!-- Templates -->
          <section class="rounded-lg border border-border" data-testid="notetype-templates">
            <header class="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
              <div>
                <h2 class="text-sm font-semibold text-foreground">{$t('notetype.templates.title')}</h2>
                <p class="mt-0.5 text-xs text-muted-foreground">
                  {clozeKind ? $t('notetype.templates.cloze_locked') : $t('notetype.templates.hint')}
                </p>
              </div>
              <Button variant="outline" size="sm" onclick={addTemplate} disabled={clozeKind} testId="notetype-template-add">
                <Plus class="size-3.5" aria-hidden="true" />
                <span>{$t('notetype.templates.add')}</span>
              </Button>
            </header>
            <div class="space-y-2 border-b border-border p-3">
              {#each draft.config.templates as template, index (template.id)}
                <div class="flex flex-wrap items-center gap-2 rounded-md px-2 py-1.5 {template.id === selectedTemplateId ? 'bg-muted' : ''}" data-testid={`notetype-template-row-${template.id}`}>
                  <button type="button" class="min-w-32 flex-1 truncate text-left text-sm" onclick={() => (selectedTemplateId = template.id)} data-testid={`notetype-template-select-${template.id}`}>
                    {template.name}
                  </button>
                  <Button variant="ghost" size="icon" label={$t('notetype.templates.up')} disabled={index === 0} onclick={() => moveTemplate(index, -1)} testId={`notetype-template-up-${template.id}`}>
                    <ArrowUp class="size-4" aria-hidden="true" />
                  </Button>
                  <Button variant="ghost" size="icon" label={$t('notetype.templates.down')} disabled={index === draft!.config.templates.length - 1} onclick={() => moveTemplate(index, 1)} testId={`notetype-template-down-${template.id}`}>
                    <ArrowDown class="size-4" aria-hidden="true" />
                  </Button>
                  <Button variant="ghost" size="icon" label={$t('notetype.templates.delete')} disabled={clozeKind || draft!.config.templates.length <= 1} onclick={() => deleteTemplate(index)} testId={`notetype-template-delete-${template.id}`}>
                    <Trash2 class="size-4" aria-hidden="true" />
                  </Button>
                </div>
              {/each}
            </div>

            {#if selectedTemplate}
              <div class="space-y-4 p-4">
                <label class="block max-w-sm">
                  <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.name')}</span>
                  <input
                    type="text"
                    value={selectedTemplate.name}
                    oninput={(event) => {
                      const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                      if (template) template.name = event.currentTarget.value;
                    }}
                    data-testid="notetype-template-name"
                    class="field-input mt-1 block w-full text-sm"
                  />
                </label>

                <div class="grid gap-4 xl:grid-cols-2">
                  <label class="block">
                    <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.front')}</span>
                    <textarea
                      rows="7"
                      value={selectedTemplate.qfmt}
                      oninput={(event) => {
                        const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                        if (template) template.qfmt = event.currentTarget.value;
                      }}
                      data-testid="notetype-template-qfmt"
                      class="field-input mt-1 block w-full font-mono text-xs"
                    ></textarea>
                  </label>
                  <label class="block">
                    <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.back')}</span>
                    <textarea
                      rows="7"
                      value={selectedTemplate.afmt}
                      oninput={(event) => {
                        const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                        if (template) template.afmt = event.currentTarget.value;
                      }}
                      data-testid="notetype-template-afmt"
                      class="field-input mt-1 block w-full font-mono text-xs"
                    ></textarea>
                  </label>
                </div>

                <details>
                  <summary class="cursor-pointer text-xs text-muted-foreground">{$t('notetype.templates.browser')}</summary>
                  <div class="mt-3 grid gap-4 xl:grid-cols-2">
                    <label class="block">
                      <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.browser_front')}</span>
                      <textarea
                        rows="4"
                        value={selectedTemplate.bqfmt ?? ''}
                        oninput={(event) => {
                          const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                          if (template) template.bqfmt = event.currentTarget.value;
                        }}
                        data-testid="notetype-template-bqfmt"
                        class="field-input mt-1 block w-full font-mono text-xs"
                      ></textarea>
                    </label>
                    <label class="block">
                      <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.browser_back')}</span>
                      <textarea
                        rows="4"
                        value={selectedTemplate.bafmt ?? ''}
                        oninput={(event) => {
                          const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                          if (template) template.bafmt = event.currentTarget.value;
                        }}
                        data-testid="notetype-template-bafmt"
                        class="field-input mt-1 block w-full font-mono text-xs"
                      ></textarea>
                    </label>
                    <label class="block">
                      <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.browser_font')}</span>
                      <input
                        type="text"
                        value={selectedTemplate.browser_font ?? ''}
                        oninput={(event) => {
                          const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                          if (template) template.browser_font = event.currentTarget.value;
                        }}
                        class="field-input mt-1 block w-full text-xs"
                      />
                    </label>
                    <label class="block">
                      <span class="text-xs font-medium text-muted-foreground">{$t('notetype.templates.browser_font_size')}</span>
                      <input
                        type="number"
                        min="0"
                        value={selectedTemplate.browser_font_size ?? 0}
                        oninput={(event) => {
                          const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                          if (template) template.browser_font_size = Number(event.currentTarget.value) || 0;
                        }}
                        class="field-input mt-1 block w-full text-xs"
                      />
                    </label>
                  </div>
                </details>

                <!-- Answer spec: the control list and role names come from the server registry. -->
                <div class="rounded-md border border-border p-3" data-testid="notetype-answer">
                  <h3 class="text-xs font-semibold text-foreground">{$t('notetype.answer.title')}</h3>
                  <p class="mt-0.5 text-xs text-muted-foreground">{$t('notetype.answer.hint')}</p>
                  <div class="mt-3 max-w-sm">
                    <span class="text-xs text-muted-foreground">{$t('notetype.answer.control')}</span>
                    <Select
                      class="mt-1"
                      testId="notetype-answer-control"
                      value={answerControlValue(selectedTemplate)}
                      onValueChange={(value) => {
                        const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                        if (template) setAnswerControl(template, value);
                      }}
                      options={controlOptions}
                      ariaLabel={$t('notetype.answer.control')}
                    />
                  </div>
                  {#if rolesFor(selectedTemplate).length > 0}
                    <div class="mt-3 grid gap-3 sm:grid-cols-2">
                      {#each rolesFor(selectedTemplate) as role (role.name)}
                        <div>
                          <span class="text-xs text-muted-foreground">
                            {$t('notetype.answer.role', { role: role.name })}
                            {#if role.required}<span class="text-destructive-foreground">*</span>{/if}
                          </span>
                          <Select
                            class="mt-1"
                            testId={`notetype-answer-binding-${role.name}`}
                            value={bindingValue(selectedTemplate, role.name)}
                            allowDeselect={!role.required}
                            onValueChange={(value) => {
                              const template = draft!.config.templates.find((entry) => entry.id === selectedTemplateId);
                              if (template) setBinding(template, role.name, value);
                            }}
                            options={draft.config.fields.map((field) => ({ value: field.id, label: field.name }))}
                            placeholder={$t('notetype.answer.unbound')}
                            ariaLabel={$t('notetype.answer.role', { role: role.name })}
                          />
                        </div>
                      {/each}
                    </div>
                  {/if}
                </div>
              </div>
            {/if}
          </section>

          <!-- Styling -->
          <section class="rounded-lg border border-border p-4" data-testid="notetype-css">
            <h2 class="text-sm font-semibold text-foreground">{$t('notetype.css.title')}</h2>
            <p class="mt-0.5 text-xs text-muted-foreground">{$t('notetype.css.hint')}</p>
            <textarea
              rows="8"
              value={draft.config.css}
              oninput={(event) => (draft!.config.css = event.currentTarget.value)}
              data-testid="notetype-css-input"
              class="field-input mt-2 block w-full font-mono text-xs"
            ></textarea>
          </section>

          <!-- Preview: the unsaved template and CSS rendered by the server, shown only inside the sandboxed frame. -->
          <section class="rounded-lg border border-border p-4" data-testid="notetype-preview">
            <h2 class="text-sm font-semibold text-foreground">{$t('notetype.preview.title')}</h2>
            <p class="mt-0.5 text-xs text-muted-foreground">{$t('notetype.preview.hint')}</p>
            {#if previewError}
              <p role="alert" data-testid="notetype-preview-error" class="mt-2 text-sm text-destructive-foreground">
                {$t(previewError)}
              </p>
            {/if}
            {#if selectedTemplate}
              <div class="mt-3 grid gap-4 xl:grid-cols-2">
                <div data-testid="notetype-preview-front" class="space-y-2 rounded-md border border-border p-3">
                  <h3 class="text-xs font-medium text-muted-foreground">{$t('notetype.preview.front')}</h3>
                  <CardFrame
                    html={preview?.front.html ?? ''}
                    css={previewCSS}
                    bodyClass={preview?.front.body_class ?? 'card'}
                    side="question"
                    mediaBase={preview?.front.media_base ?? ''}
                    title={$t('notetype.preview.front')}
                  />
                </div>
                <div data-testid="notetype-preview-back" class="space-y-2 rounded-md border border-border p-3">
                  <h3 class="text-xs font-medium text-muted-foreground">{$t('notetype.preview.back')}</h3>
                  <CardFrame
                    html={preview?.back.html ?? ''}
                    css={previewCSS}
                    bodyClass={preview?.back.body_class ?? 'card'}
                    side="answer"
                    mediaBase={preview?.back.media_base ?? ''}
                    title={$t('notetype.preview.back')}
                  />
                </div>
              </div>
            {:else}
              <p class="mt-2 text-sm text-muted-foreground" data-testid="notetype-preview-empty">{$t('notetype.preview.unavailable')}</p>
            {/if}
          </section>
        </section>
      {/if}
    </div>
  {/if}

  <!-- Deck-level empty-card cleanup: list, dry run, confirmed removal. -->
  <Dialog
    open={emptyOpen}
    onOpenChange={(open) => { if (!open) emptyOpen = false; }}
    title={$t('notetype.empty.cleanup.title')}
    description={$t('notetype.empty.cleanup.description')}
    size="lg"
    testId="notetype-empty-dialog"
  >
    {#if emptyError}
      <p role="alert" data-testid="notetype-empty-error" class="mb-3 text-sm text-destructive-foreground">{$t(emptyError)}</p>
    {/if}
    {#if emptyLoading}
      <p data-testid="notetype-empty-loading" class="text-sm text-muted-foreground">{$t('common.loading')}</p>
    {:else if emptyCards.length > 0}
      <p data-testid="notetype-empty-count" class="text-sm font-medium text-foreground">
        {$t('notetype.empty.cleanup.count', { count: emptyCards.length })}
      </p>
      <ul data-testid="notetype-empty-list" class="mt-2 max-h-64 space-y-1 overflow-y-auto text-sm text-muted-foreground">
        {#each emptyCards as card (card.id)}
          <li data-testid={`notetype-empty-card-${card.id}`} class="truncate">
            {$t('notetype.empty.cleanup.entry', {
              note_type: noteTypeName(card.note_type_id),
              template: templateName(card.note_type_id, card.template),
            })}
          </li>
        {/each}
      </ul>
    {:else if !emptyImpact}
      <p data-testid="notetype-empty-none" class="text-sm text-muted-foreground">{$t('notetype.empty.cleanup.none')}</p>
    {/if}
    {#if emptyImpact}
      <dl data-testid="notetype-empty-impact" class="mt-3 space-y-1 border-t border-border pt-3 text-sm">
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('notetype.empty.cleanup.cards_removed')}</dt>
          <dd data-testid="notetype-empty-removed" class="font-semibold tabular-nums text-foreground">{emptyImpact.cards_removed}</dd>
        </div>
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('notetype.empty.cleanup.reviews_kept')}</dt>
          <dd data-testid="notetype-empty-reviews" class="font-semibold tabular-nums text-foreground">{emptyImpact.reviews_on_removed_cards}</dd>
        </div>
      </dl>
    {/if}
    <div class="mt-5 flex justify-end gap-2">
      <Button variant="outline" onclick={() => (emptyOpen = false)} testId="notetype-empty-cancel">
        <span>{$t('notetype.change.cancel')}</span>
      </Button>
      <Button
        variant="outline"
        onclick={() => void previewEmptyCards()}
        disabled={emptyCards.length === 0 || emptyLoading}
        testId="notetype-empty-preview"
      >
        <span>{$t('notetype.empty.cleanup.preview')}</span>
      </Button>
      <Button
        variant="danger"
        onclick={() => void confirmEmptyCards()}
        disabled={!emptyImpact || emptyImpact.cards_removed === 0}
        loading={emptyRemoving}
        testId="notetype-empty-confirm"
      >
        <span>{$t('notetype.empty.cleanup.confirm')}</span>
      </Button>
    </div>
  </Dialog>

  <!-- Change note type: target of the same deck, field and template mapping, dry run before the write. -->
  <Dialog
    open={changeOpen}
    onOpenChange={(open) => { if (!open) changeOpen = false; }}
    title={$t('notetype.change.title')}
    description={$t('notetype.change.description')}
    size="lg"
    testId="notetype-change-dialog"
  >
    {#if changeError}
      <p role="alert" data-testid="notetype-change-error" class="mb-3 text-sm text-destructive-foreground">{$t(changeError)}</p>
    {/if}
    {#if changeTargets.length === 0}
      <p data-testid="notetype-change-no-targets" class="text-sm text-muted-foreground">{$t('notetype.change.no_targets')}</p>
    {:else}
      <div>
        <span class="text-sm font-medium text-foreground">{$t('notetype.change.target')}</span>
        <Select
          class="mt-1"
          testId="notetype-change-target"
          value={changeTargetId}
          onValueChange={chooseChangeTarget}
          options={changeTargetOptions}
          ariaLabel={$t('notetype.change.target')}
        />
      </div>

      {#if changeTarget}
        <section data-testid="notetype-change-fields" class="mt-4">
          <h3 class="text-sm font-semibold text-foreground">{$t('notetype.change.fields')}</h3>
          <p class="mt-0.5 text-xs text-muted-foreground">{$t('notetype.change.field_hint')}</p>
          <div class="mt-2 space-y-2">
            {#each changeTargetFields as field (field.id)}
              <div class="grid items-center gap-2 sm:grid-cols-[10rem_minmax(0,1fr)]">
                <span class="text-sm text-foreground">{field.name}</span>
                <Select
                  testId={`notetype-change-field-${field.id}`}
                  value={changeFieldMap[field.id] ?? ''}
                  onValueChange={(sourceId) => setChangeField(field.id, sourceId)}
                  options={sourceFieldOptions(field.id)}
                  ariaLabel={$t('notetype.change.field_for', { field: field.name })}
                />
              </div>
            {/each}
          </div>
        </section>

        {#if changeTemplateRows.length > 0}
          <section data-testid="notetype-change-templates" class="mt-4">
            <h3 class="text-sm font-semibold text-foreground">{$t('notetype.change.templates')}</h3>
            <p class="mt-0.5 text-xs text-muted-foreground">{$t('notetype.change.template_hint')}</p>
            <div class="mt-2 space-y-2">
              {#each changeTemplateRows as template (template.id)}
                <div class="grid items-center gap-2 sm:grid-cols-[10rem_minmax(0,1fr)]">
                  <span class="text-sm text-foreground">{template.name}</span>
                  <Select
                    testId={`notetype-change-template-${template.id}`}
                    value={changeTemplateMap[template.id] ?? ''}
                    onValueChange={(targetId) => setChangeTemplate(template.id, targetId)}
                    options={targetTemplateOptions(template.id)}
                    ariaLabel={$t('notetype.change.template_for', { template: template.name })}
                  />
                </div>
              {/each}
            </div>
          </section>
        {/if}

        {#if changeImpact}
          <dl data-testid="notetype-change-impact" class="mt-4 space-y-1 border-t border-border pt-3 text-sm">
            <div class="flex items-center justify-between gap-4">
              <dt class="text-muted-foreground">{$t('notetype.change.notes_moved')}</dt>
              <dd data-testid="notetype-change-notes" class="font-semibold tabular-nums text-foreground">{changeImpact.notes_moved}</dd>
            </div>
            <div class="flex items-center justify-between gap-4">
              <dt class="text-muted-foreground">{$t('notetype.change.cards_moved')}</dt>
              <dd data-testid="notetype-change-moved" class="font-semibold tabular-nums text-foreground">{changeImpact.cards_moved}</dd>
            </div>
            <div class="flex items-center justify-between gap-4">
              <dt class="text-muted-foreground">{$t('notetype.change.cards_removed')}</dt>
              <dd data-testid="notetype-change-removed" class="font-semibold tabular-nums text-foreground">{changeImpact.cards_removed}</dd>
            </div>
            <div class="flex items-center justify-between gap-4">
              <dt class="text-muted-foreground">{$t('notetype.change.reviews')}</dt>
              <dd data-testid="notetype-change-reviews" class="font-semibold tabular-nums text-foreground">{changeImpact.reviews_on_removed_cards}</dd>
            </div>
          </dl>
        {/if}
      {/if}
    {/if}
    <div class="mt-5 flex justify-end gap-2">
      <Button variant="outline" onclick={() => (changeOpen = false)} testId="notetype-change-cancel">
        <span>{$t('notetype.change.cancel')}</span>
      </Button>
      <Button
        variant="outline"
        onclick={() => void previewChange()}
        disabled={!changeTarget || changeRunning}
        testId="notetype-change-preview"
      >
        <span>{$t('notetype.change.preview')}</span>
      </Button>
      <Button
        onclick={() => void requestChange()}
        disabled={!changeTarget}
        loading={changeRunning}
        testId="notetype-change-submit"
      >
        <span>{$t('notetype.change.confirm')}</span>
      </Button>
    </div>
  </Dialog>

  <!-- Extra confirmation: a change that removes cards must be confirmed once more after the dry run. -->
  <Dialog
    open={changeConfirmOpen}
    onOpenChange={(open) => { if (!open) changeConfirmOpen = false; }}
    title={$t('notetype.change.removal_title')}
    description={$t('notetype.change.removal_description', { cards: changeImpact?.cards_removed ?? 0 })}
    testId="notetype-change-confirm-dialog"
  >
    <div class="mt-4 flex justify-end gap-2">
      <Button variant="outline" onclick={() => (changeConfirmOpen = false)} testId="notetype-change-confirm-cancel">
        <span>{$t('notetype.change.cancel')}</span>
      </Button>
      <Button variant="danger" onclick={() => void commitChange(true)} loading={changeRunning} testId="notetype-change-confirm-removal">
        <span>{$t('notetype.change.removal_confirm')}</span>
      </Button>
    </div>
  </Dialog>

  <!-- Create: a stock definition or a copy of an existing type. -->
  <Dialog
    open={createOpen}
    onOpenChange={(open) => { if (!open) createOpen = false; }}
    title={$t('notetype.create.title')}
    description={$t('notetype.create.hint')}
    size="lg"
    testId="notetype-create-dialog"
  >
    <div class="mb-4 flex flex-wrap gap-2">
      <Button variant={createMode === 'stock' ? 'primary' : 'outline'} size="sm" onclick={() => chooseCreateMode('stock')} testId="notetype-create-mode-stock">
        <span>{$t('notetype.create.mode.stock')}</span>
      </Button>
      <Button variant={createMode === 'copy' ? 'primary' : 'outline'} size="sm" onclick={() => chooseCreateMode('copy')} testId="notetype-create-mode-copy">
        <span>{$t('notetype.create.mode.copy')}</span>
      </Button>
    </div>
    {#if createError}
      <p role="alert" data-testid="notetype-create-error" class="mb-3 text-sm text-destructive-foreground">{$t(createError)}</p>
    {/if}
    <form onsubmit={(event) => { event.preventDefault(); void submitCreate(); }} class="space-y-4" data-testid="notetype-create-form">
      {#if createMode === 'stock'}
        <div>
          <span class="text-sm font-medium text-foreground">{$t('notetype.create.stock')}</span>
          <Select
            class="mt-1"
            testId="notetype-create-stock"
            value={createStockId}
            onValueChange={chooseStock}
            options={stockOptions}
            ariaLabel={$t('notetype.create.stock')}
          />
          <p class="mt-1 text-xs text-muted-foreground">{$t('notetype.create.stock_hint')}</p>
        </div>
      {:else}
        <div>
          <span class="text-sm font-medium text-foreground">{$t('notetype.create.copy_source')}</span>
          <Select
            class="mt-1"
            testId="notetype-create-source"
            value={createSourceId}
            onValueChange={chooseCopySource}
            options={copyOptions}
            ariaLabel={$t('notetype.create.copy_source')}
          />
        </div>
      {/if}
      <label class="block">
        <span class="text-sm font-medium text-foreground">{$t('notetype.create.name')}</span>
        <input type="text" bind:value={createName} required data-testid="notetype-create-name" class="field-input mt-1 block w-full text-sm" />
      </label>
      <div class="flex justify-end gap-2">
        <Button variant="outline" onclick={() => (createOpen = false)} testId="notetype-create-cancel">
          <span>{$t('notetype.impact.cancel')}</span>
        </Button>
        <Button type="submit" loading={creating} testId="notetype-create-submit">
          <span>{$t('notetype.create.submit')}</span>
        </Button>
      </div>
    </form>
  </Dialog>

  <!-- Card impact: the counts are the server's dry-run result and gate the real write. -->
  <Dialog
    open={impactOpen}
    onOpenChange={(open) => { if (!open) impactOpen = false; }}
    title={$t('notetype.impact.title')}
    description={$t('notetype.impact.description')}
    testId="notetype-impact-dialog"
  >
    {#if impact}
      <dl data-testid="notetype-impact" class="space-y-2 text-sm">
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('notetype.impact.added')}</dt>
          <dd class="font-semibold tabular-nums text-foreground" data-testid="notetype-impact-added">{impact.cards_added}</dd>
        </div>
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('notetype.impact.removed')}</dt>
          <dd class="font-semibold tabular-nums text-foreground" data-testid="notetype-impact-removed">{impact.cards_removed}</dd>
        </div>
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('notetype.impact.reviews')}</dt>
          <dd class="font-semibold tabular-nums text-foreground" data-testid="notetype-impact-reviews">{impact.reviews_on_removed_cards}</dd>
        </div>
      </dl>
      {#if impact.cards_removed > 0}
        <p class="mt-3 text-xs text-destructive-foreground" data-testid="notetype-impact-warning">{$t('notetype.impact.removal_warning')}</p>
      {/if}
    {/if}
    <div class="mt-5 flex justify-end gap-2">
      <Button variant="outline" onclick={() => (impactOpen = false)} testId="notetype-impact-cancel">
        <span>{$t('notetype.impact.cancel')}</span>
      </Button>
      <Button onclick={() => void confirmImpact()} loading={saving} testId="notetype-impact-confirm">
        <span>{$t('notetype.impact.confirm')}</span>
      </Button>
    </div>
  </Dialog>

  <!-- Delete confirmation. -->
  <Dialog
    open={deleteOpen}
    onOpenChange={(open) => { if (!open) deleteOpen = false; }}
    title={$t('notetype.type.delete.title')}
    description={$t('notetype.type.delete.confirm', { name: draft?.name ?? '' })}
    testId="notetype-delete-dialog"
  >
    {#if deleteError}
      <p role="alert" data-testid="notetype-delete-error" class="text-sm text-destructive-foreground">{$t(deleteError)}</p>
    {/if}
    <div class="mt-4 flex justify-end gap-2">
      <Button variant="outline" onclick={() => (deleteOpen = false)} testId="notetype-delete-cancel">
        <span>{$t('notetype.impact.cancel')}</span>
      </Button>
      <Button variant="danger" onclick={() => void confirmDelete()} loading={deleting} testId="notetype-delete-confirm">
        <span>{$t('notetype.type.delete')}</span>
      </Button>
    </div>
  </Dialog>
</Page>

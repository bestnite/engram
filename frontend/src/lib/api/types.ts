/**
 * Deck data structure (aligned with the Go backend's internal/api/decks.go:DeckResponse)
 */
export interface Deck {
  id: string;
  name: string;
  description: string;
  /**
   * Whether `description` is Markdown source instead of HTML (Go: DeckResponse.DescriptionMarkdown).
   * A response from an older server omits it; the default is HTML.
   */
  description_markdown?: boolean;
  /**
   * The description after the server rendered it: Markdown converted when `description_markdown` is set, the stored HTML
   * passed through otherwise (Go: DeckResponse.DescriptionHTML). It is unsanitised content: show it only inside the
   * sandboxed frame, never in the parent page.
   */
  description_html?: string;
  /**
   * The description reduced to one line of plain text, for the lists that print it as a string
   * (Go: DeckResponse.DescriptionText). A response from an older server omits it.
   */
  description_text?: string;
  new_per_day: number;
  reviews_per_day: number;
  preset_id: string;
  /**
   * Deck-wide autoplay setting (Go: DeckResponse.Autoplay): whether a card face starts its audio tags without a click.
   * A response from an older server omits it; the deck default is on.
   */
  autoplay?: boolean;
  created_at: string;
  /**
   * The caller's explicit relation to this deck (Go: DeckResponse.Role).
   * 'owner' means own deck; 'editor'/'reader' mean explicitly granted. A deck in the list is necessarily one of these two groups.
   * The list card uses this to decide whether to show "delete" (owner) or "leave shared deck" (editor/reader).
   */
  role: 'owner' | 'editor' | 'reader';
  /**
   * Whether the caller paused this deck for themselves (Go: DeckResponse.Paused). A paused deck stays out of the default review
   * queue, the home deck list and the sidebar, but can still be opened and reviewed by name. Absent means active.
   */
  paused?: boolean;
}

/** PUT /api/v1/decks/:id/paused response (Go: DeckPauseStatus). */
export interface DeckPauseStatus {
  deck_id: string;
  paused: boolean;
}

/**
 * GET /api/v1/decks response body structure
 */
export interface DecksResponse {
  decks: Deck[];
}

/**
 * A pending deck sharing invite (consent-based; Go: internal/web/share_invites.go).
 *
 * From the recipient's view read deck_name + inviter_name (who invited me to which deck); from the owner's view read username
 * (whom I invited). One structure, two readings; use the fields matching the view.
 */
export interface DeckShareInvite {
  deck_id: string;
  deck_name: string;
  user_id: string;
  username: string;
  role: 'reader' | 'editor' | 'owner';
  invited_by: string;
  inviter_name: string;
  created_at: string;
  expires_at: string;
}

/** Accept policy: who may share decks with me. NULL/omitted means anyone. */
export type ShareAcceptPolicy = 'anyone' | 'whitelist' | 'nobody';

/** One row in the allowlist. It carries the username because the UI must show "who"; a bare id is unreadable. */
export interface ShareAllowRow {
  user_id: string;
  username: string;
}

/** GET /api/v1/sharing/invites (my pending invites + my accept policy). */
export interface ShareInvitesResponse {
  invites: DeckShareInvite[];
  policy: ShareAcceptPolicy;
  allow_list: ShareAllowRow[];
}

/** Response of GET/PUT /api/v1/settings/share-policy. */
export interface SharePolicyResponse {
  policy: ShareAcceptPolicy;
  allow_list: ShareAllowRow[];
}

/**
 * One deck's counts in GET /api/v1/decks/queue-counts.
 * new_count / review_count are what can be studied today, after the daily limits.
 * studied_count / remaining_count are the caller's progress through the whole deck; remaining excludes suspended new cards.
 */
export interface DeckQueueCount {
  deck_id: string;
  new_count: number;
  review_count: number;
  studied_count: number;
  remaining_count: number;
}

/** GET /api/v1/decks/queue-counts response; counts are produced by the shared queue builder. */
export interface DeckQueueCountsResponse {
  decks: DeckQueueCount[];
}

/**
 * GET/PATCH /api/v1/decks/:id/settings (Go: internal/web/deck_settings_api.go).
 * new_per_day / reviews_per_day of 0 mean unlimited; new_unlimited / review_unlimited express
 * "unlimited" explicitly, because 0 and "0 cards left today" are numerically identical. Today's used/remaining come from the same source as the review queue.
 */
export interface DeckSettings {
  deck_id: string;
  deck_name: string;
  /** Deck description (used by the owner to prefill the "deck info" form; shared members cannot change it). */
  deck_description: string;
  /** The caller's role on the deck: settings belong to the caller, so the owner and each shared member have their own copy. */
  role: 'owner' | 'editor' | 'reader';
  preset_id: string;
  /** Deck-wide autoplay setting; only the owner can change it (Go: deckSettingsResponse.Autoplay). */
  autoplay?: boolean;
  /** Whether the deck description is Markdown rather than HTML; only the owner can change it (Go: deckSettingsResponse.DescriptionMarkdown). */
  description_markdown?: boolean;
  new_per_day: number;
  reviews_per_day: number;
  /** New-card quota added by today's "one more round"; new_left already counts it, new_per_day does not. */
  new_extra: number;
  new_used: number;
  review_used: number;
  new_left: number;
  review_left: number;
  new_unlimited: boolean;
  review_unlimited: boolean;
}

/**
 * PATCH /api/v1/decks/:id/settings request body; both quota fields are required, and 0 is valid (unlimited).
 * preset_id is optional: omitting it leaves the preset untouched (older quota-only callers keep their behavior).
 */
export interface UpdateDeckSettingsRequest {
  new_per_day: number;
  reviews_per_day: number;
  preset_id?: string;
  /** Optional deck-wide setting; the owner only. Omitting it leaves the value untouched. */
  autoplay?: boolean;
  /** Optional deck-wide setting that switches the description between HTML and Markdown; the owner only. */
  description_markdown?: boolean;
}

/** POST /api/v1/decks request and response body. An empty preset_id uses the server's default preset. */
export interface CreateDeckRequest {
  name: string;
  description: string;
  preset_id: string;
}

/**
 * Note data structure (aligned with the Go backend's internal/api/notes.go:NoteJSON)
 */
export interface Note {
  id: string;
  deck_id: string;
  /** Public id of the note's note type, or null for a note written before note types existed. */
  note_type?: string | null;
  fields: Record<string, unknown>;
  tags: string[];
  created_at: string;
  updated_at: string;
  external_ref: string;
  /** Whether the caller suspended the cards under this note (suspension applies to you only). */
  suspended: boolean;
}

/**
 * GET /api/v1/decks/:id/notes response body structure
 * internal/api/notes.go:listNotes
 */
export interface NotesResponse {
  notes: Note[];
  total: number;
  page: number;
  per_page: number;
}

/** PATCH /api/v1/notes/:id request body. Fields are replaced as a complete object. */
export interface UpdateNoteRequest {
  /** Public id of the note type the fields belong to. */
  note_type: string;
  fields: Record<string, unknown>;
  tags?: string[];
}

/**
 * One audio or text-to-speech tag a card face references as `[anki:play:<side>:<n>]` (Go: internal/notetype.AVTag).
 * side is 'q' for the question and 'a' for the answer; the answer face carries the question's tags as well, because
 * {{FrontSide}} keeps the question markers in its HTML.
 */
export interface AVTag {
  kind: 'sound' | 'tts';
  side: 'q' | 'a';
  name?: string;
  lang?: string;
  voices?: string[];
  speed: number;
  other_args?: string[];
  field_text?: string;
  cloze_blank?: string | null;
}

/**
 * One rendered side of a card (Go: internal/web/review.go:CardFace).
 *
 * html is the face HTML the server rendered, and the server does not sanitise it: the sandboxed frame and its CSP are the
 * only boundary around it. css is the note type's own stylesheet, which the frame appends after its engram face typography
 * so a note type rule wins; body_class is the class list the frame writes on its `<body>`; media_base is the `<base href>`
 * the face's relative media names resolve against. av_tags describes every play marker in html, and autoplay is the deck's
 * setting for starting those tags without a click.
 */
export interface CardFace {
  html: string;
  css: string;
  body_class: string;
  /** The server's own side name, `front` or `back`. The frame's side prop is `question` or `answer` and is not read from here. */
  side: string;
  media_base: string;
  av_tags?: AVTag[];
  autoplay?: boolean;
}

/** One preview card: the two faces the review frame would show. */
export interface NotePreviewCard {
  front: CardFace;
  back: CardFace;
}

/** POST /api/v1/decks/:id/notes/preview response; the cards carry the same face objects the review frame shows. */
export interface NotePreviewResponse {
  cards: NotePreviewCard[];
}

/** One note field of a note type (aligned with Go internal/notetype.Field). */
export interface NoteTypeField {
  /** Opaque stable id; a note stores its values by this id. */
  id: string;
  /** Display name; the editor uses it as the label and the save payload key. */
  name: string;
  sticky: boolean;
  rtl: boolean;
  /** CSS font family of the field's editing surface; empty means the editor default. */
  font: string;
  /** Font size in px; 0 means the editor default. */
  font_size: number;
  description: string;
  /** The field holds plain text: no formatting toolbar and no rich formatting. */
  plain_text: boolean;
  /** Start folded in the editor; the writer unfolds it. */
  collapsed: boolean;
  exclude_from_search?: boolean;
  tag?: number | null;
  prevent_deletion?: boolean;
}

/**
 * The answer control a template binds (aligned with Go internal/notetype.AnswerSpec).
 * Bindings maps a role name declared by GET /api/v1/answer-controls to a field id.
 */
export interface NoteTypeAnswerSpec {
  control: string;
  bindings: Record<string, string>;
}

/** One card template of a note type (aligned with Go internal/notetype.Template). */
export interface NoteTypeTemplate {
  id: string;
  name: string;
  qfmt: string;
  afmt: string;
  bqfmt?: string;
  bafmt?: string;
  browser_font?: string;
  browser_font_size?: number;
  /** The answer spec, or null when the template has no machine-graded control. */
  answer?: NoteTypeAnswerSpec | null;
}

/**
 * The decoded config of a note type (aligned with Go internal/notetype.Config).
 * Fields are in display order; templates decide which cards a note produces.
 */
export interface NoteTypeConfig {
  kind: 'normal' | 'cloze';
  fields: NoteTypeField[];
  templates: NoteTypeTemplate[];
  css: string;
  sort_field_id?: string;
  latex_pre?: string;
  latex_post?: string;
  latex_svg?: boolean;
  original_stock_kind?: string | null;
}

/** One note type of a deck (Go internal/api.NoteTypeJSON); id is the public id the payloads speak. */
export interface NoteType {
  id: string;
  deck_id: string;
  name: string;
  version: number;
  config: NoteTypeConfig;
  created_at: string;
  updated_at: string;
}

/** GET /api/v1/decks/:id/note-types response; the server orders the types by name then id. */
export interface NoteTypesResponse {
  note_types: NoteType[];
}

/**
 * POST /api/v1/decks/:id/note-types/preview response.
 *
 * The front and back faces are the server's own rendering of the unsaved config, so they are the shape a review
 * receives. The HTML only enters the sandboxed card frame, never the parent page.
 */
export interface NoteTypePreviewResponse {
  front: CardFace;
  back: CardFace;
}

/**
 * One stock definition a note type can be created from (GET /api/v1/note-type-stocks).
 * The id is "source:kind" because Anki's and engram's stock sets share kind values;
 * label_key is the translation catalog entry for the display name.
 */
export interface NoteTypeStock {
  id: string;
  source: 'anki' | 'engram';
  name: string;
  label_key: string;
}

/** GET /api/v1/note-type-stocks response; Anki's six definitions followed by engram's ten. */
export interface NoteTypeStocksResponse {
  stocks: NoteTypeStock[];
}

/** One role an answer control reads (GET /api/v1/answer-controls). */
export interface AnswerControlRole {
  name: string;
  kind: 'text' | 'lines' | 'bool' | 'number';
  required: boolean;
}

/** One registered answer control and the roles it declares. */
export interface AnswerControl {
  name: string;
  roles: AnswerControlRole[];
}

/** GET /api/v1/answer-controls response; controls are in lexicographic order by name. */
export interface AnswerControlsResponse {
  controls: AnswerControl[];
}

/** POST /api/v1/decks/:id/note-types body: a stock id, or a config copied from an existing type. */
export interface CreateNoteTypeRequest {
  name: string;
  config?: NoteTypeConfig;
  stock_id?: string;
}

/**
 * PATCH /api/v1/decks/:id/note-types/:type_id body.
 * version is the version the caller read. dry_run counts the card impact without writing;
 * confirm_card_removal is required when the update would remove cards.
 */
export interface UpdateNoteTypeRequest {
  name: string;
  config: NoteTypeConfig;
  version: number;
  dry_run?: boolean;
  confirm_card_removal?: boolean;
}

/** The card impact of a note type update, reported by a dry run and by the real update alike. */
export interface NoteTypeImpact {
  cards_added: number;
  cards_removed: number;
  reviews_on_removed_cards: number;
}

/** PATCH response: the updated type plus the impact counts. */
export interface UpdateNoteTypeResponse extends NoteType, NoteTypeImpact {
  dry_run: boolean;
}

/**
 * The effect of deleting a note type whose notes are all soft-deleted (Go store.NoteTypePurgeImpact).
 * A DELETE without confirm_purge answers 409 note_type_purge_unconfirmed with these counts; the
 * confirmed request hard-deletes the notes and their cards, while the review rows stay.
 */
export interface NoteTypePurgeImpact {
  deleted_notes: number;
  cards: number;
  reviews: number;
}

/** One card the empty-card cleanup found (Go internal/api.EmptyCardJSON). */
export interface EmptyCard {
  /** Public card id. */
  id: string;
  /** Public id of the card's note. */
  note_id: string;
  /** Public id of the card's note type. */
  note_type_id: string;
  /** Template id the card currently holds. */
  template: string;
  ordinal: number;
}

/** GET /api/v1/decks/:id/empty-cards response; count repeats len(cards) for callers that only show the number. */
export interface EmptyCardListResponse {
  cards: EmptyCard[];
  count: number;
}

/** POST /api/v1/decks/:id/empty-cards body. */
export interface CleanupEmptyCardsRequest {
  /** Report the counts without writing. */
  dry_run?: boolean;
  /** Allow the cleanup to soft-delete the cards it found. */
  confirm_card_removal?: boolean;
}

/** The effect of one empty-card cleanup; reviews_on_removed_cards counts the log rows the cards keep. */
export interface EmptyCardCleanupResult {
  dry_run: boolean;
  cards_removed: number;
  reviews_on_removed_cards: number;
}

/**
 * POST /api/v1/decks/:id/note-types/:type_id/change body.
 *
 * field_mapping maps a source field id to a target field id; template_mapping maps a source template id to a target
 * template id. An unmapped target field receives an empty value, and a card of an unmapped source template is removed,
 * which needs confirm_card_removal.
 */
export interface ChangeNoteTypeRequest {
  target_note_type: string;
  field_mapping: Record<string, string>;
  template_mapping: Record<string, string>;
  dry_run?: boolean;
  confirm_card_removal?: boolean;
}

/** The effect of one change-note-type operation, reported by a dry run and the real change alike. */
export interface ChangeNoteTypeResult {
  dry_run: boolean;
  notes_moved: number;
  cards_moved: number;
  cards_removed: number;
  reviews_on_removed_cards: number;
}

/**
 * One item of GET /api/v1/media (aligned with Go internal/api.MediaItem).
 * url is built from sha256 as /media/<sha256> and can be used directly in <img src> and for inserting Markdown references.
 */
export interface MediaItem {
  sha256: string;
  mime: string;
  bytes: number;
  url: string;
  width?: number | null;
  height?: number | null;
  created_at: string;
}

/** One page of GET /api/v1/media; an empty next_cursor means the end is reached. */
export interface MediaListResponse {
  items: MediaItem[];
  next_cursor: string;
}

/**
 * Editor upload success response (aligned with the Go backend's internal/web/media.go:storeUpload).
 * The public identifier is sha256, and url is built directly from the hash as /media/<sha256>.
 */
export interface MediaUploadResult {
  sha256: string;
  mime: string;
  bytes: number;
  url: string;
}

/**
 * GET /api/v1/decks/:id/notes query parameters
 * internal/store/note.go:NoteListOptions
 */
export interface NoteListParams {
  page?: number;
  per_page?: number;
  q?: string;
  tag?: string;
  status?: string;
}

/**
 * Request body of the bulk write contract.
 *
 * Every note names its note type public id; fields is a Record whose keys are the selected note type's field names.
 */
export interface CreateNotesRequest {
  notes: Array<{
    note_type: string;
    fields: Record<string, unknown>;
    tags: string[];
  }>;
  dry_run?: boolean;
  on_conflict?: 'skip' | 'update' | 'fail';
}

export interface CreateNotesResponse {
  created: number;
  updated: number;
  skipped: number;
  errors: Array<{ index: number; reason: string }>;
  dry_run: boolean;
}

/**
 * POST /api/v1/notes/bulk request body (the authoritative schema is schema/note-bulk.schema.json).
 * The set of action values matches the Go-side bulkAction* constants item by item; note_ids is deduplicated to 1..500;
 * tags is needed only for tag actions (1..20), and delete must not carry tags.
 */
export interface BulkNotesRequest {
  action: 'delete' | 'add_tags' | 'remove_tags' | 'set_tags';
  note_ids: string[];
  tags?: string[];
  dry_run?: boolean;
}

/** A note rejected row by row in a bulk action; code is not_found / insufficient_role. */
export interface BulkNotesSkipped {
  note_id: string;
  code: string;
}

/**
 * POST /api/v1/notes/bulk response body. affected counts only rows whose stored state actually changed,
 * so resubmitting the same request gives affected=0 the second time; skipped gives the rejection reason per row, and the batch is never rolled back.
 */
export interface BulkNotesResponse {
  dry_run: boolean;
  affected: number;
  skipped: BulkNotesSkipped[];
}

/**
 * POST /api/v1/decks/bulk request body. Only delete is accepted today; deck_ids is deduplicated to 1..100 entries,
 * and dry_run counts the decks that would be deleted without writing.
 */
export interface BulkDecksRequest {
  action: 'delete';
  deck_ids: string[];
  dry_run?: boolean;
}

/** A deck rejected row by row in a bulk deck action; code is not_found (unknown id) / forbidden (the caller does not own it). */
export interface BulkDecksSkipped {
  deck_id: string;
  code: string;
}

/**
 * POST /api/v1/decks/bulk response body. affected counts the decks actually deleted;
 * skipped gives the reason per rejected deck, and one rejected row never rolls back the batch.
 */
export interface BulkDecksResponse {
  dry_run: boolean;
  affected: number;
  skipped: BulkDecksSkipped[];
}

/** POST /api/v1/decks/import report. */
export interface PackageImportReport {
  target: string;
  dry_run: boolean;
  deck_id?: string;
  notes_created: number;
  notes_updated: number;
  notes_skipped: number;
  cards_created: number;
  /** Cards soft-deleted by a structural .apkg re-import (their progress rows stay). */
  cards_removed?: number;
  media_new: number;
  media_missing: number;
  progress_applied: number;
  progress_skipped: number;
  progress_discarded: boolean;
  weights_applied: boolean;
  weights_discarded: boolean;
  match_rule?: string;
  /** Anki .apkg report: media references with no bytes in the package. */
  missing_media?: string[];
  /** Anki .apkg report: video files a browser cannot play (only mp4 and webm can). */
  unplayable_video?: string[];
  /** Anki .apkg report: absolute script and stylesheet references. */
  external_assets?: string[];
  /** Anki .apkg report: LaTeX images the package does not carry. */
  latex_missing_images?: string[];
  errors: Array<{ entry: string; reason: string }>;
}

/**
 * Study stats summary (aligned with the Go backend's internal/api/service.go:StatsSummary)
 */
export interface StatsSummary {
  decks: number;
  due: number;
  reviews_today: number;
  reviews_total: number;
  /** True retention: among due reviews, the share not graded Again (same rule as the stats page). */
  retention: number;
  /** The retention denominator (due reviews only, not equal to reviews_total); display "no data yet" when 0. */
  retention_total: number;
  /** The retention numerator. */
  retention_passed: number;
  notes: number;
  cards: number;
}

/**
 * Detailed stats (aligned with statsDetail in the Go backend's internal/web/stats_api.go)
 * The same aggregate queries as the stats page; fields return raw counts/ratios/milliseconds, and localization and bar widths are the frontend's job.
 */
export interface StatsVolume {
  today: number;
  last_7_days: number;
  last_30_days: number;
}

export interface StatsDue {
  today: number;
  tomorrow: number;
  within_7_days: number;
  within_30_days: number;
  later: number;
  /** Unstudied new cards (not suspended); never part of the dated buckets. */
  new: number;
}

export interface StatsRetentionBucket {
  label: string;
  total: number;
  passed: number;
  rate: number;
}

export interface StatsRetention {
  total: number;
  passed: number;
  rate: number;
  buckets: StatsRetentionBucket[];
}

export interface StatsTimeSpent {
  total_ms: number;
  count: number;
  avg_ms: number;
  median_ms: number;
}

export interface StatsStreak {
  current: number;
  longest: number;
}

export interface StatsCurvePoint {
  day: string;
  new: number;
  review: number;
}

export interface StatsDeck {
  deck_id: string;
  name: string;
  /** Due learning and review cards the queue serves today, the same number as the deck list. */
  review: number;
  /** New cards the queue still introduces today. */
  new: number;
  /** Total review count (including learning steps). */
  reviews: number;
  /** True retention; a denominator retention_total of 0 does not mean 0% but no data. */
  retention: number;
  retention_total: number;
  elapsed_ms: number;
}

export interface StatsTag {
  tag: string;
  reviews: number;
  retention: number;
  retention_total: number;
}

export interface StatsGradeSource {
  source: string;
  count: number;
}

export interface StatsDetail {
  generated_at: string;
  empty: boolean;
  volume: StatsVolume;
  due: StatsDue;
  retention: StatsRetention;
  time_spent: StatsTimeSpent;
  streak: StatsStreak;
  curve: StatsCurvePoint[];
  /** First and last review day of the curve window (inclusive); curve only contains days with reviews, so a continuous date axis needs these two bounds. */
  curve_from: string;
  curve_to: string;
  decks: StatsDeck[];
  tags: StatsTag[];
  grades: StatsGradeSource[];
}

/**
 * Public shape of a due card (aligned with the Go backend's internal/api/service.go:DueCard)
 */
export interface DueCard {
  card_id: string;
  note_id: string;
  deck_id: string;
  state: string;
  due_at: string;
  retrievability: number;
  fields: Record<string, unknown>;
  tags: string[];
  template?: string;
  version: number;
  /**
   * Control name of the card template's answer spec, or null when the template has none. Together with
   * `machine_graded` it decides the review page's flow.
   */
  answer_control: string | null;
  /** Whether the server grades this card's answer, by the same rule the submit path applies. */
  machine_graded: boolean;
  /** The note type's field ids in config order; `fields` is keyed by field id and keeps no order of its own. */
  field_order: string[];
  /**
   * Display order of a choice card's options: display slot -> stored option index. `option_order[b] = s` means screen row `b`
   * shows the option stored at index `s`; it is a permutation of `0..n-1` for choice_single / choice_multi and null for every
   * other template. The card's `fields.options` stay in stored order, and a submitted answer is a stored index (or an array of
   * stored indices for choice_multi).
   */
  option_order?: number[] | null;
}

/**
 * GET /api/v1/review/due response body structure
 */
export interface DueCardsResponse {
  cards: DueCard[];
}

/**
 * GET / POST /api/v1/decks/:id/extra-new response body (aligned with the Go backend's internal/api/extra_new.go:ExtraNewStatus):
 * the caller's new-card quota on the deck for today, plus how much "one more round" can add.
 */
export interface ExtraNewStatus {
  deck_id: string;
  /** Daily new-card limit (study goal); meaningless when new_unlimited is true. */
  new_per_day: number;
  new_unlimited: boolean;
  /** New-card quota already added today, reset to zero the next day. */
  extra_new: number;
  new_used: number;
  /** How many new cards can still be introduced today, including the added amount. */
  new_left: number;
  /** Total new cards in the deck not yet studied that can be introduced right now, regardless of quota (when reviewing by tag, only cards carrying those tags are counted). */
  new_available: number;
  /** Default number of cards to add; 0 means there are no new cards to add. */
  suggested_count: number;
  /** Maximum number of cards allowed in a single add. */
  max_count: number;
}

export interface SubmitSelfReviewRequest {
  card_id: string;
  rating: number;
  /** The answer written before self-grading a short-answer card (answer_control = 'essay'), archived with the self-grade; other kinds do not carry it. */
  answer?: string;
  expected_version: number;
  elapsed_ms?: number;
  deck?: string[];
  tags?: string[];
}

export interface SubmitReviewResult {
  card_id: string;
  state: string;
  due_at: string | null;
  version: number;
  stability: number | null;
  cards: DueCard[];
  remaining: number;
}

/**
 * Raw answer submitted for answer-graded templates.
 * The grade is produced by the server's grader; the client never submits a grade.
 */
export type GradedAnswer = string | number | boolean | number[] | string[];

/**
 * POST /api/v1/review/grade request body.
 * When action is omitted it grades normally; 'reveal' only fetches the correct answer (writes nothing);
 * 'give_up' means giving up after the answer was revealed, recorded as a self-grade of Again.
 */
export interface SubmitGradedReviewRequest {
  card_id: string;
  expected_version?: number;
  elapsed_ms?: number;
  deck?: string[];
  tags?: string[];
  action?: 'reveal' | 'give_up';
  answer?: GradedAnswer;
}

/**
 * Grade feedback: verdict is the decision, answer_html is the rendered correct answer, shown by the frame.
 */
export interface GradedFeedback {
  verdict: 'correct' | 'partial' | 'incorrect';
  score: number;
  rating: number;
  answer_html: string;
  /** The answer face the frame renders; carries av_tags and autoplay next to answer_html. */
  back?: CardFace;
  given: string;
  parsed?: string;
  /** Per-item result for templates graded item by item (list cards): each item's rendered answer HTML plus correctness, in item order. */
  breakdown?: { answer_html: string; correct: boolean }[];
}

/**
 * POST /api/v1/review/grade response body (aligned with the Go backend's internal/web/review_api.go).
 * Both the grade and give-up paths return the new state and the same-scope queue; reveal goes through RevealAnswerResponse.
 */
export interface GradedReviewResult {
  card_id: string;
  state: string;
  due_at: string | null;
  version: number;
  stability: number | null;
  cards: DueCard[];
  remaining: number;
  feedback?: GradedFeedback;
  gave_up?: boolean;
}

/**
 * POST /api/v1/review/grade (action=reveal) response body: only the correct answer.
 */
export interface RevealAnswerResponse {
  revealed: boolean;
  card_id: string;
  answer_html: string;
  /** The answer face the frame renders; carries av_tags and autoplay next to answer_html. */
  back?: CardFace;
}

/**
 * POST /api/v1/review/render response body (Go: internal/web/review_api.go).
 * front / back are the card's faces — rendered HTML plus the note type's CSS, body class and media base — and the review page
 * hands them to the sandboxed frame.
 * edit_href points at the card edit page, for the `e` shortcut to jump there.
 */
export interface ReviewRenderResponse {
  card_id: string;
  /**
   * The card's zero-based template ordinal. The frame writes it as the body class `card` + ordinal + 1, which is the
   * `cardN` Anki's reviewer uses.
   */
  ordinal?: number;
  /** The two faces of the card, each carrying its own media base. */
  front: CardFace;
  back: CardFace;
  /**
   * True when the front template is a type-in placeholder without an answer spec. The frame draws `input#typeans`, and
   * the answer face compares the typed value with the field the placeholder names; the rating stays self-chosen.
   */
  type_in?: boolean;
  edit_href: string;
  /** Next interval (seconds) for each of the four grades, ordered Again/Hard/Good/Easy; empty array when the preview fails. */
  intervals?: RatingInterval[];
}

/** Interval from this card to its next review after one grade is submitted (aligned with the Go backend's internal/api/review_preview.go:RatingInterval). */
export interface RatingInterval {
  rating: number;
  seconds: number;
}

/**
 * POST /api/v1/review/bury response body: burying writes only your own progress and creates no reviews rows;
 * cards / remaining are the queue rebuilt over the same scope.
 */
export interface ReviewQueueResponse {
  cards: DueCard[];
  remaining: number;
}

/**
 * POST /api/v1/review/undo response body: besides the rebuilt same-scope queue, it explicitly carries the undone card's public id.
 * The queue is sorted by due_at, so the undone card is not guaranteed to be first and the frontend must locate the current card via undone_card_id.
 */
export interface ReviewUndoResponse {
  cards: DueCard[];
  remaining: number;
  undone_card_id: string;
}

/**
 * GET /api/v1/review/due query parameters
 * The deck parameter may be repeated with multiple deck IDs (mutually exclusive/single/multiple), and limit is in [1, 500]
 */
export interface DueCardsQuery {
  deck?: string | string[];
  /** Take only cards carrying any of these tags; with tags, deck must be exactly one, otherwise the server returns 400. */
  tag?: string[];
  limit?: number;
}

/** One tag in a deck and the number of notes carrying it (GET /api/v1/decks/:id/tags). */
export interface DeckTag {
  tag: string;
  notes: number;
}

export interface DeckTagsResponse {
  tags: DeckTag[];
}

/**
 * User info structure (aligned with the Go backend's internal/web/auth_api.go)
 */
export interface User {
  id: string;
  username: string;
  email: string;
  display_name?: string;
  role: string;
  locale: string;
  /**
   * Whether the account has a local password. An OIDC-created account starts without one; the security page uses this
   * to show "set a password" instead of "change the password", and to explain why its only sign-in identity cannot be unlinked.
   */
  has_password: boolean;
}

/**
 * GET /api/v1/auth/session response body structure
 */
export interface SessionResponse {
  authenticated: boolean;
  user: User | null;
  csrf_token: string;
}

/**
 * POST /api/v1/auth/login request body structure
 */
export interface LoginRequest {
  username: string;
  password: string;
}

/**
 * POST /api/v1/auth/login response body structure
 */
export interface LoginResponse {
  authenticated?: boolean;
  user?: User | null;
  csrf_token?: string;
  requires_totp?: boolean;
}

/**
 * GET /api/v1/auth/totp response body structure: whether the current request holds a valid login second-step credential.
 * The credential is only issued after the first factor (password) passes, so pending=true leaks nothing about whether any account has TOTP enabled.
 */
export interface TOTPPendingResponse {
  pending: boolean;
}

/**
 * POST /api/v1/auth/totp response body structure: same shape as a successful login (the session is issued only after the second factor passes).
 */
export interface TOTPLoginResponse {
  authenticated: boolean;
  user: User | null;
  csrf_token?: string;
}

/**
 * POST /api/v1/auth/logout response body structure
 */
export interface LogoutResponse {
  authenticated: boolean;
  csrf_token?: string;
}

/**
 * POST /api/v1/auth/register request body.
 * When invite is empty the site registration policy decides; when non-empty it takes the one-time invite acceptance path.
 */
export interface RegisterRequest {
  username: string;
  email: string;
  display_name?: string;
  password: string;
  invite?: string;
}

/** POST /api/v1/auth/register response body; a successful registration establishes no session and the frontend then redirects to login. */
export interface RegisterResponse {
  created: boolean;
}

/**
 * POST /api/v1/auth/setup request body.
 * When email is empty and BOOTSTRAP_ADMIN_EMAIL is configured, the server falls back to it.
 */
export interface SetupRequest {
  username: string;
  email?: string;
  display_name?: string;
  password: string;
}

/** POST /api/v1/auth/setup response body; a successful bootstrap establishes no session. */
export interface SetupResponse {
  created: boolean;
}

/**
 * Go backend error body inner structure (internal/api/errors.go:errorBody)
 */
export interface ApiErrorDetail {
  code: string;
  message: string;
}

/**
 * Unified error envelope ({"error": {"code": "...", "message": "..."}})
 */
export interface ApiErrorEnvelope {
  error: ApiErrorDetail;
}

/**
 * API client error class
 * Wraps the HTTP status code, the stable business error code and the raw details
 * AGENTS.md §2.1: the internal message aimed at developers is always English; UI copy is mapped by the frontend language packs
 */
export class ApiClientError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: unknown;

  constructor(message: string, options: { status: number; code: string; details?: unknown }) {
    super(message);
    this.name = 'ApiClientError';
    this.status = options.status;
    this.code = options.code;
    this.details = options.details;

    // Keep the prototype chain
    Object.setPrototypeOf(this, new.target.prototype);
  }

  /**
   * Whether this is the 401 unauthenticated state
   */
  get isUnauthorized(): boolean {
    return this.status === 401 || this.code === 'unauthorized';
  }

  /**
   * Whether this is the 403 insufficient-permission or insufficient-role state
   */
  get isForbidden(): boolean {
    return (
      this.status === 403 ||
      this.code === 'forbidden' ||
      this.code === 'insufficient_role' ||
      this.code === 'scope_required'
    );
  }

  /**
   * Whether this is the 409 data-conflict or version-conflict state
   */
  get isConflict(): boolean {
    return (
      this.status === 409 ||
      this.code === 'conflict' ||
      this.code === 'version_conflict'
    );
  }

  /**
   * Whether this is 404 resource not found
   */
  get isNotFound(): boolean {
    return this.status === 404 || this.code === 'not_found';
  }

  /**
   * Whether this is 429 rate limited
   */
  get isRateLimited(): boolean {
    return this.status === 429 || this.code === 'rate_limited';
  }

  /**
   * Whether this is a network-layer failure (e.g. offline or a fetch exception)
   */
  get isNetworkError(): boolean {
    return this.code === 'network_error';
  }

  /**
   * Whether this is a CSRF validation failure
   */
  get isCsrfError(): boolean {
    return this.code === 'csrf_failed' || this.code === 'csrf_no_session';
  }

  /**
   * Whether this is a wrong-account-credentials error
   */
  get isInvalidCredentials(): boolean {
    return this.code === 'invalid_credentials';
  }

  /**
   * Whether the account is disabled
   */
  get isUserDisabled(): boolean {
    return this.code === 'user_disabled';
  }

  /**
   * Whether two-factor verification is required
   */
  get isTotpRequired(): boolean {
    return this.code === 'totp_required';
  }
}

/**
 * User basic profile and settings structure (Go: internal/store/models.go:User)
 */
export interface UserProfile {
  id?: string;
  username?: string;
  email?: string;
  display_name: string;
  locale: string;
  timezone: string;
  day_cutoff_hour: number | null;
  /** Early-study window (minutes); null means use the server default. */
  learn_ahead_minutes?: number | null;
}

/**
 * Update profile request body structure
 */
export interface UpdateProfileRequest {
  display_name: string;
  locale: string;
  timezone: string;
  day_cutoff_hour?: number | null;
  learn_ahead_minutes?: number | null;
}

/**
 * Profile response body structure
 */
export interface ProfileResponse {
  profile: UserProfile;
}

export interface APIKeyRecord {
  id: string;
  name: string;
  prefix: string;
  scopes: string;
  expires_at?: string | null;
  last_used_at?: string | null;
  revoked_at?: string | null;
  created_at: string;
}

export interface APIKeysResponse {
  keys: APIKeyRecord[];
}

/**
 * Client-side form validation result structure
 */
export interface ProfileValidationResult {
  valid: boolean;
  errors: Partial<Record<'display_name' | 'locale' | 'timezone' | 'day_cutoff_hour' | 'learn_ahead_minutes', string>>;
  data?: UpdateProfileRequest;
}

/**
 * TOTP management endpoints (Go: internal/web/totp_api.go).
 *
 * GET only reports status and never returns the secret or otpauth; the secret appears only once in the begin response,
 * and plaintext recovery codes appear only once in the confirm / recovery responses.
 */
export interface TOTPStatus {
  enabled: boolean;
  pending: boolean;
  recovery_remaining: number;
}

/** POST /api/v1/settings/totp/begin response; the secret and otpauth link appear only here, once. */
export interface TOTPBeginResponse {
  secret: string;
  otpauth_url: string;
  pending: boolean;
}

/** POST /api/v1/settings/totp/confirm response; returns recovery codes once on successful enablement. */
export interface TOTPConfirmResponse {
  enabled: boolean;
  recovery_codes: string[];
  recovery_remaining: number;
}

/** POST /api/v1/settings/totp/disable response. */
export interface TOTPDisableResponse {
  enabled: boolean;
}

/** POST /api/v1/settings/totp/recovery response; returns a new batch of recovery codes once. */
export interface TOTPRecoveryResponse {
  recovery_codes: string[];
  recovery_remaining: number;
}

/**
 * Email notification preference endpoints (Go: internal/web/mail_prefs_api.go).
 *
 * Groups and toggles are derived by the server from the internal/mail catalog; the frontend only looks up its own language pack by stable id
 * and does not keep a separate list of kinds. reminder_hour null means the site default; 0-23 are explicit hours (0 is a valid midnight).
 * Class-D admin notification kinds are returned only to admins: non-admin responses omit that group, and submitting it is rejected with 403 (forbidden_type).
 */
export interface NotificationPrefType {
  type: string;
  enabled: boolean;
  locked: boolean;
}

export interface NotificationPrefGroup {
  class: string;
  types: NotificationPrefType[];
}

/** GET /api/v1/settings/notifications response. */
export interface NotificationPrefsResponse {
  groups: NotificationPrefGroup[];
  reminder_hour: number | null;
  default_reminder_hour: number;
  timezone: string;
}

/** PATCH /api/v1/settings/notifications request; keys missing from choices are treated as off (consistent with an absent checkbox). */
export interface UpdateNotificationPrefsRequest {
  choices: Record<string, boolean>;
  reminder_hour: number | null;
}

/**
 * Scheduling preset endpoints (Go: internal/web/presets_api.go).
 *
 * Same source as the SSR preset page: default preset padding, thresholds, single-concurrency enqueueing, status and revert all reuse the same
 * store/jobs methods. verdict is a three-state enum computed by the server, and the frontend looks it up in the language pack instead of duplicating the criteria.
 */
export interface PresetFitMetrics {
  log_loss: number;
  rmse: number;
  items: number;
}

/** Stable enum for the fit conclusion before/after optimization. */
export type PresetOptimizeVerdict =
  | 'improved'
  | 'not_improved'
  | 'insufficient_sample'
  | 'unavailable';

export interface PresetOptimizeResult {
  reviews_used: number;
  weights: number[] | null;
  fit_before: PresetFitMetrics;
  fit_after: PresetFitMetrics;
  optimized_at: string | null;
  verdict: PresetOptimizeVerdict | string;
}

/** Optimization job status; result is non-null only when succeeded and the report parses. */
export interface PresetJob {
  id: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | string;
  stage: string | null;
  log_tail: string | null;
  error: string | null;
  result: PresetOptimizeResult | null;
}

/** Optimization threshold: shortfall is meaningful only when eligible is false. */
export interface OptimizeGate {
  reviews: number;
  min: number;
  shortfall: number;
  eligible: boolean;
}

export interface PresetRecord {
  id: string;
  name: string;
  /** Whether this is the default preset; both the display name and the "not deletable/not renameable" criteria use it, so the UI need not know the stored name. */
  is_default: boolean;
  desired_retention: number;
  learning_steps: string;
  relearning_steps: string;
  maximum_interval_days: number;
  enable_fuzz: boolean;
  weights_optimized: boolean;
  weights_optimized_at: string | null;
  weights_review_count: number | null;
  weights_raw: string | null;
  job: PresetJob | null;
  /** This preset's optimization threshold: only reviews on decks carrying this preset count, and each preset has its own. */
  gate: OptimizeGate;
}

/** Response body shared by GET /api/v1/presets and create/edit/revert. */
export interface PresetsResponse {
  presets: PresetRecord[];
}

/** Response body shared by triggering optimization and polling status. */
export interface PresetOptimizeResponse {
  job: PresetJob | null;
  gate: OptimizeGate;
}

/** Create/edit preset request body; enable_fuzz must be given explicitly, and the server rejects a missing field. */
export interface PresetWriteRequest {
  name: string;
  desired_retention: number;
  learning_steps: string;
  relearning_steps: string;
  maximum_interval_days: number;
  enable_fuzz: boolean;
}

/**
 * Admin panel endpoints (Go: internal/web/admin_read.go).
 * Responses carry only raw values and stable English identifiers; any localized copy is mapped by the frontend language pack by identifier.
 */

/** GET /api/v1/admin/summary: instance-level counts. */
export interface AdminSummary {
  users_total: number;
  users_active: number;
  decks: number;
  notes: number;
  cards: number;
  due: number;
  jobs_running: number;
  jobs_failed: number;
}

/** GET /api/v1/admin/health: health page readings. schema_version / due null means it could not be read. */
export interface AdminHealth {
  database: 'ok' | 'error';
  schema_version: number | null;
  media_bytes: number;
  media_truncated: boolean;
  due: number | null;
}

/** The operator of an audit row; null means a system action (no UserID). */
export interface AdminAuditActor {
  user_id: string | null;
  username: string;
}

/** The target object of an audit row; null means no target. */
export interface AdminAuditTarget {
  type: string;
  id: string | null;
}

/** One audit record; time is already formatted in the current admin's timezone, and detail is a raw JSON string (empty string means no details). */
export interface AdminAuditRow {
  time: string;
  actor: AdminAuditActor | null;
  action: string;
  target: AdminAuditTarget | null;
  detail: string;
}

/** GET /api/v1/admin/audit response; notice is a stable English code (empty string means no notice). */
export interface AdminAuditResponse {
  rows: AdminAuditRow[];
  actions: string[];
  page: number;
  pages: number;
  total: number;
  notice: string;
}

/** Audit search filters; empty string/omitted means no filtering on that item. */
export interface AdminAuditQuery {
  user?: string;
  action?: string;
  target_type?: string;
  target_id?: string;
  from?: string;
  to?: string;
  page?: number;
}

/** User management (Go: internal/web/admin_users_api.go). */

/** One row in the user list; role/status are stored values, mapped to copy by the frontend. */
export interface AdminUser {
  id: string;
  username: string;
  email: string;
  display_name: string;
  role: string;
  status: string;
  decks: number;
  cards: number;
  reviews: number;
  is_self: boolean;
}

/** GET /api/v1/admin/users response. */
export interface AdminUsersResponse {
  users: AdminUser[];
  page: number;
  pages: number;
  total: number;
  query: string;
}

/** POST /api/v1/admin/users request. */
export interface AdminUserCreateRequest {
  username: string;
  email: string;
  display_name: string;
  password: string;
  role: string;
}

/** Registration and invites (Go: internal/web/admin_registration_api.go). */

/** One invite; role/status are stored values, and link is for the admin to copy. */
export interface AdminInvite {
  id: string;
  token: string;
  link: string;
  email: string;
  role: string;
  status: string;
  created_at: string;
  expires_at: string;
  used_at: string;
  used_by: string;
}

/** GET /api/v1/admin/registration response. */
export interface AdminRegistrationResponse {
  policy: string;
  email_domains: string;
  invites: AdminInvite[];
}

/** POST /api/v1/admin/registration request. */
export interface AdminRegistrationRequest {
  policy: string;
  email_domains: string;
}

/** POST /api/v1/admin/invites request; expires_days null or 0 means never expires. */
export interface AdminInviteCreateRequest {
  email: string;
  role: string;
  expires_days: number | null;
  send_email: boolean;
}

/** POST /api/v1/admin/invites response; mail_notice is a stable English code (empty string means no mail was requested). */
export interface AdminInviteCreateResponse {
  invite: AdminInvite;
  mail_notice: string;
}

/** API key overview (Go: internal/web/admin_keys_api.go). */

/** Metadata of one key; times are already formatted in the admin's timezone, null means unset. */
export interface AdminAPIKey {
  id: string;
  user_id: string;
  owner: string;
  name: string;
  prefix: string;
  scopes: string[];
  last_used_at: string | null;
  expires_at: string | null;
  state: string;
}

/** GET /api/v1/admin/api-keys response. */
export interface AdminAPIKeysResponse {
  keys: AdminAPIKey[];
  page: number;
  pages: number;
  total: number;
}

/** System settings / SMTP / OIDC (Go: internal/web/admin_settings_api.go, admin_smtp.go, admin_oidc.go). */

/** One row in the settings table; when unit is non-empty the value is interpreted in that unit (currently only "bytes"). */
export interface AdminSettingRow {
  key: string;
  value: string;
  source: string;
  editable: boolean;
  sensitive: boolean;
  configured: boolean;
  unit?: string;
}

/** One settings section (general / media / optimize / sensitive). */
export interface AdminSettingsSection {
  name: string;
  rows: AdminSettingRow[];
}

/** GET /api/v1/admin/settings response. */
export interface AdminSettingsResponse {
  sections: AdminSettingsSection[];
}

/** outbox readings. */
export interface AdminOutbox {
  pending: number;
  failed: number;
  last_error: string;
  last_attempts: number;
}

/** GET /api/v1/admin/smtp response. */
export interface AdminSMTPResponse {
  host: string;
  host_source: string;
  port: string;
  port_source: string;
  username: string;
  username_source: string;
  from: string;
  from_source: string;
  tls_mode: string;
  password_configured: boolean;
  password_source: string;
  configured: boolean;
  outbox: AdminOutbox;
  admin_notify_ready: boolean;
}

/** POST /api/v1/admin/smtp(/test) request; empty fields mean keep the saved values. */
export interface AdminSMTPRequest {
  host?: string;
  port?: string;
  username?: string;
  from?: string;
  tls_mode?: string;
  password?: string;
}

/** Test connection response; ignore message when ok is true. */
export interface AdminTestResult {
  ok: boolean;
  code: string;
  message: string;
}

/** One bound identity. */
export interface AdminOIDCIdentity {
  id: string;
  provider: string;
  subject: string;
  email: string;
  username: string;
  linked_at: string;
}

/** GET /api/v1/admin/oidc response. */
export interface AdminOIDCResponse {
  enabled: boolean;
  issuer: string;
  client_id: string;
  secret_configured: boolean;
  redirect_uri: string;
  scopes: string;
  claim_subject: string;
  claim_email: string;
  claim_name: string;
  claim_email_verified: string;
  identities: AdminOIDCIdentity[];
}

/** POST /api/v1/admin/oidc(/test) request; empty string fields mean no change. */
export interface AdminOIDCRequest {
  enabled: boolean;
  issuer?: string;
  client_id?: string;
  scopes?: string;
  claim_subject?: string;
  claim_email?: string;
  claim_name?: string;
  claim_email_verified?: string;
  client_secret?: string;
}

/** Jobs and language pack report (Go: internal/web/admin_jobs_api.go, admin_i18n_api.go). */

/** Metadata of one job; stage null means it has not entered the training stage yet. */
export interface AdminJob {
  id: string;
  kind: string;
  status: string;
  stage: string | null;
  created_at: string;
  started_at: string;
  finished_at: string;
  log_tail: string;
  error: string;
  can_cancel: boolean;
}

/** GET /api/v1/admin/jobs response. */
export interface AdminJobsResponse {
  jobs: AdminJob[];
  page: number;
  pages: number;
  total: number;
}

/** Coverage of one language. */
export interface AdminLocaleCoverage {
  code: string;
  percent: number;
  present: number;
  total: number;
  complete: boolean;
  missing: string[];
}

/** GET /api/v1/admin/i18n response. */
export interface AdminI18nResponse {
  locales: AdminLocaleCoverage[];
  all_complete: boolean;
}
// ---- Account security and mail flows (Go: internal/web/account.go) ----

/** POST /api/v1/auth/forgot-password request. The response only covers site-level mail availability and never echoes whether an account exists. */
export interface ForgotPasswordRequest {
  email: string;
}

export interface ForgotPasswordResponse {
  mail_ready: boolean;
}

/** POST /api/v1/auth/reset-password: one-time token + new password. */
export interface ResetPasswordRequest {
  token: string;
  password: string;
}

export interface ResetPasswordResponse {
  reset: boolean;
}

export interface VerifyEmailResponse {
  verified: boolean;
}

export interface ConfirmEmailChangeResponse {
  changed: boolean;
}

/** GET /api/v1/settings/email (session): current email, verification state, and whether site mail is available. */
export interface EmailSettingsResponse {
  email: string;
  email_verified: boolean;
  mail_ready: boolean;
}

/** POST /api/v1/settings/email (session): request an email change; the stored address stays unchanged until confirmation. */
export interface EmailChangeRequest {
  email: string;
}

export interface EmailChangeResponse {
  sent: boolean;
}

/** POST /api/v1/settings/verify-email (session): resend the verification message. */
export interface ResendVerificationResponse {
  sent: boolean;
}

// ---- Linked sign-in identities (Go: internal/web/identity_api.go) ----

/**
 * One OIDC identity bound to the signed-in account. email is empty when the provider returned no email claim,
 * and linked_at is an RFC3339 timestamp.
 */
export interface IdentityItem {
  id: string;
  provider: string;
  email: string;
  linked_at: string;
}

/** GET /api/v1/settings/identities: whether OIDC sign-in is enabled on this instance, plus the caller's own links. */
export interface IdentitiesResponse {
  oidc_enabled: boolean;
  identities: IdentityItem[];
}

/** POST /api/v1/settings/identities/bind: the provider URL the browser must be sent to. */
export interface BindIdentityResponse {
  authorize_url: string;
}

// ---- OIDC login entry probing (Go: internal/web/oidc_api.go) ----

/** GET /api/v1/auth/oidc: whether the entry is available and the initiation URL; contains no credentials. */
export interface OIDCInfo {
  enabled: boolean;
  start_url: string;
}

// ---- Registration policy probing (Go: internal/web/auth_api.go) ----

/** Site self-registration policy; values correspond one-to-one with Go-side internal/auth/policy.go. */
export type RegistrationPolicy = 'open' | 'invite' | 'closed';

/** GET /api/v1/auth/registration: the current registration policy; contains no credentials and is callable anonymously. */
export interface RegistrationInfo {
  policy: RegistrationPolicy;
}

/**
 * Response of GET /api/v1/unsubscribe?token=....
 * type is the optional mail kind named by the token; the server only reads it and does not consume the token.
 */
export interface UnsubscribeReadResponse {
  type: string;
}

/** POST /api/v1/unsubscribe response: after confirming, returns the kind that was disabled. */
export interface UnsubscribeConfirmResponse {
  type: string;
}

/** Mail templates: one per mail kind per language; a missing one falls back to the built-in body. */
export interface AdminMailTemplateVar {
  name: string;
  required: boolean;
  /** Language pack key for the variable's purpose; the UI localizes by it instead of hardcoding explanatory text. */
  note_key: string;
}

export interface AdminMailTemplateType {
  type: string;
  class: string;
  label_key: string;
  vars: AdminMailTemplateVar[];
}

export interface AdminMailTemplateRow {
  type: string;
  locale: string;
  subject: string;
  body_md: string;
  updated_at: string;
}

/**
 * The **built-in body** of one kind in one language.
 *
 * It shares the same code path as the sender's fallback, only substituting placeholders instead of real values - so after pre-filling the edit box with it,
 * "the default you see" and "the default actually sent" are never two different texts.
 */
export interface AdminMailTemplateDefault {
  type: string;
  locale: string;
  subject: string;
  body_md: string;
}

export interface AdminMailTemplatesResponse {
  locales: string[];
  /** Second level of the fallback chain: used when there is no custom template, and the UI explains that. */
  site_default_locale: string;
  types: AdminMailTemplateType[];
  rows: AdminMailTemplateRow[];
  /** Built-in body for every kind x language; the edit box pre-fills from it, and "restore default" returns to it. */
  defaults: AdminMailTemplateDefault[];
}

export interface AdminMailTemplatePreview {
  subject: string;
  text: string;
  html: string;
  variables: Record<string, string>;
}

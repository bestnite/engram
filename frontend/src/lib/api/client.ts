import {
  ApiClientError,
  type Deck,
  type DecksResponse,
  type DeckTagsResponse,
  type DeckQueueCountsResponse,
  type DeckPauseStatus,
  type DeckSettings,
  type UpdateDeckSettingsRequest,
  type CreateDeckRequest,
  type AdminMailTemplatesResponse,
  type AdminMailTemplatePreview,
  type NotesResponse,
  type Note,
  type NoteListParams,
  type UpdateNoteRequest,
  type CreateNotesRequest,
  type CreateNotesResponse,
  type BulkNotesRequest,
  type BulkNotesResponse,
  type BulkDecksRequest,
  type BulkDecksResponse,
  type NotePreviewResponse,
  type MediaListResponse,
  type MediaUploadResult,
  type StatsSummary,
  type StatsDetail,
  type DueCardsResponse,
  type ExtraNewStatus,
  type DueCardsQuery,
  type SubmitSelfReviewRequest,
  type SubmitReviewResult,
  type SubmitGradedReviewRequest,
  type GradedReviewResult,
  type RevealAnswerResponse,
  type ReviewRenderResponse,
  type ReviewQueueResponse,
  type ReviewUndoResponse,
  type ApiErrorEnvelope,
  type UserProfile,
  type UpdateProfileRequest,
  type ProfileResponse,
  type APIKeyRecord,
  type APIKeysResponse,
  type SessionResponse,
  type LoginRequest,
  type LoginResponse,
  type TOTPPendingResponse,
  type TOTPLoginResponse,
  type LogoutResponse,
  type RegisterRequest,
  type RegisterResponse,
  type SetupRequest,
  type SetupResponse,
  type PackageImportReport,
  type TOTPStatus,
  type TOTPBeginResponse,
  type TOTPConfirmResponse,
  type TOTPDisableResponse,
  type TOTPRecoveryResponse,
  type NotificationPrefsResponse,
  type UpdateNotificationPrefsRequest,
  type PresetsResponse,
  type PresetOptimizeResponse,
  type PresetWriteRequest,
  type ForgotPasswordRequest,
  type ForgotPasswordResponse,
  type ResetPasswordRequest,
  type ResetPasswordResponse,
  type VerifyEmailResponse,
  type ConfirmEmailChangeResponse,
  type EmailSettingsResponse,
  type EmailChangeRequest,
  type EmailChangeResponse,
  type ResendVerificationResponse,
  type OIDCInfo,
  type RegistrationInfo,
  type NoteTypesResponse,
  type NoteTypeStocksResponse,
  type AnswerControlsResponse,
  type CreateNoteTypeRequest,
  type UpdateNoteTypeRequest,
  type UpdateNoteTypeResponse,
  type NoteType,
  type NoteTypeConfig,
  type NoteTypePreviewResponse,
  type EmptyCardListResponse,
  type CleanupEmptyCardsRequest,
  type EmptyCardCleanupResult,
  type ChangeNoteTypeRequest,
  type ChangeNoteTypeResult,
  type IdentitiesResponse,
  type BindIdentityResponse,
} from './types';

/**
 * API client configuration
 */
export interface ApiClientConfig {
  baseUrl?: string;
  fetch?: typeof fetch;
}

/**
 * Derive a stable error code from the HTTP status code
 */
function inferErrorCodeFromStatus(status: number): string {
  switch (status) {
    case 401:
      return 'unauthorized';
    case 403:
      return 'forbidden';
    case 404:
      return 'not_found';
    case 409:
      return 'conflict';
    case 429:
      return 'rate_limited';
    default:
      if (status >= 500) {
        return 'internal_error';
      }
      return 'unknown_error';
  }
}

/**
 * Centralized, type-safe same-origin REST API client
 *
 * Core design decisions:
 * 1. Strictly use credentials: 'same-origin' (HttpOnly cookie session)
 * 2. Never read an API key from storage or send an Authorization Bearer token in request headers
 * 3. Parse error responses safely into a structured ApiClientError; keep diagnostics in English internally and let the UI map them via language packs
 * 4. Zero external HTTP framework dependencies, built on native fetch
 */
/** Kinds of change for onDataChanged, see ApiClient.onDataChanged. */
export type DataChangeKind = 'decks' | 'cards' | 'review';

/**
 * Options of a request whose card-face HTML is rendered by the card frame.
 *
 * With `frame: true` the request carries `X-Engram-Frame: 1`, so the server hands out the card-face HTML with media names
 * relative to the frame's `<base href>` and reports that base as `media_base`. The flag is set only on the calls whose HTML
 * actually enters the frame: choice-option HTML stays in the parent page and keeps the absolute `/media/<sha256>` names.
 */
export interface CardFaceRequestOptions {
  frame?: boolean;
}

/** The header map for one card-face request; absent when the HTML is not going into the frame. */
function frameHeaders(options?: CardFaceRequestOptions): Record<string, string> | undefined {
  return options?.frame ? { 'X-Engram-Frame': '1' } : undefined;
}

export class ApiClient {
  private readonly baseUrl: string;
  private readonly fetchFn: typeof fetch;
  private csrfToken: string | null = null;

  /**
   * Callback for the session becoming invalid mid-way (registered by the app shell: clear auth state + send back to the login page).
   *
   * The client is only responsible for **detecting** that "this session no longer counts"; it does not decide UI policy - policy (whether to redirect, where to)
   * belongs to the shell. With the two separated, the client needs no route imports and forms no circular dependency with views.
   */
  onUnauthorized: (() => void) | null = null;

  /**
   * Notification after a successful write (the shell uses it to refresh the sidebar's deck list and due-review count).
   * decks: decks themselves created/renamed/deleted; cards: cards or quotas changed, so the due count changes; review: review answers, which change frequently,
   * so subscribers may just mark stale instead of refetching immediately. Failed requests do not notify.
   */
  onDataChanged: ((kind: DataChangeKind) => void) | null = null;

  private async notifying<T>(kind: DataChangeKind, pending: Promise<T>): Promise<T> {
    const result = await pending;
    this.onDataChanged?.(kind);
    return result;
  }

  constructor(config: ApiClientConfig = {}) {
    this.baseUrl = config.baseUrl || '';
    this.fetchFn =
      config.fetch ||
      (typeof fetch !== 'undefined' ? fetch.bind(globalThis) : (null as unknown as typeof fetch));
  }

  /**
   * Get the CSRF Token currently held in memory (never stored in localStorage/sessionStorage)
   */
  getCsrfToken(): string | null {
    return this.csrfToken;
  }

  /**
   * Set or update the CSRF Token bound to the client
   */
  setCsrfToken(token: string | null): void {
    this.csrfToken = token;
  }

  /**
   * Base generic request method
   *
   * isRetry is only set inside this method during “stale CSRF” self-healing, to guarantee at most one retry (no infinite recursion).
   */
  async request<T>(path: string, init?: RequestInit, isRetry = false): Promise<T> {
    const url = this.baseUrl ? `${this.baseUrl}${path}` : path;
    const headers = new Headers(init?.headers);

    if (!headers.has('Accept')) {
      headers.set('Accept', 'application/json');
    }

    // Never send an Authorization header
    headers.delete('Authorization');

    const method = (init?.method || 'GET').toUpperCase();
    const isSafeMethod = method === 'GET' || method === 'HEAD' || method === 'OPTIONS';

    // Mutating requests automatically attach X-CSRF-Token and Content-Type: application/json
    if (!isSafeMethod) {
      if (this.csrfToken && !headers.has('X-CSRF-Token')) {
        headers.set('X-CSRF-Token', this.csrfToken);
      }
      if (init?.body && typeof init.body === 'string' && !headers.has('Content-Type')) {
        headers.set('Content-Type', 'application/json');
      }
    }

    let res: Response;
    try {
      res = await this.fetchFn(url, {
        ...init,
        headers,
        // Force same-origin session cookies, managed automatically by the browser
        credentials: 'same-origin',
      });
    } catch (netErr) {
      throw new ApiClientError('Network request failed', {
        status: 0,
        code: 'network_error',
        details: netErr,
      });
    }

    if (!res.ok) {
      let errorEnvelope: ApiErrorEnvelope | null = null;
      try {
        const data = await res.json();
        if (
          data &&
          typeof data === 'object' &&
          'error' in data &&
          data.error &&
          typeof data.error.code === 'string'
        ) {
          errorEnvelope = data as ApiErrorEnvelope;
        }
      } catch {
        // Ignore non-JSON response bodies silently (e.g. a gateway 502 HTML page) and rely on the status code instead
      }

      const code = errorEnvelope?.error?.code || inferErrorCodeFromStatus(res.status);

      // Stale CSRF self-healing: the token in memory no longer matches (typically the session just
      // expired or was swapped while the page had not re-asked the session). Re-fetch the session to get
      // the value expected right now, then retry once as-is - the client holds a single csrf field, which after the re-fetch automatically becomes what the server expects now (the double-submit value when anonymous, the session value when logged in).
      if (code === 'csrf_failed' && !isRetry) {
        await this.getSession();
        return this.request<T>(path, init, true);
      }

      // The session no longer counts: notify the shell to clear auth state and send the user back to login (the shell decides whether to redirect).
      // csrf_no_session means "the CSRF check on the post-login endpoint found no session at all", synonymous with 401.
      if (res.status === 401 || code === 'csrf_no_session') {
        this.onUnauthorized?.();
      }

      throw new ApiClientError(`HTTP ${res.status}: ${code}`, {
        status: res.status,
        code,
        details: errorEnvelope ?? undefined,
      });
    }

    if (res.status === 204) {
      return undefined as unknown as T;
    }

    try {
      return (await res.json()) as T;
    } catch (parseErr) {
      throw new ApiClientError('Failed to parse JSON response', {
        status: res.status,
        code: 'invalid_response',
        details: parseErr,
      });
    }
  }

  /** Download an existing deck package attachment using the same-origin session. */
  async downloadDeckPackage(deckId: string, options: { includeProgress?: boolean; includeMedia?: boolean; includeReviews?: boolean; includeWeights?: boolean } = {}): Promise<{ blob: Blob; filename: string }> {
    if (options.includeReviews && !options.includeProgress) {
      throw new ApiClientError('Review history requires progress export', { status: 400, code: 'invalid_request' });
    }
    const params = new URLSearchParams();
    if (options.includeProgress) params.set('include_progress', '1');
    if (options.includeMedia === false) params.set('include_media', '0');
    if (options.includeReviews) params.set('include_reviews', '1');
    if (options.includeWeights) params.set('include_weights', '1');
    if (!params.has('include_media')) params.set('include_media', '1');
    const query = params.toString();
    const path = `/api/v1/decks/${encodeURIComponent(String(deckId))}/package${query ? `?${query}` : ''}`;
    const headers = new Headers({ Accept: 'application/vnd.engram.edeck' });
    let response: Response;
    try {
      response = await this.fetchFn(this.baseUrl ? `${this.baseUrl}${path}` : path, {
        method: 'GET', headers, credentials: 'same-origin',
      });
    } catch (err) {
      throw new ApiClientError('Network request failed', { status: 0, code: 'network_error', details: err });
    }
    if (!response.ok) {
      let envelope: ApiErrorEnvelope | null = null;
      try { envelope = await response.json() as ApiErrorEnvelope; } catch { /* Non-JSON errors use status fallback. */ }
      const code = envelope?.error?.code || inferErrorCodeFromStatus(response.status);
      throw new ApiClientError(`HTTP ${response.status}: ${code}`, { status: response.status, code, details: envelope ?? undefined });
    }
    const disposition = response.headers.get('Content-Disposition') || '';
    const match = disposition.match(/filename\*=UTF-8''([^;]+)|filename="?([^";]+)"?/i);
    const filename = match?.[1] ? decodeURIComponent(match[1]) : (match?.[2] || `deck-${deckId}.edeck`);
    return { blob: await response.blob(), filename };
  }

  /** Import a deck package through the same-origin session multipart route. */
  async importDeckPackage(file: File, options: { target: string; dryRun?: boolean; onConflict?: 'skip' | 'update' | 'fail'; allowOthersProgress?: boolean; skipMissingMedia?: boolean; applyWeights?: boolean }): Promise<PackageImportReport> {
    if (!this.csrfToken) await this.getSession();
    const form = new FormData();
    form.append('file', file, file.name);
    form.append('target', options.target);
    form.append('dry_run', options.dryRun ? '1' : '0');
    form.append('on_conflict', options.onConflict || 'update');
    form.append('allow_others_progress', options.allowOthersProgress ? '1' : '0');
    form.append('skip_missing_media', options.skipMissingMedia ? '1' : '0');
    form.append('apply_weights', options.applyWeights ? '1' : '0');
    return this.notifying('decks', this.request<PackageImportReport>('/api/v1/decks/import', { method: 'POST', body: form }));
  }

  /**
   * Import a deck package from a public HTTPS direct link through the same-origin session JSON route (POST /api/v1/decks/import-url).
   *
   * Shares the same set of options as file import (target / dry_run / on_conflict / allow_others_progress /
   * skip_missing_media / apply_weights), the server returns the same PackageImportReport, and the frontend does not distinguish result shapes.
   * Write out every field explicitly instead of relying on server defaults: option semantics must be identical on both entry points, and omitting them would
   * decouple "the value shown in the UI" from "the value actually in effect". CSRF and Content-Type are injected centrally by request (only
   * string bodies force application/json).
   */
  async importDeckPackageURL(
    rawURL: string,
    options: { target: string; dryRun?: boolean; onConflict?: 'skip' | 'update' | 'fail'; allowOthersProgress?: boolean; skipMissingMedia?: boolean; applyWeights?: boolean }
  ): Promise<PackageImportReport> {
    if (!this.csrfToken) await this.getSession();
    return this.notifying('decks', this.request<PackageImportReport>('/api/v1/decks/import-url', {
      method: 'POST',
      body: JSON.stringify({
        url: rawURL,
        target: options.target,
        dry_run: options.dryRun === true,
        on_conflict: options.onConflict || 'update',
        allow_others_progress: options.allowOthersProgress === true,
        skip_missing_media: options.skipMissingMedia === true,
        apply_weights: options.applyWeights === true,
      }),
    }));
  }

  /**
   * Read one deck's note types (GET /api/v1/decks/:id/note-types, scope: read).
   * Each entry carries the decoded config the editor builds its fields from; the server orders the list by name then id.
   */
  async listNoteTypes(deckId: string): Promise<NoteTypesResponse> {
    const id = encodeURIComponent(String(deckId));
    return this.request<NoteTypesResponse>(`/api/v1/decks/${id}/note-types`);
  }

  /**
   * Render one template of an unsaved note type config (POST /api/v1/decks/:id/note-types/preview, editor role).
   *
   * The body carries the whole draft config and the selected template id, so the server's own renderer produces the
   * preview instead of a second renderer in the page. Every field renders with its own name as the value. A request
   * with `frame: true` receives media names relative to the frame's `<base href>`; the HTML only enters the frame.
   */
  async previewNoteType(
    deckId: string,
    config: NoteTypeConfig,
    templateId: string,
    options?: CardFaceRequestOptions,
  ): Promise<NoteTypePreviewResponse> {
    if (!this.csrfToken) await this.getSession();
    const id = encodeURIComponent(String(deckId));
    return this.request<NoteTypePreviewResponse>(`/api/v1/decks/${id}/note-types/preview`, {
      method: 'POST',
      headers: frameHeaders(options),
      body: JSON.stringify({ config, template_id: templateId }),
    });
  }

  /**
   * Read the stock definitions a note type can be created from (GET /api/v1/note-type-stocks, scope: read).
   * Static metadata; the create call sends the returned id and the server builds the config.
   */
  async listNoteTypeStocks(): Promise<NoteTypeStocksResponse> {
    return this.request<NoteTypeStocksResponse>('/api/v1/note-type-stocks');
  }

  /**
   * Read the registered answer controls and their roles (GET /api/v1/answer-controls, scope: read).
   * The note type editor builds its control and binding pickers from this response, so a newly
   * registered control needs no frontend release.
   */
  async listAnswerControls(): Promise<AnswerControlsResponse> {
    return this.request<AnswerControlsResponse>('/api/v1/answer-controls');
  }

  /**
   * Create a note type in a deck (POST /api/v1/decks/:id/note-types, scope: write, editor role).
   * Send stock_id to build the config on the server, or config to copy an existing type.
   */
  async createNoteType(deckId: string, input: CreateNoteTypeRequest): Promise<NoteType> {
    if (!this.csrfToken) await this.getSession();
    const id = encodeURIComponent(String(deckId));
    return this.notifying('cards', this.request<NoteType>(`/api/v1/decks/${id}/note-types`, {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Update a note type's name and config (PATCH /api/v1/decks/:id/note-types/:type_id).
   * A dry_run returns the card impact without writing. A 409 card_removal_unconfirmed error carries
   * the same counts in its response body, so the caller can show them before confirming.
   */
  async updateNoteType(
    deckId: string,
    typeId: string,
    input: UpdateNoteTypeRequest,
  ): Promise<UpdateNoteTypeResponse> {
    if (!this.csrfToken) await this.getSession();
    const id = encodeURIComponent(String(deckId));
    const type = encodeURIComponent(String(typeId));
    return this.request<UpdateNoteTypeResponse>(`/api/v1/decks/${id}/note-types/${type}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    });
  }

  /**
   * Delete a note type (DELETE /api/v1/decks/:id/note-types/:type_id); the server refuses one a live note still uses.
   * A type used only by soft-deleted notes answers 409 note_type_purge_unconfirmed with the impact counts; repeat the
   * call with confirmPurge to hard-delete those notes and their cards. The review rows always stay.
   */
  async deleteNoteType(deckId: string, typeId: string, confirmPurge = false): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    const id = encodeURIComponent(String(deckId));
    const type = encodeURIComponent(String(typeId));
    const query = confirmPurge ? '?confirm_purge=true' : '';
    await this.notifying('cards', this.request<void>(`/api/v1/decks/${id}/note-types/${type}${query}`, {
      method: 'DELETE',
    }));
  }

  /**
   * List the cards of a deck whose front renders empty (GET /api/v1/decks/:id/empty-cards, editor role).
   * Read-only: the cleanup that follows is a separate request.
   */
  async listEmptyCards(deckId: string): Promise<EmptyCardListResponse> {
    const id = encodeURIComponent(String(deckId));
    return this.request<EmptyCardListResponse>(`/api/v1/decks/${id}/empty-cards`);
  }

  /**
   * Clean up a deck's empty cards (POST /api/v1/decks/:id/empty-cards, editor role).
   * A dry_run reports the counts without writing. Without confirm_card_removal the server answers
   * 409 card_removal_unconfirmed and keeps the counts in the response body.
   */
  async cleanupEmptyCards(
    deckId: string,
    input: CleanupEmptyCardsRequest,
  ): Promise<EmptyCardCleanupResult> {
    if (!this.csrfToken) await this.getSession();
    const id = encodeURIComponent(String(deckId));
    return this.notifying('cards', this.request<EmptyCardCleanupResult>(`/api/v1/decks/${id}/empty-cards`, {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Move one note type's notes to another type of the same deck (POST /api/v1/decks/:id/note-types/:type_id/change).
   * A dry_run reports the impact without writing. A 409 card_removal_unconfirmed error carries the same counts in
   * its response body, so the caller can ask for the extra confirmation before resending.
   */
  async changeNoteType(
    deckId: string,
    typeId: string,
    input: ChangeNoteTypeRequest,
  ): Promise<ChangeNoteTypeResult> {
    if (!this.csrfToken) await this.getSession();
    const id = encodeURIComponent(String(deckId));
    const type = encodeURIComponent(String(typeId));
    return this.notifying('cards', this.request<ChangeNoteTypeResult>(`/api/v1/decks/${id}/note-types/${type}/change`, {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Get the current user's deck list (GET /api/v1/decks)
   */
  async getDecks(): Promise<DecksResponse> {
    return this.request<DecksResponse>('/api/v1/decks');
  }

  /** Fetch per-visible-deck queue counts generated by the shared schedule queue builder. */
  async getDeckQueueCounts(): Promise<DeckQueueCountsResponse> {
    return this.request<DeckQueueCountsResponse>('/api/v1/decks/queue-counts');
  }

  /**
   * Read a deck's daily limit plus today's used/remaining (GET /api/v1/decks/:id/settings, owner only, session only).
   * The server returns 403/404 for non-owners; this method does not hide that failure.
   */
  async getDeckSettings(deckId: string): Promise<DeckSettings> {
    const id = encodeURIComponent(String(deckId));
    return this.request<DeckSettings>(`/api/v1/decks/${id}/settings`);
  }

  /**
   * Save a deck's daily limit (PATCH /api/v1/decks/:id/settings, owner only).
   * 0 means unlimited and must be submitted verbatim; when the session token is missing, fetch a CSRF token first.
   */
  async updateDeckSettings(deckId: string, input: UpdateDeckSettingsRequest): Promise<DeckSettings> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.notifying('cards', this.request<DeckSettings>(`/api/v1/decks/${id}/settings`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    }));
  }

  /** Create a deck; when the session token is missing, fetch a new CSRF token first. */
  async createDeck(input: CreateDeckRequest): Promise<Deck> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('decks', this.request<Deck>('/api/v1/decks', {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Update a deck's name and description (PATCH /api/v1/decks/:id, owner only).
   * Validation matches deck creation: name non-empty and at most 200 characters, description at most 2000 characters (an empty description is valid).
   */
  async updateDeck(deckId: string, input: { name: string; description: string }): Promise<Deck> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.notifying('decks', this.request<Deck>(`/api/v1/decks/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Pause or resume a deck for the caller only (PUT /api/v1/decks/:id/paused, at least reader).
   * The default queue and the due counts change with it, so the sidebar is told the decks changed.
   */
  async setDeckPaused(deckId: string, paused: boolean): Promise<DeckPauseStatus> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.notifying('decks', this.request<DeckPauseStatus>(`/api/v1/decks/${id}/paused`, {
      method: 'PUT',
      body: JSON.stringify({ paused }),
    }));
  }

  /** Delete a deck (DELETE /api/v1/decks/:id, owner only). */
  async deleteDeck(deckId: string): Promise<{ deleted: boolean }> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.notifying('decks', this.request<{ deleted: boolean }>(`/api/v1/decks/${id}`, {
      method: 'DELETE',
    }));
  }

  /**
   * Apply one bulk action to a set of decks (POST /api/v1/decks/bulk, scope: write).
   * Only delete is accepted today; the server decides ownership per deck, reports rejected rows in skipped,
   * and leaves the list untouched for those decks.
   */
  async bulkDecks(input: BulkDecksRequest): Promise<BulkDecksResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('decks', this.request<BulkDecksResponse>('/api/v1/decks/bulk', {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Leave a shared deck (DELETE /api/v1/decks/:id/membership, scope: write).
   * Only deletes your own authorization row on the deck and does not touch deck content; the owner has no "leave" semantics and is rejected by the server.
   */
  async leaveDeck(deckId: string): Promise<{ left: boolean }> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.notifying('decks', this.request<{ left: boolean }>(`/api/v1/decks/${id}/membership`, {
      method: 'DELETE',
    }));
  }

  /** Export the selected deck packages as a single zip archive (POST /api/v1/decks/export-zip). */
  async exportDecksZip(
    deckIds: string[],
    options: { includeMedia?: boolean; includeProgress?: boolean; includeReviews?: boolean; includeWeights?: boolean } = {}
  ): Promise<{ blob: Blob; filename: string }> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const headers = new Headers({
      'Content-Type': 'application/json',
      Accept: 'application/zip',
    });
    if (this.csrfToken) {
      headers.set('X-CSRF-Token', this.csrfToken);
    }
    const res = await this.fetchFn(this.baseUrl ? `${this.baseUrl}/api/v1/decks/export-zip` : '/api/v1/decks/export-zip', {
      method: 'POST',
      headers,
      credentials: 'same-origin',
      body: JSON.stringify({
        deck_ids: deckIds,
        include_media: options.includeMedia ?? true,
        include_progress: options.includeProgress ?? false,
        include_reviews: options.includeReviews ?? false,
        include_weights: options.includeWeights ?? false,
      }),
    });
    if (!res.ok) {
      let envelope: any = null;
      try { envelope = await res.json(); } catch {}
      const code = envelope?.error?.code || inferErrorCodeFromStatus(res.status);
      throw new ApiClientError(`HTTP ${res.status}: ${code}`, { status: res.status, code, details: envelope ?? undefined });
    }
    const disposition = res.headers.get('Content-Disposition') || '';
    const match = disposition.match(/filename\*=UTF-8''([^;]+)|filename="?([^";]+)"?/i);
    const filename = match?.[1] ? decodeURIComponent(match[1]) : (match?.[2] || 'engram-decks-export.zip');
    return { blob: await res.blob(), filename };
  }

  /**
   * Clone a deck you can read (POST /api/v1/decks/:id/clone).
   * reader and above can clone; the server authorizes and audits, returning the new deck's {id, name} (progress does not carry over).
   * Declare Accept: application/json explicitly so the server returns JSON instead of a 303 redirect.
   */
  async cloneDeck(deckId: string): Promise<{ id: string; name: string }> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.request<{ id: string; name: string }>(`/api/v1/decks/${id}/clone`, {
      method: 'POST',
      headers: { Accept: 'application/json' },
      body: JSON.stringify({}),
    });
  }

  /**
   * Get the card list under a deck (GET /api/v1/decks/:id/notes)
   */
  async getDeckNotes(
    deckId: string,
    params?: NoteListParams
  ): Promise<NotesResponse> {
    const encodedId = encodeURIComponent(String(deckId));
    let qs = '';
    if (params) {
      const searchParams = new URLSearchParams();
      if (params.page !== undefined) {
        searchParams.set('page', String(params.page));
      }
      if (params.per_page !== undefined) {
        searchParams.set('per_page', String(params.per_page));
      }
      if (params.q !== undefined && params.q !== '') {
        searchParams.set('q', params.q);
      }
      if (params.tag !== undefined && params.tag !== '') {
        searchParams.set('tag', params.tag);
      }
      if (params.status !== undefined && params.status !== '') {
        searchParams.set('status', params.status);
      }
      const qsStr = searchParams.toString();
      if (qsStr) {
        qs = `?${qsStr}`;
      }
    }
    return this.request<NotesResponse>(`/api/v1/decks/${encodedId}/notes${qs}`);
  }

  /**
   * Get a deck's card list (alias of getDeckNotes)
   */
  async getNotes(
    deckId: string,
    params?: NoteListParams
  ): Promise<NotesResponse> {
    return this.getDeckNotes(deckId, params);
  }

  /** Update one existing note through the authenticated, CSRF-protected REST API. */
  async updateNote(noteId: string, input: UpdateNoteRequest): Promise<Note> {
    const id = encodeURIComponent(String(noteId));
    return this.request<Note>(`/api/v1/notes/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    });
  }

  /** Soft-delete one note through the authenticated, CSRF-protected REST API. */
  async deleteNote(noteId: string): Promise<{ deleted: boolean; id: string }> {
    const id = encodeURIComponent(String(noteId));
    return this.notifying('cards', this.request<{ deleted: boolean; id: string }>(`/api/v1/notes/${id}`, {
      method: 'DELETE',
    }));
  }

  /** Create notes via the safe bulk endpoint using the same-origin session and in-memory CSRF token. */
  async createNotes(deckId: string, input: CreateNotesRequest): Promise<CreateNotesResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encodedId = encodeURIComponent(String(deckId));
    return this.notifying('cards', this.request<CreateNotesResponse>(`/api/v1/decks/${encodedId}/notes`, {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Bulk delete/retag (POST /api/v1/notes/bulk).
   * Row-level failures do not roll back the whole batch: the response's skipped lists not_found / insufficient_role per row,
   * and callers report the real result from affected and skipped, making no optimistic assumptions.
   */
  async bulkNotes(input: BulkNotesRequest): Promise<BulkNotesResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('cards', this.request<BulkNotesResponse>('/api/v1/notes/bulk', {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Preview fields using the authenticated session-only preview endpoint.
   *
   * noteTypeID is the public id of a note type of the deck in the address; the server renders that stored type (templates
   * and CSS included). fields is keyed by the note type's field names, the same shape a note write accepts.
   */
  async previewNote(
    deckId: string,
    noteTypeID: string,
    fields: Record<string, unknown>,
    options?: CardFaceRequestOptions,
  ): Promise<NotePreviewResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encodedId = encodeURIComponent(String(deckId));
    return this.request<NotePreviewResponse>(`/api/v1/decks/${encodedId}/notes/preview`, {
      method: 'POST',
      headers: frameHeaders(options),
      body: JSON.stringify({ note_type: noteTypeID, fields }),
    });
  }

  /**
   * List one page of the media library the current user can read (GET /api/v1/media).
   * Replaces the removed SSR fragment endpoint: each returned JSON item has a sha256 and a directly usable url (/media/<sha256>),
   * so the frontend no longer parses HTML. When params are omitted the server applies a default page size; an invalid/oversized cursor gets a 400 from the server.
   */
  async listMedia(cursor = ''): Promise<MediaListResponse> {
    const params = new URLSearchParams();
    if (cursor) params.set('cursor', cursor);
    const query = params.toString();
    return this.request<MediaListResponse>(`/api/v1/media${query ? `?${query}` : ''}`);
  }

  /**
   * Upload one media file through the in-deck editor upload entry (POST /decks/:id/media).
   * The endpoint requires editor and above, enforced by the server, and the frontend does not hide failures; the multipart boundary is left to the browser,
   * so request does not set Content-Type for FormData (only string bodies force application/json).
   * A success response must satisfy the /media/<sha256> hash contract, otherwise it is rejected as invalid, avoiding insertion of unconstrained
   * strings into fields.
   */
  async uploadDeckMedia(deckId: string, file: File): Promise<MediaUploadResult> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encodedId = encodeURIComponent(String(deckId));
    const form = new FormData();
    form.append('file', file, file.name);
    const result = await this.request<MediaUploadResult>(`/decks/${encodedId}/media`, {
      method: 'POST',
      body: form,
    });
    if (!/^[0-9a-f]{64}$/.test(result.sha256) || result.url !== `/media/${result.sha256}`) {
      throw new ApiClientError('Invalid media upload response', { status: 201, code: 'invalid_response' });
    }
    return result;
  }

  /**
   * Find a single deck's metadata in the deck list
   */
  async getDeck(deckId: string): Promise<Deck | null> {
    const res = await this.getDecks();
    return res.decks.find((d) => d.id === String(deckId)) || null;
  }

  /** List a deck's tags with their note counts (GET /api/v1/decks/:id/tags), for choosing review by tag. */
  async getDeckTags(deckId: string): Promise<DeckTagsResponse> {
    return this.request<DeckTagsResponse>(`/api/v1/decks/${encodeURIComponent(String(deckId))}/tags`);
  }

  /**
   * Read a single card (GET /api/v1/notes/:id).
   *
   * The edit page fetches the target row directly by public id instead of pulling the list and finding it - the list is paginated, so
   * cards outside the current page would otherwise never open.
   */
  async getNote(noteId: string): Promise<Note> {
    const id = encodeURIComponent(String(noteId));
    return this.request<Note>(`/api/v1/notes/${id}`);
  }

  /**
   * Get the current user's basic profile (GET /api/v1/profile)
   */
  async getProfile(): Promise<UserProfile> {
    const res = await this.request<ProfileResponse | UserProfile>('/api/v1/profile');
    if (res && typeof res === 'object' && 'profile' in res && res.profile) {
      return res.profile;
    }
    return res as UserProfile;
  }

  /**
   * Update the current user's profile (PATCH /api/v1/profile)
   * Strictly use same-origin credentials and the CSRF header
   */
  async updateProfile(
    data: UpdateProfileRequest,
    options?: { csrfToken?: string }
  ): Promise<UserProfile> {
    const headers = new Headers();
    const token = options?.csrfToken || this.csrfToken;
    if (token) {
      headers.set('X-CSRF-Token', token);
    }
    const res = await this.request<ProfileResponse | UserProfile>('/api/v1/profile', {
      method: 'PATCH',
      headers,
      body: JSON.stringify(data),
    });
    if (res && typeof res === 'object' && 'profile' in res && res.profile) {
      return res.profile;
    }
    return res as UserProfile;
  }

  /**
   * Change only the UI language preference (PATCH /api/v1/settings/locale).
   *
   * The header language switcher uses it to persist immediately; the /settings language field is submitted with the profile form (PATCH /api/v1/profile),
   * both paths write the same column, and the UI treats the store as authoritative.
   */
  async updateLocale(
    locale: string,
    options?: { csrfToken?: string }
  ): Promise<{ locale: string }> {
    const headers = new Headers();
    const token = options?.csrfToken || this.csrfToken;
    if (token) {
      headers.set('X-CSRF-Token', token);
    }
    return this.request<{ locale: string }>('/api/v1/settings/locale', {
      method: 'PATCH',
      headers,
      body: JSON.stringify({ locale }),
    });
  }

  /**
   * Get the current user's study stats summary (GET /api/v1/stats/summary)
   */
  async getStatsSummary(): Promise<StatsSummary> {
    return this.request<StatsSummary>('/api/v1/stats/summary');
  }

  /**
   * Get the current user's detailed stats (GET /api/v1/stats/detail)
   * The same aggregate queries as the stats page, returning raw values for every metric (counts/ratios/milliseconds); localization is the frontend's job.
   */
  async getStatsDetail(): Promise<StatsDetail> {
    return this.request<StatsDetail>('/api/v1/stats/detail');
  }

  /**
   * Get the due card list (GET /api/v1/review/due)
   * The deck parameter may be repeated with multiple deck IDs (mutually exclusive/single/multiple), and limit is in [1, 500];
   * tag may be repeated to take only cards carrying any of those tags (in that case deck must be exactly one)
   */
  async getDueCards(query?: DueCardsQuery): Promise<DueCardsResponse> {
    const params = new URLSearchParams();
    if (query?.deck !== undefined) {
      if (Array.isArray(query.deck)) {
        for (const d of query.deck) {
          params.append('deck', String(d));
        }
      } else {
        params.append('deck', String(query.deck));
      }
    }
    for (const tag of query?.tag ?? []) {
      params.append('tag', tag);
    }
    if (query?.limit !== undefined) {
      params.set('limit', String(query.limit));
    }
    const queryString = params.toString();
    const path = queryString ? `/api/v1/review/due?${queryString}` : '/api/v1/review/due';
    return this.request<DueCardsResponse>(path);
  }

  /**
   * Read today's new-card quota and the default count for "one more round" (GET /api/v1/decks/:id/extra-new);
   * tag follows the same rule as the review queue and only affects how many extra cards can be added.
   */
  async getExtraNew(deckId: string, tags: string[] = []): Promise<ExtraNewStatus> {
    const params = new URLSearchParams();
    for (const tag of tags) params.append('tag', tag);
    const query = params.toString();
    const path = `/api/v1/decks/${encodeURIComponent(deckId)}/extra-new${query ? `?${query}` : ''}`;
    return this.request<ExtraNewStatus>(path);
  }

  /**
   * "One more round": add new-card quota for this deck, for today only and for yourself only (POST /api/v1/decks/:id/extra-new).
   * When count is omitted the server uses the daily new-card limit. The quota changed, so the due count changes too, hence the cards notification.
   */
  async addExtraNew(deckId: string, count?: number): Promise<ExtraNewStatus> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('cards', this.request<ExtraNewStatus>(`/api/v1/decks/${encodeURIComponent(deckId)}/extra-new`, {
      method: 'POST',
      body: JSON.stringify(count === undefined ? {} : { count }),
    }));
  }

  /** Submit self-grades through the web-only CSRF endpoint; the server rebuilds the same-scope queue. */
  async submitSelfReview(input: SubmitSelfReviewRequest): Promise<SubmitReviewResult> {
    const { deck, ...review } = input;
    return this.notifying('review', this.request<SubmitReviewResult>('/api/v1/review/answer', {
      method: 'POST',
      body: JSON.stringify({ ...review, deck }),
    }));
  }

  /**
   * Submit raw answers for answer-graded templates; the server grades them and writes reviews.
   * The client does not submit a grade; the grade comes from the server's graderFor and the preset mapping.
   */
  async submitGradedReview(
    input: SubmitGradedReviewRequest,
    options?: CardFaceRequestOptions,
  ): Promise<GradedReviewResult> {
    return this.notifying('review', this.request<GradedReviewResult>('/api/v1/review/grade', {
      method: 'POST',
      headers: frameHeaders(options),
      body: JSON.stringify(input),
    }));
  }

  /** Request revealing the correct answer for answer-graded templates (read-only preview, writes no progress). */
  async revealGradedAnswer(
    input: SubmitGradedReviewRequest,
    options?: CardFaceRequestOptions,
  ): Promise<RevealAnswerResponse> {
    return this.request<RevealAnswerResponse>('/api/v1/review/grade', {
      method: 'POST',
      headers: frameHeaders(options),
      body: JSON.stringify({ ...input, action: 'reveal' }),
    });
  }

  /**
   * Fetch both rendered sides of a card plus its edit URL (POST /api/v1/review/render).
   * The review page only hands this HTML to the card frame and never renders raw fields as Markdown.
   */
  async renderReviewCard(
    input: { card_id: string; deck?: string[]; tags?: string[]; answer?: string },
    options?: CardFaceRequestOptions,
  ): Promise<ReviewRenderResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ReviewRenderResponse>('/api/v1/review/render', {
      method: 'POST',
      headers: frameHeaders(options),
      body: JSON.stringify(input),
    });
  }

  /**
   * Bury the current card (POST /api/v1/review/bury): writes only your own progress and creates no reviews rows.
   * The response carries the queue rebuilt over the same scope, so cross-deck review does not degrade to a single deck.
   */
  async buryReview(input: { card_id: string; deck?: string[]; tags?: string[] }): Promise<ReviewQueueResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('review', this.request<ReviewQueueResponse>('/api/v1/review/bury', {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Suspend the current card (POST /api/v1/review/suspend): applies to you only and creates no reviews rows;
   * the response carries the queue rebuilt over the same scope.
   */
  async suspendReview(input: { card_id: string; deck?: string[]; tags?: string[] }): Promise<ReviewQueueResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('review', this.request<ReviewQueueResponse>('/api/v1/review/suspend', {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /**
   * Undo the review just submitted (POST /api/v1/review/undo): restores progress and deletes the undone review log.
   * The response carries the rebuilt same-scope queue and the undone card's public id (undone_card_id); the queue is sorted by due_at,
   * so the undone card is not guaranteed to be first and callers must relocate the current card using undone_card_id.
   */
  async undoReview(input: { card_id: string; expected_version: number; deck?: string[]; tags?: string[] }): Promise<ReviewUndoResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('review', this.request<ReviewUndoResponse>('/api/v1/review/undo', {
      method: 'POST',
      body: JSON.stringify(input),
    }));
  }

  /** Suspend or resume all of your cards under one note (PUT / DELETE /api/v1/notes/:id/suspension). */
  async setNoteSuspended(noteId: string, suspended: boolean): Promise<{ suspended: boolean; cards: string[] }> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.notifying('cards', this.request<{ suspended: boolean; cards: string[] }>(`/api/v1/notes/${encodeURIComponent(noteId)}/suspension`, {
      method: suspended ? 'PUT' : 'DELETE',
    }));
  }

  /** Manage the current account's API keys; writes reuse the in-memory CSRF token auto-injected by request. */
  async getAPIKeys(): Promise<APIKeysResponse> {
    return this.request<APIKeysResponse>('/api/v1/keys');
  }

  /**
   * Create a key for the current account. `expires_at` is an RFC3339/ISO timestamp computed by the caller;
   * omitting it (or null) means the key never expires.
   */
  async createAPIKey(input: { name: string; scopes: string[]; expires_at?: string | null }): Promise<{ key: APIKeyRecord; plaintext: string }> {
    return this.request<{ key: APIKeyRecord; plaintext: string }>('/api/v1/keys', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  async deleteAPIKey(id: string): Promise<{ revoked: boolean; id: string }> {
    return this.request<{ revoked: boolean; id: string }>(`/api/v1/keys/${encodeURIComponent(String(id))}`, {
      method: 'DELETE',
    });
  }

  /**
   * Get session state and the security CSRF Token (GET /api/v1/auth/session)
   */
  async changePassword(data: { old_password?: string; new_password: string }): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.request<void>('/api/v1/settings/password', { method: 'PATCH', body: JSON.stringify(data) });
  }

  /**
   * Read the OIDC identities bound to the signed-in account (GET /api/v1/settings/identities, session only).
   * oidc_enabled describes the instance, so the page can still list existing links while sign-in is off.
   */
  async getIdentities(): Promise<IdentitiesResponse> {
    return this.request<IdentitiesResponse>('/api/v1/settings/identities');
  }

  /**
   * Start linking a new OIDC identity (POST /api/v1/settings/identities/bind).
   * The response carries the provider's authorize URL; the browser leaves the SPA to complete the flow there.
   */
  async beginIdentityBind(): Promise<BindIdentityResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<BindIdentityResponse>('/api/v1/settings/identities/bind', {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }

  /** Unlink one OIDC identity (DELETE /api/v1/settings/identities/:id); the server refuses the last credential with 409. */
  async unlinkIdentity(id: string): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.request<void>(`/api/v1/settings/identities/${encodeURIComponent(String(id))}`, {
      method: 'DELETE',
    });
  }

  /** Read the current user's TOTP status (GET /api/v1/settings/totp, session only); never returns the secret. */
  async getTOTPStatus(): Promise<TOTPStatus> {
    return this.request<TOTPStatus>('/api/v1/settings/totp');
  }

  /** Generate a pending secret (POST /api/v1/settings/totp/begin); the secret appears only in this response. */
  async beginTOTP(): Promise<TOTPBeginResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPBeginResponse>('/api/v1/settings/totp/begin', {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }

  /** Confirm enrollment with a one-time code (POST /api/v1/settings/totp/confirm); on success it returns recovery codes once. */
  async confirmTOTP(code: string): Promise<TOTPConfirmResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPConfirmResponse>('/api/v1/settings/totp/confirm', {
      method: 'POST',
      body: JSON.stringify({ code }),
    });
  }

  /** Disable TOTP (POST /api/v1/settings/totp/disable, requires the password). */
  async disableTOTP(password: string): Promise<TOTPDisableResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPDisableResponse>('/api/v1/settings/totp/disable', {
      method: 'POST',
      body: JSON.stringify({ password }),
    });
  }

  /** Regenerate recovery codes (POST /api/v1/settings/totp/recovery, requires the password); old codes are invalidated immediately. */
  async regenerateTOTPRecovery(password: string): Promise<TOTPRecoveryResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPRecoveryResponse>('/api/v1/settings/totp/recovery', {
      method: 'POST',
      body: JSON.stringify({ password }),
    });
  }

  /** Read email notification preferences (GET /api/v1/settings/notifications, session only); toggles are derived by the server from the catalog. */
  async getNotificationPrefs(): Promise<NotificationPrefsResponse> {
    return this.request<NotificationPrefsResponse>('/api/v1/settings/notifications');
  }

  /** Save email notification preferences (PATCH /api/v1/settings/notifications); writes require session CSRF. */
  async updateNotificationPrefs(data: UpdateNotificationPrefsRequest): Promise<NotificationPrefsResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<NotificationPrefsResponse>('/api/v1/settings/notifications', {
      method: 'PATCH',
      body: JSON.stringify(data),
    });
  }

  async getSession(): Promise<SessionResponse> {
    const res = await this.request<SessionResponse>('/api/v1/auth/session');
    if (res?.csrf_token) {
      this.csrfToken = res.csrf_token;
    }
    return res;
  }

  /**
   * SPA same-origin password login (POST /api/v1/auth/login)
   */
  async login(credentials: LoginRequest): Promise<LoginResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const res = await this.request<LoginResponse>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify(credentials),
    });
    if (res?.csrf_token) {
      this.csrfToken = res.csrf_token;
    }
    return res;
  }

  /**
   * Check whether the login second step (TOTP) can be submitted (GET /api/v1/auth/totp).
   * Read-only: does not refresh credential lifetime and does not establish a session.
   */
  async getTOTPPending(): Promise<TOTPPendingResponse> {
    return this.request<TOTPPendingResponse>('/api/v1/auth/totp');
  }

  /**
   * Submit the login second step's code or one-time recovery code (POST /api/v1/auth/totp).
   * Pre-session flow: writes use double-submit CSRF; when the token is missing, fetch a session token first.
   * Only after that does the server issue a session cookie, and the response body has the same shape as a successful login.
   */
  async submitTOTP(code: string): Promise<TOTPLoginResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const res = await this.request<TOTPLoginResponse>('/api/v1/auth/totp', {
      method: 'POST',
      body: JSON.stringify({ code }),
    });
    if (res?.csrf_token) {
      this.csrfToken = res.csrf_token;
    }
    return res;
  }

  /**
   * SPA same-origin logout (POST /api/v1/auth/logout)
   */
  async logout(): Promise<LogoutResponse> {
    const res = await this.request<LogoutResponse>('/api/v1/auth/logout', {
      method: 'POST',
    });
    if (res?.csrf_token) {
      this.csrfToken = res.csrf_token;
    } else {
      this.csrfToken = null;
    }
    return res;
  }

  /**
   * SPA same-origin self-registration (POST /api/v1/auth/register).
   * Pre-session flow: writes use double-submit CSRF; when the token is missing, fetch a session token first.
   * Success does not establish a session (the server issues no session cookie); the caller then redirects to the login page.
   */
  async register(input: RegisterRequest): Promise<RegisterResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<RegisterResponse>('/api/v1/auth/register', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /**
   * SPA same-origin first-admin bootstrap (POST /api/v1/auth/setup).
   * Reachable only when there is no active admin, otherwise the server returns 404. Success does not establish a session.
   */
  async setup(input: SetupRequest): Promise<SetupResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<SetupResponse>('/api/v1/auth/setup', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Read all of the current user's scheduling presets and optimization thresholds (GET /api/v1/presets, session only). */
  async listPresets(): Promise<PresetsResponse> {
    return this.request<PresetsResponse>('/api/v1/presets');
  }

  /** Create a preset (POST /api/v1/presets); returns the full list on success. */
  async createPreset(input: PresetWriteRequest): Promise<PresetsResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<PresetsResponse>('/api/v1/presets', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }
  // ── Admin panel (Go: the files starting at internal/web/admin_api.go) ──────────
  // Everything goes through same-origin session cookies and authorization lives on the server (anonymous 401, non-admin 403); this layer only does transport.

  /** Read instance-level counts (GET /api/v1/admin/summary). */
  async getAdminSummary(): Promise<import('./types').AdminSummary> {
    return this.request<import('./types').AdminSummary>('/api/v1/admin/summary');
  }

  /** Read health page readings (GET /api/v1/admin/health). */
  async getAdminHealth(): Promise<import('./types').AdminHealth> {
    return this.request<import('./types').AdminHealth>('/api/v1/admin/health');
  }

  /** Search the audit log (GET /api/v1/admin/audit); filtering and pagination match the server-rendered SSR page. */
  async getAdminAudit(query?: import('./types').AdminAuditQuery): Promise<import('./types').AdminAuditResponse> {
    const params = new URLSearchParams();
    if (query) {
      for (const key of ['user', 'action', 'target_type', 'target_id', 'from', 'to'] as const) {
        const value = query[key];
        if (value !== undefined && value !== '') params.set(key, String(value));
      }
      if (query.page !== undefined) params.set('page', String(query.page));
    }
    const qs = params.toString();
    return this.request<import('./types').AdminAuditResponse>(`/api/v1/admin/audit${qs ? `?${qs}` : ''}`);
  }

  /** User management list (GET /api/v1/admin/users). */
  async getAdminUsers(params?: { q?: string; page?: number }): Promise<import('./types').AdminUsersResponse> {
    const search = new URLSearchParams();
    if (params?.q) search.set('q', params.q);
    if (params?.page !== undefined) search.set('page', String(params.page));
    const qs = search.toString();
    return this.request<import('./types').AdminUsersResponse>(`/api/v1/admin/users${qs ? `?${qs}` : ''}`);
  }

  /** Create a local account (POST /api/v1/admin/users); validation failures return a stable code. */
  async createAdminUser(input: import('./types').AdminUserCreateRequest): Promise<import('./types').AdminUser> {
    if (!this.csrfToken) await this.getSession();
    return this.request<import('./types').AdminUser>('/api/v1/admin/users', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }
  /**
   * Request a password reset (POST /api/v1/auth/forgot-password).
   * Pre-session flow: writes use double-submit CSRF; when the token is missing, fetch a session token first.
   * The response only contains site-level mail_ready and does not reveal whether the account exists; when mail is not configured the frontend renders an explanation instead of falsely claiming it was sent.
   */
  async requestPasswordReset(input: ForgotPasswordRequest): Promise<ForgotPasswordResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ForgotPasswordResponse>('/api/v1/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Edit a preset (PATCH /api/v1/presets/:id); returns the full list on success. */
  async updatePreset(id: string, input: PresetWriteRequest): Promise<PresetsResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encoded = encodeURIComponent(String(id));
    return this.request<PresetsResponse>(`/api/v1/presets/${encoded}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    });
  }

  /** Delete a preset (DELETE /api/v1/presets/:id); returns the full latest preset list on success. */
  /** Admin panel: email template inventory (kind + variable table + saved templates). */
  async getAdminMailTemplates(): Promise<AdminMailTemplatesResponse> {
    return this.request<AdminMailTemplatesResponse>('/api/v1/admin/mail-templates');
  }

  /** Admin panel: save one email template (full replacement; subject and body must come from the same edit). */
  async saveAdminMailTemplate(
    type: string,
    locale: string,
    input: { subject: string; body_md: string }
  ): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.request<{ saved: boolean }>(
      `/api/v1/admin/mail-templates/${encodeURIComponent(type)}/${encodeURIComponent(locale)}`,
      { method: 'PUT', body: JSON.stringify(input) }
    );
  }

  /** Admin panel: delete the custom template and fall back to the built-in body. */
  async deleteAdminMailTemplate(type: string, locale: string): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.request<{ deleted: boolean }>(
      `/api/v1/admin/mail-templates/${encodeURIComponent(type)}/${encodeURIComponent(locale)}`,
      { method: 'DELETE' }
    );
  }

  /** Admin panel: render a draft with sample values without writing to the DB or sending mail. */
  async previewAdminMailTemplate(input: {
    type: string;
    locale: string;
    subject: string;
    body_md: string;
  }): Promise<AdminMailTemplatePreview> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<AdminMailTemplatePreview>('/api/v1/admin/mail-templates/preview', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Admin panel: send the rendered result to yourself using sample values. */
  async testAdminMailTemplate(input: {
    type: string;
    locale: string;
    subject: string;
    body_md: string;
  }): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.request<{ queued: boolean }>('/api/v1/admin/mail-templates/test', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  async deletePreset(id: string): Promise<PresetsResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encoded = encodeURIComponent(String(id));
    return this.request<PresetsResponse>(`/api/v1/presets/${encoded}`, {
      method: 'DELETE',
    });
  }
  /** Enable / disable an account (POST /api/v1/admin/users/:id/status). */
  async setAdminUserStatus(id: string, action: 'enable' | 'disable'): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/users/${encodeURIComponent(String(id))}/status`, {
      method: 'POST',
      body: JSON.stringify({ action }),
    });
  }

  /** Change role (POST /api/v1/admin/users/:id/role); a dangerous action, the server requires confirm. */
  async setAdminUserRole(id: string, role: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/users/${encodeURIComponent(String(id))}/role`, {
      method: 'POST',
      body: JSON.stringify({ role, confirm: true }),
    });
  }

  /** Reset password (POST /api/v1/admin/users/:id/password); returns a temporary password once. */
  async resetAdminUserPassword(id: string): Promise<{ temp_password: string }> {
    if (!this.csrfToken) await this.getSession();
    return this.request<{ temp_password: string }>(`/api/v1/admin/users/${encodeURIComponent(String(id))}/password`, {
      method: 'POST',
      body: JSON.stringify({ confirm: true }),
    });
  }

  /** Force logout (POST /api/v1/admin/users/:id/logout). */
  async forceLogoutAdminUser(id: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/users/${encodeURIComponent(String(id))}/logout`, { method: 'POST', body: '{}' });
  }

  /** Delete a user (POST /api/v1/admin/users/:id/delete); a dangerous action, the server requires confirm. */
  async deleteAdminUser(id: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/users/${encodeURIComponent(String(id))}/delete`, {
      method: 'POST',
      body: JSON.stringify({ confirm: true }),
    });
  }

  /** Read registration policy, allowlist and invite list (GET /api/v1/admin/registration). */
  async getAdminRegistration(): Promise<import('./types').AdminRegistrationResponse> {
    return this.request<import('./types').AdminRegistrationResponse>('/api/v1/admin/registration');
  }

  /** Save registration policy and the email domain allowlist (POST /api/v1/admin/registration). */
  async saveAdminRegistration(input: import('./types').AdminRegistrationRequest): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>('/api/v1/admin/registration', { method: 'POST', body: JSON.stringify(input) });
  }

  /** Create an invite (POST /api/v1/admin/invites); returns the mail delivery result code. */
  async createAdminInvite(input: import('./types').AdminInviteCreateRequest): Promise<import('./types').AdminInviteCreateResponse> {
    if (!this.csrfToken) await this.getSession();
    return this.request<import('./types').AdminInviteCreateResponse>('/api/v1/admin/invites', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }
  /** Set a new password with a one-time token (POST /api/v1/auth/reset-password). */
  async resetPassword(input: ResetPasswordRequest): Promise<ResetPasswordResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ResetPasswordResponse>('/api/v1/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Trigger parameter optimization (POST /api/v1/presets/:id/optimize); 400 below threshold, 409 if already running. */
  async optimizePreset(id: string): Promise<PresetOptimizeResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encoded = encodeURIComponent(String(id));
    return this.request<PresetOptimizeResponse>(`/api/v1/presets/${encoded}/optimize`, {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }
  /** Consume an email verification token (POST /api/v1/auth/verify-email). */
  async verifyEmail(token: string): Promise<VerifyEmailResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<VerifyEmailResponse>('/api/v1/auth/verify-email', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
  }

  /** Consume an email-change confirmation token (POST /api/v1/auth/confirm-email-change). */
  async confirmEmailChange(token: string): Promise<ConfirmEmailChangeResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ConfirmEmailChangeResponse>('/api/v1/auth/confirm-email-change', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
  }

  /** Read the current email and verification state (GET /api/v1/settings/email, session only). */
  async getEmailSettings(): Promise<EmailSettingsResponse> {
    return this.request<EmailSettingsResponse>('/api/v1/settings/email');
  }

  /** Submit an email change request (POST /api/v1/settings/email); the address in the DB is unchanged until confirmation is sent. */
  async requestEmailChange(input: EmailChangeRequest): Promise<EmailChangeResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<EmailChangeResponse>('/api/v1/settings/email', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Resend the email verification message (POST /api/v1/settings/verify-email). */
  async resendVerification(): Promise<ResendVerificationResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ResendVerificationResponse>('/api/v1/settings/verify-email', {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }

  /** Poll optimization job status (GET /api/v1/presets/:id/optimize/status?job=:jobId). */
  async getPresetOptimizeStatus(
    id: string,
    jobId: string
  ): Promise<PresetOptimizeResponse> {
    const encoded = encodeURIComponent(String(id));
    const job = encodeURIComponent(String(jobId));
    return this.request<PresetOptimizeResponse>(
      `/api/v1/presets/${encoded}/optimize/status?job=${job}`
    );
  }

  /** One-click revert to default weights (POST /api/v1/presets/:id/optimize/revert); returns the full list on success. */
  async revertPresetWeights(id: string): Promise<PresetsResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encoded = encodeURIComponent(String(id));
    return this.request<PresetsResponse>(`/api/v1/presets/${encoded}/optimize/revert`, {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }
  /** Revoke an invite (POST /api/v1/admin/invites/:id/revoke). */
  async revokeAdminInvite(id: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/invites/${encodeURIComponent(String(id))}/revoke`, { method: 'POST', body: '{}' });
  }

  /** All-user API key overview (GET /api/v1/admin/api-keys). */
  async getAdminAPIKeys(params?: { page?: number }): Promise<import('./types').AdminAPIKeysResponse> {
    const qs = params?.page !== undefined ? `?page=${params.page}` : '';
    return this.request<import('./types').AdminAPIKeysResponse>(`/api/v1/admin/api-keys${qs}`);
  }

  /** Revoke any user's key (POST /api/v1/admin/api-keys/:id/revoke). */
  async revokeAdminAPIKey(id: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/api-keys/${encodeURIComponent(String(id))}/revoke`, { method: 'POST', body: '{}' });
  }

  /** Read system settings sections (GET /api/v1/admin/settings). */
  async getAdminSettings(): Promise<import('./types').AdminSettingsResponse> {
    return this.request<import('./types').AdminSettingsResponse>('/api/v1/admin/settings');
  }

  /** Save system settings (POST /api/v1/admin/settings); only non-empty values are submitted and empty strings are treated as "no change" by the server. */
  async saveAdminSettings(values: Record<string, string>): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>('/api/v1/admin/settings', { method: 'POST', body: JSON.stringify({ values }) });
  }

  /** Read SMTP config and outbox readings (GET /api/v1/admin/smtp). */
  async getAdminSMTP(): Promise<import('./types').AdminSMTPResponse> {
    return this.request<import('./types').AdminSMTPResponse>('/api/v1/admin/smtp');
  }

  /** Save SMTP config (POST /api/v1/admin/smtp); empty fields mean no change. */
  async saveAdminSMTP(input: import('./types').AdminSMTPRequest): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>('/api/v1/admin/smtp', { method: 'POST', body: JSON.stringify(input) });
  }

  /** Test the SMTP connection (POST /api/v1/admin/smtp/test); on failure message is the server's raw error text. */
  async testAdminSMTP(input: import('./types').AdminSMTPRequest): Promise<import('./types').AdminTestResult> {
    if (!this.csrfToken) await this.getSession();
    return this.request<import('./types').AdminTestResult>('/api/v1/admin/smtp/test', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Read OIDC config and bound identities (GET /api/v1/admin/oidc). */
  async getAdminOIDC(): Promise<import('./types').AdminOIDCResponse> {
    return this.request<import('./types').AdminOIDCResponse>('/api/v1/admin/oidc');
  }

  /** Save OIDC config (POST /api/v1/admin/oidc); empty string fields mean no change. */
  async saveAdminOIDC(input: import('./types').AdminOIDCRequest): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>('/api/v1/admin/oidc', { method: 'POST', body: JSON.stringify(input) });
  }

  /** Test the OIDC discovery document (POST /api/v1/admin/oidc/test); on failure message is the provider's raw error. */
  async testAdminOIDC(input: import('./types').AdminOIDCRequest): Promise<import('./types').AdminTestResult> {
    if (!this.csrfToken) await this.getSession();
    return this.request<import('./types').AdminTestResult>('/api/v1/admin/oidc/test', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Unlink an external identity (POST /api/v1/admin/oidc/identities/:id/unlink). */
  async unlinkAdminOIDCIdentity(id: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/oidc/identities/${encodeURIComponent(String(id))}/unlink`, { method: 'POST', body: '{}' });
  }

  /** Job list (GET /api/v1/admin/jobs). */
  async getAdminJobs(params?: { page?: number }): Promise<import('./types').AdminJobsResponse> {
    const qs = params?.page !== undefined ? `?page=${params.page}` : '';
    return this.request<import('./types').AdminJobsResponse>(`/api/v1/admin/jobs${qs}`);
  }

  /** Cancel a job (POST /api/v1/admin/jobs/:id/cancel). */
  async cancelAdminJob(id: string): Promise<void> {
    if (!this.csrfToken) await this.getSession();
    await this.request<void>(`/api/v1/admin/jobs/${encodeURIComponent(String(id))}/cancel`, { method: 'POST', body: '{}' });
  }

  /** Language pack completeness report (GET /api/v1/admin/i18n). */
  async getAdminI18n(): Promise<import('./types').AdminI18nResponse> {
    return this.request<import('./types').AdminI18nResponse>('/api/v1/admin/i18n');
  }
  /**
   * Probe whether the OIDC login entry is available (GET /api/v1/auth/oidc).
   * Read-only and callable before login; the response only contains enabled and a stable initiation URL, never issuer / secret.
   */
  async getOIDC(): Promise<OIDCInfo> {
    return this.request<OIDCInfo>('/api/v1/auth/oidc');
  }

  /**
   * Probe the current self-registration policy (GET /api/v1/auth/registration).
   * Read-only and callable before login; the registration page uses it to decide, before rendering the form, whether to state outright that "self-registration is unavailable".
   * The response only contains the policy value (open / invite / closed) and no credentials or email allowlist.
   */
  async registrationInfo(): Promise<RegistrationInfo> {
    return this.request<RegistrationInfo>('/api/v1/auth/registration');
  }

  /**
   * Read the optional mail kind named by an unsubscribe token (GET /api/v1/unsubscribe, no login required, does not consume the token).
   * When the token is invalid/used/expired the server returns a stable code (token_invalid / token_used / token_expired).
   */
  async readUnsubscribe(token: string): Promise<import('./types').UnsubscribeReadResponse> {
    return this.request<import('./types').UnsubscribeReadResponse>(
      `/api/v1/unsubscribe?token=${encodeURIComponent(token)}`
    );
  }

  /**
   * Read the deck sharing invites awaiting my acceptance, along with my accept policy and allowlist (GET /api/v1/sharing/invites).
   * A pending invite grants nothing: without clicking accept, someone else's deck never appears in my list or queue.
   */
  async getShareInvites(): Promise<import('./types').ShareInvitesResponse> {
    return this.request<import('./types').ShareInvitesResponse>('/api/v1/sharing/invites');
  }

  /** Accept a sharing invite (POST /api/v1/sharing/invites/:deckId/accept). Accepting is the step that writes authorization. */
  async acceptShareInvite(deckId: string): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.notifying('decks', this.request(`/api/v1/sharing/invites/${encodeURIComponent(String(deckId))}/accept`, {
      method: 'POST',
    }));
  }

  /** Reject a sharing invite (POST /api/v1/sharing/invites/:deckId/reject). Rejecting deletes the invite; authorization never existed. */
  async rejectShareInvite(deckId: string): Promise<void> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    await this.request(`/api/v1/sharing/invites/${encodeURIComponent(String(deckId))}/reject`, {
      method: 'POST',
    });
  }

  /**
   * Save my accept policy and allowlist additions/removals (PUT /api/v1/settings/share-policy).
   * An empty policy means only the allowlist changes; the response echoes the server's actual values and the UI trusts the response rather than local inference.
   */
  async saveSharePolicy(input: {
    policy?: import('./types').ShareAcceptPolicy;
    allow?: string[];
    revoke?: string[];
    // The UI only knows usernames; the server resolves by name (an unknown name fails the whole request with 400 user_not_found rather than being silently dropped).
    allow_usernames?: string[];
    revoke_usernames?: string[];
  }): Promise<import('./types').SharePolicyResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<import('./types').SharePolicyResponse>('/api/v1/settings/share-policy', {
      method: 'PUT',
      body: JSON.stringify(input),
    });
  }

  /**
   * Confirm unsubscribe (POST /api/v1/unsubscribe). Pre-session writes use double-submit CSRF; when the token is missing, fetch a session token first.
   * Consumes the token once and disables only the optional kind named by the token; the response returns the disabled kind.
   */
  async confirmUnsubscribe(token: string): Promise<import('./types').UnsubscribeConfirmResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<import('./types').UnsubscribeConfirmResponse>('/api/v1/unsubscribe', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
  }
}

/**
 * Default singleton API client instance
 */
export const apiClient = new ApiClient();

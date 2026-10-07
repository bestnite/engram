import {
  ApiClientError,
  type Deck,
  type DecksResponse,
  type DeckQueueCountsResponse,
  type DeckSettings,
  type UpdateDeckSettingsRequest,
  type CreateDeckRequest,
  type NotesResponse,
  type Note,
  type NoteListParams,
  type UpdateNoteRequest,
  type CreateNotesRequest,
  type CreateNotesResponse,
  type BulkNotesRequest,
  type BulkNotesResponse,
  type NotePreviewResponse,
  type MediaPickerPage,
  type MediaUploadResult,
  type StatsSummary,
  type StatsDetail,
  type DueCardsResponse,
  type DueCardsQuery,
  type SubmitSelfReviewRequest,
  type SubmitReviewResult,
  type SubmitGradedReviewRequest,
  type GradedReviewResult,
  type RevealAnswerResponse,
  type ReviewRenderResponse,
  type ReviewQueueResponse,
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
} from './types';

/**
 * API 客户端配置
 */
export interface ApiClientConfig {
  baseUrl?: string;
  fetch?: typeof fetch;
}

/**
 * 根据 HTTP 状态码推导稳定错误 code（DESIGN.md §7.3、§8.3）
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
 * 集中式类型安全同源 REST API 客户端
 * DESIGN.md §7.3、§8.3
 *
 * 核心设计决策：
 * 1. 严格使用 credentials: 'same-origin'（HttpOnly Cookie 会话）
 * 2. 严禁从存储中读取 API Key 或在请求头中发送 Authorization Bearer token
 * 3. 错误响应安全解析为结构化 ApiClientError，保持内部英文诊断，UI 由语言包映射
 * 4. 零外部 HTTP 框架依赖，基于原生 fetch
 */
export class ApiClient {
  private readonly baseUrl: string;
  private readonly fetchFn: typeof fetch;
  private csrfToken: string | null = null;

  constructor(config: ApiClientConfig = {}) {
    this.baseUrl = config.baseUrl || '';
    this.fetchFn =
      config.fetch ||
      (typeof fetch !== 'undefined' ? fetch.bind(globalThis) : (null as unknown as typeof fetch));
  }

  /**
   * 获取当前内存中持有的 CSRF Token（绝不存入 localStorage/sessionStorage）
   */
  getCsrfToken(): string | null {
    return this.csrfToken;
  }

  /**
   * 配置或更新客户端绑定的 CSRF Token（DESIGN.md §4.3）
   */
  setCsrfToken(token: string | null): void {
    this.csrfToken = token;
  }

  /**
   * 基础通用请求方法
   */
  async request<T>(path: string, init?: RequestInit): Promise<T> {
    const url = this.baseUrl ? `${this.baseUrl}${path}` : path;
    const headers = new Headers(init?.headers);

    if (!headers.has('Accept')) {
      headers.set('Accept', 'application/json');
    }

    // 严禁发送 Authorization 标头（DESIGN.md §8.3）
    headers.delete('Authorization');

    const method = (init?.method || 'GET').toUpperCase();
    const isSafeMethod = method === 'GET' || method === 'HEAD' || method === 'OPTIONS';

    // 变更请求自动附加 X-CSRF-Token 与 Content-Type: application/json
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
        // 强制使用同源会话 cookie，浏览器自动管理
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
        // 响应体非 JSON（如网关 502 HTML）时静默忽略，依赖状态码兜底
      }

      const code = errorEnvelope?.error?.code || inferErrorCodeFromStatus(res.status);
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

  /** 使用同源会话下载现有的卡组包附件。 */
  async downloadDeckPackage(deckId: number | string, options: { includeProgress?: boolean; includeMedia?: boolean; includeReviews?: boolean } = {}): Promise<{ blob: Blob; filename: string }> {
    if (options.includeReviews && !options.includeProgress) {
      throw new ApiClientError('Review history requires progress export', { status: 400, code: 'invalid_request' });
    }
    const params = new URLSearchParams();
    if (options.includeProgress) params.set('include_progress', '1');
    if (options.includeMedia === false) params.set('include_media', '0');
    if (options.includeReviews) params.set('include_reviews', '1');
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

  /** 通过同源会话 multipart 路由导入卡组包。 */
  async importDeckPackage(file: File, options: { target: string; dryRun?: boolean; onConflict?: 'skip' | 'update' | 'fail'; allowOthersProgress?: boolean; skipMissingMedia?: boolean }): Promise<PackageImportReport> {
    if (!this.csrfToken) await this.getSession();
    const form = new FormData();
    form.append('file', file, file.name);
    form.append('target', options.target);
    form.append('dry_run', options.dryRun ? '1' : '0');
    form.append('on_conflict', options.onConflict || 'update');
    form.append('allow_others_progress', options.allowOthersProgress ? '1' : '0');
    form.append('skip_missing_media', options.skipMissingMedia ? '1' : '0');
    return this.request<PackageImportReport>('/api/v1/decks/import', { method: 'POST', body: form });
  }

  /**
   * 获取当前用户的卡组列表（GET /api/v1/decks）
   * DESIGN.md §7.3
   */
  async getDecks(): Promise<DecksResponse> {
    return this.request<DecksResponse>('/api/v1/decks');
  }

  /** Fetch per-visible-deck queue counts generated by the shared schedule queue builder. */
  async getDeckQueueCounts(): Promise<DeckQueueCountsResponse> {
    return this.request<DeckQueueCountsResponse>('/api/v1/decks/queue-counts');
  }

  /**
   * 读取卡组每日上限与今日已用/剩余（GET /api/v1/decks/:id/settings，仅 owner，会话专用）。
   * 非 owner 由服务端返回 403/404，本方法不隐藏该失败。
   */
  async getDeckSettings(deckId: number | string): Promise<DeckSettings> {
    const id = encodeURIComponent(String(deckId));
    return this.request<DeckSettings>(`/api/v1/decks/${id}/settings`);
  }

  /**
   * 保存卡组每日上限（PATCH /api/v1/decks/:id/settings，仅 owner）。
   * 0 表示不限，必须原样提交；缺少会话令牌时先获取 CSRF token。
   */
  async updateDeckSettings(deckId: number | string, input: UpdateDeckSettingsRequest): Promise<DeckSettings> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.request<DeckSettings>(`/api/v1/decks/${id}/settings`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    });
  }

  /** 创建卡组；缺少会话令牌时先获取新 CSRF token。 */
  async createDeck(input: CreateDeckRequest): Promise<Deck> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<Deck>('/api/v1/decks', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /**
   * 克隆一个自己可读的卡组（POST /api/v1/decks/:id/clone，DESIGN.md §5）。
   * reader 及以上都能克隆；服务端判权与审计，返回新卡组的 {id, name}（进度不跟随）。
   * 显式声明 Accept: application/json，服务端据此返回 JSON 而不是 303 重定向。
   */
  async cloneDeck(deckId: number | string): Promise<{ id: number; name: string }> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const id = encodeURIComponent(String(deckId));
    return this.request<{ id: number; name: string }>(`/api/v1/decks/${id}/clone`, {
      method: 'POST',
      headers: { Accept: 'application/json' },
      body: JSON.stringify({}),
    });
  }

  /**
   * 获取卡组下的卡片列表（GET /api/v1/decks/:id/notes）
   * DESIGN.md §7.3、§8.1
   */
  async getDeckNotes(
    deckId: number | string,
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
      if (params.kind !== undefined && params.kind !== '') {
        searchParams.set('kind', params.kind);
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
   * 获取卡组卡片列表（getDeckNotes 别名）
   */
  async getNotes(
    deckId: number | string,
    params?: NoteListParams
  ): Promise<NotesResponse> {
    return this.getDeckNotes(deckId, params);
  }

  /** Update one existing note through the authenticated, CSRF-protected REST API. */
  async updateNote(noteId: number | string, input: UpdateNoteRequest): Promise<Note> {
    const id = encodeURIComponent(String(noteId));
    return this.request<Note>(`/api/v1/notes/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    });
  }

  /** Soft-delete one note through the authenticated, CSRF-protected REST API. */
  async deleteNote(noteId: number | string): Promise<{ deleted: boolean; id: number }> {
    const id = encodeURIComponent(String(noteId));
    return this.request<{ deleted: boolean; id: number }>(`/api/v1/notes/${id}`, {
      method: 'DELETE',
    });
  }

  /** 使用同源会话与内存 CSRF token 调用安全批量端点创建一条基础笔记。 */
  async createNotes(deckId: number | string, input: CreateNotesRequest): Promise<CreateNotesResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encodedId = encodeURIComponent(String(deckId));
    return this.request<CreateNotesResponse>(`/api/v1/decks/${encodedId}/notes`, {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /**
   * 批量删除/改标签（POST /api/v1/notes/bulk，DESIGN.md §7.3）。
   * 行级失败不回滚整批：响应 skipped 逐行给出 not_found / insufficient_role，
   * 调用方据 affected 与 skipped 报告真实结果，不做乐观假设。
   */
  async bulkNotes(input: BulkNotesRequest): Promise<BulkNotesResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<BulkNotesResponse>('/api/v1/notes/bulk', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** Preview fields using the authenticated session-only sanitized preview endpoint. */
  async previewNote(deckId: number | string, kind: string, fields: Record<string, unknown>): Promise<NotePreviewResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    const encodedId = encodeURIComponent(String(deckId));
    return this.request<NotePreviewResponse>(`/api/v1/decks/${encodedId}/notes/preview`, {
      method: 'POST',
      body: JSON.stringify({ kind, fields }),
    });
  }

  /** 复用已有的编辑器权限媒体片段；权限与可读媒体集合仍由服务端判定。 */
  async getMediaPickerPage(deckId: number | string, cursor = ''): Promise<MediaPickerPage> {
    const expectedPath = `/decks/${encodeURIComponent(String(deckId))}/media/picker`;
    const suffix = cursor ? `?cursor=${encodeURIComponent(cursor)}` : '';
    const headers = new Headers({ Accept: 'text/html' });
    headers.delete('Authorization');
    let response: Response;
    try {
      response = await this.fetchFn(`${expectedPath}${suffix}`, {
        method: 'GET', headers, credentials: 'same-origin',
      });
    } catch (err) {
      throw new ApiClientError('Network request failed', { status: 0, code: 'network_error', details: err });
    }
    if (!response.ok) {
      throw new ApiClientError(`HTTP ${response.status}`, {
        status: response.status, code: inferErrorCodeFromStatus(response.status),
      });
    }
    const html = await response.text();
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const root = doc.querySelector('#media-picker-list');
    if (!root) throw new ApiClientError('Invalid media picker response', { status: response.status, code: 'invalid_response' });
    const items = Array.from(root.querySelectorAll<HTMLButtonElement>('[data-media-insert]')).map((button) => {
      const sha256 = button.title;
      const path = button.getAttribute('data-media-insert');
      const image = button.querySelector('img');
      if (!/^[0-9a-f]{64}$/.test(sha256) || path !== `/media/${sha256}` || image?.getAttribute('src') !== path) {
        throw new ApiClientError('Invalid media picker item', { status: response.status, code: 'invalid_response' });
      }
      return { sha256, src: path, insert_url: path };
    });
    const next = root.querySelector<HTMLButtonElement>('button[hx-get]')?.getAttribute('hx-get') || '';
    let nextCursor = '';
    if (next) {
      const url = new URL(next, window.location.origin);
      if (url.origin !== window.location.origin || url.pathname !== expectedPath) {
        throw new ApiClientError('Invalid media picker pagination response', { status: response.status, code: 'invalid_response' });
      }
      nextCursor = url.searchParams.get('cursor') || '';
    }
    return { items, next_cursor: nextCursor };
  }

  /**
   * 通过卡组内编辑器上传入口（POST /decks/:id/media）上传一个媒体文件。
   * 端点要求 editor 及以上角色，由服务端判定，前端不隐藏失败；multipart 边界交由浏览器生成，
   * 因此 request 不会替 FormData 设置 Content-Type（仅字符串 body 才强制 application/json）。
   * 成功响应必须满足 /media/<sha256> 的哈希契约，否则按无效响应拒绝，避免把不受约束的
   * 字符串插入字段（DESIGN.md §6.3）。
   */
  async uploadDeckMedia(deckId: number | string, file: File): Promise<MediaUploadResult> {
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
   * 通过卡组列表查找单个卡组元数据
   */
  async getDeck(deckId: number | string): Promise<Deck | null> {
    const res = await this.getDecks();
    const idNum = Number(deckId);
    return res.decks.find((d) => d.id === idNum) || null;
  }

  /**
   * 获取当前用户基础资料（GET /api/v1/profile）
   * DESIGN.md §4.1、§8.3
   */
  async getProfile(): Promise<UserProfile> {
    const res = await this.request<ProfileResponse | UserProfile>('/api/v1/profile');
    if (res && typeof res === 'object' && 'profile' in res && res.profile) {
      return res.profile;
    }
    return res as UserProfile;
  }

  /**
   * 更新当前用户资料（PATCH /api/v1/profile）
   * DESIGN.md §4.1、§8.3
   * 严格使用同源凭据与 CSRF 标头
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
   * 仅切换用户界面语言偏好（PATCH /api/v1/settings/locale）
   * DESIGN.md §8.3
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
   * 获取当前用户学习统计概要（GET /api/v1/stats/summary）
   * DESIGN.md §7.3、§9
   */
  async getStatsSummary(): Promise<StatsSummary> {
    return this.request<StatsSummary>('/api/v1/stats/summary');
  }

  /**
   * 获取当前用户统计明细（GET /api/v1/stats/detail）
   * DESIGN.md §8.1、§9：与 SSR 统计页同源，返回 §9 全部指标的原始数值。
   */
  async getStatsDetail(): Promise<StatsDetail> {
    return this.request<StatsDetail>('/api/v1/stats/detail');
  }

  /**
   * 获取到期卡片列表（GET /api/v1/review/due）
   * DESIGN.md §3.3、§7.3:
   * deck 参数可重复传递多个卡组 ID（互斥/单/多），limit 取 [1, 500]
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
    if (query?.limit !== undefined) {
      params.set('limit', String(query.limit));
    }
    const queryString = params.toString();
    const path = queryString ? `/api/v1/review/due?${queryString}` : '/api/v1/review/due';
    return this.request<DueCardsResponse>(path);
  }

  /** 通过 Web 专用 CSRF 端点提交自评，并由服务端重建同范围队列。 */
  async submitSelfReview(input: SubmitSelfReviewRequest): Promise<SubmitReviewResult> {
    const { deck, ...review } = input;
    return this.request<SubmitReviewResult>('/api/v1/review/answer', {
      method: 'POST',
      body: JSON.stringify({ ...review, deck }),
    });
  }

  /**
   * 提交作答类题型的原始作答，由服务端判分并写入 reviews（DESIGN.md §6.2、§8.2）。
   * 客户端不提交档位；判分档位来自服务端 graderFor 与 preset 映射。
   */
  async submitGradedReview(input: SubmitGradedReviewRequest): Promise<GradedReviewResult> {
    return this.request<GradedReviewResult>('/api/v1/review/grade', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** 请求揭示作答类题型的正确答案（只读预览，不写进度）。 */
  async revealGradedAnswer(input: SubmitGradedReviewRequest): Promise<RevealAnswerResponse> {
    return this.request<RevealAnswerResponse>('/api/v1/review/grade', {
      method: 'POST',
      body: JSON.stringify({ ...input, action: 'reveal' }),
    });
  }

  /**
   * 取一张卡正反面的服务端清洗 HTML 与编辑地址（POST /api/v1/review/render）。
   * 复习页只把这里的 HTML 交给 {@html}，绝不把 fields 原文当 Markdown 渲染（DESIGN.md §6.1）。
   */
  async renderReviewCard(input: { card_id: number; deck?: number[] }): Promise<ReviewRenderResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ReviewRenderResponse>('/api/v1/review/render', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /**
   * 埋藏当前卡（POST /api/v1/review/bury）：只写本人进度，不产生 reviews 行。
   * 响应带同一范围重建后的队列，跨卡组复习不会退化成单卡组（DESIGN.md §8.2）。
   */
  async buryReview(input: { card_id: number; deck?: number[] }): Promise<ReviewQueueResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ReviewQueueResponse>('/api/v1/review/bury', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** 管理当前账号的 API keys；写操作沿用 request 自动注入的内存 CSRF token。 */
  async getAPIKeys(): Promise<APIKeysResponse> {
    return this.request<APIKeysResponse>('/api/v1/keys');
  }

  async createAPIKey(input: { name: string; scopes: string[] }): Promise<{ key: APIKeyRecord; plaintext: string }> {
    return this.request<{ key: APIKeyRecord; plaintext: string }>('/api/v1/keys', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  async deleteAPIKey(id: number): Promise<{ revoked: boolean; id: number }> {
    return this.request<{ revoked: boolean; id: number }>(`/api/v1/keys/${encodeURIComponent(String(id))}`, {
      method: 'DELETE',
    });
  }

  /**
   * 获取会话状态与安全 CSRF Token（GET /api/v1/auth/session）
   * DESIGN.md §4.3、§8.3
   */
  async changePassword(data: { old_password: string; new_password: string }): Promise<void> {
    await this.request<void>('/api/v1/settings/password', { method: 'PATCH', body: JSON.stringify(data) });
  }

  /** 读取当前用户的 TOTP 状态（GET /api/v1/settings/totp，仅会话）；不返回 secret。 */
  async getTOTPStatus(): Promise<TOTPStatus> {
    return this.request<TOTPStatus>('/api/v1/settings/totp');
  }

  /** 生成待确认的 secret（POST /api/v1/settings/totp/begin）；secret 只在此响应出现一次。 */
  async beginTOTP(): Promise<TOTPBeginResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPBeginResponse>('/api/v1/settings/totp/begin', {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }

  /** 用一次验证码确认绑定（POST /api/v1/settings/totp/confirm）；启用成功时一次性返回恢复码。 */
  async confirmTOTP(code: string): Promise<TOTPConfirmResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPConfirmResponse>('/api/v1/settings/totp/confirm', {
      method: 'POST',
      body: JSON.stringify({ code }),
    });
  }

  /** 关闭 TOTP（POST /api/v1/settings/totp/disable，需要密码）。 */
  async disableTOTP(password: string): Promise<TOTPDisableResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPDisableResponse>('/api/v1/settings/totp/disable', {
      method: 'POST',
      body: JSON.stringify({ password }),
    });
  }

  /** 重新生成恢复码（POST /api/v1/settings/totp/recovery，需要密码）；旧码立即作废。 */
  async regenerateTOTPRecovery(password: string): Promise<TOTPRecoveryResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<TOTPRecoveryResponse>('/api/v1/settings/totp/recovery', {
      method: 'POST',
      body: JSON.stringify({ password }),
    });
  }

  /** 读取邮件通知偏好（GET /api/v1/settings/notifications，仅会话）；开关由服务端按目录推导。 */
  async getNotificationPrefs(): Promise<NotificationPrefsResponse> {
    return this.request<NotificationPrefsResponse>('/api/v1/settings/notifications');
  }

  /** 保存邮件通知偏好（PATCH /api/v1/settings/notifications）；写操作需要会话 CSRF。 */
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
   * SPA 同源密码登录（POST /api/v1/auth/login）
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
   * 查询登录第二步（TOTP）是否可提交（GET /api/v1/auth/totp）。
   * 只读：不刷新凭据有效期，也不建立会话。
   */
  async getTOTPPending(): Promise<TOTPPendingResponse> {
    return this.request<TOTPPendingResponse>('/api/v1/auth/totp');
  }

  /**
   * 提交登录第二步的验证码或一次性恢复码（POST /api/v1/auth/totp）。
   * 会话前流程：写请求走双提交 CSRF，缺少 token 时先取一次会话 token。
   * 通过后才由服务端签发会话 cookie，返回体与登录成功同形。
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
   * SPA 同源登出（POST /api/v1/auth/logout）
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
   * SPA 同源自助注册（POST /api/v1/auth/register）。
   * 会话前流程：写请求走双提交 CSRF，缺少 token 时先取一次会话 token。
   * 成功不建立会话（服务端不签发会话 cookie），调用方随后跳转登录页。
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
   * SPA 同源首个管理员引导（POST /api/v1/auth/setup）。
   * 仅在没有活跃管理员时可达，否则服务端返回 404。成功不建立会话。
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

  /**
   * 请求密码重置（POST /api/v1/auth/forgot-password）。
   * 会话前流程：写请求走双提交 CSRF，缺少 token 时先取一次会话 token。
   * 响应只含站点级 mail_ready，不透露账号是否存在；未配置邮件时前端渲染说明而不是谎报已发送。
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

  /** 用一次性令牌设置新密码（POST /api/v1/auth/reset-password）。 */
  async resetPassword(input: ResetPasswordRequest): Promise<ResetPasswordResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ResetPasswordResponse>('/api/v1/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** 消费邮箱验证令牌（POST /api/v1/auth/verify-email）。 */
  async verifyEmail(token: string): Promise<VerifyEmailResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<VerifyEmailResponse>('/api/v1/auth/verify-email', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
  }

  /** 消费改邮箱确认令牌（POST /api/v1/auth/confirm-email-change）。 */
  async confirmEmailChange(token: string): Promise<ConfirmEmailChangeResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ConfirmEmailChangeResponse>('/api/v1/auth/confirm-email-change', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
  }

  /** 读取当前邮箱与验证状态（GET /api/v1/settings/email，仅会话）。 */
  async getEmailSettings(): Promise<EmailSettingsResponse> {
    return this.request<EmailSettingsResponse>('/api/v1/settings/email');
  }

  /** 提交改邮箱请求（POST /api/v1/settings/email）；确认邮件发出前库中地址不变。 */
  async requestEmailChange(input: EmailChangeRequest): Promise<EmailChangeResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<EmailChangeResponse>('/api/v1/settings/email', {
      method: 'POST',
      body: JSON.stringify(input),
    });
  }

  /** 重发邮箱验证邮件（POST /api/v1/settings/verify-email）。 */
  async resendVerification(): Promise<ResendVerificationResponse> {
    if (!this.csrfToken) {
      await this.getSession();
    }
    return this.request<ResendVerificationResponse>('/api/v1/settings/verify-email', {
      method: 'POST',
      body: JSON.stringify({}),
    });
  }
}

/**
 * 默认单例 API 客户端实例
 */
export const apiClient = new ApiClient();

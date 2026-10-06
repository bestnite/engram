import {
  ApiClientError,
  type Deck,
  type DecksResponse,
  type NotesResponse,
  type NoteListParams,
  type StatsSummary,
  type DueCardsResponse,
  type DueCardsQuery,
  type SubmitSelfReviewRequest,
  type SubmitReviewResult,
  type ApiErrorEnvelope,
  type UserProfile,
  type UpdateProfileRequest,
  type ProfileResponse,
  type SessionResponse,
  type LoginRequest,
  type LoginResponse,
  type LogoutResponse,
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

  /**
   * 获取当前用户的卡组列表（GET /api/v1/decks）
   * DESIGN.md §7.3
   */
  async getDecks(): Promise<DecksResponse> {
    return this.request<DecksResponse>('/api/v1/decks');
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
   * 获取会话状态与安全 CSRF Token（GET /api/v1/auth/session）
   * DESIGN.md §4.3、§8.3
   */
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
}

/**
 * 默认单例 API 客户端实例
 */
export const apiClient = new ApiClient();

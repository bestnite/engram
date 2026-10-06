import {
  ApiClientError,
  type DecksResponse,
  type ApiErrorEnvelope,
  type UserProfile,
  type UpdateProfileRequest,
  type ProfileResponse,
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
   * 配置或更新客户端绑定的 CSRF Token（DESIGN.md §4.3）
   */
  setCsrfToken(token: string | null): void {
    this.csrfToken = token;
  }

  /**
   * 读取当前已绑定的 CSRF Token
   */
  getCsrfToken(): string | null {
    return this.csrfToken;
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

    const method = (init?.method || 'GET').toUpperCase();
    if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
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
}

/**
 * 默认单例 API 客户端实例
 */
export const apiClient = new ApiClient();

import {
  ApiClientError,
  type DecksResponse,
  type ApiErrorEnvelope,
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
   * 设置当前内存中的 CSRF Token
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

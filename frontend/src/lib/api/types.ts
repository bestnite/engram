/**
 * 卡组数据结构（与 Go 后端 internal/api/decks.go:DeckResponse 对齐）
 * DESIGN.md §2.2、§7.3
 */
export interface Deck {
  id: number;
  name: string;
  description: string;
  visibility: string;
  new_per_day: number;
  reviews_per_day: number;
  preset_id: number;
  archived_at?: string | null;
  created_at: string;
}

/**
 * GET /api/v1/decks 响应体结构
 */
export interface DecksResponse {
  decks: Deck[];
}

/**
 * Go 后端错误体内层结构（internal/api/errors.go:errorBody）
 */
export interface ApiErrorDetail {
  code: string;
  message: string;
}

/**
 * 统一错误包壳（{"error": {"code": "...", "message": "..."}}）
 * DESIGN.md §7.3
 */
export interface ApiErrorEnvelope {
  error: ApiErrorDetail;
}

/**
 * API 客户端错误类
 * 封装 HTTP 状态码、稳定业务错误 code 与原始细节
 * AGENTS.md §2.1：面向开发者的内部 message 恒为英文，UI 文案由前端语言包映射
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

    // 维持原型链
    Object.setPrototypeOf(this, new.target.prototype);
  }

  /**
   * 是否为 401 未认证状态
   */
  get isUnauthorized(): boolean {
    return this.status === 401 || this.code === 'unauthorized';
  }

  /**
   * 是否为 403 权限不足或角色不够状态
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
   * 是否为 409 数据冲突或版本冲突状态
   */
  get isConflict(): boolean {
    return (
      this.status === 409 ||
      this.code === 'conflict' ||
      this.code === 'version_conflict'
    );
  }

  /**
   * 是否为 404 资源未找到
   */
  get isNotFound(): boolean {
    return this.status === 404 || this.code === 'not_found';
  }

  /**
   * 是否为 429 请求限流
   */
  get isRateLimited(): boolean {
    return this.status === 429 || this.code === 'rate_limited';
  }

  /**
   * 是否为网络层故障（如断网或 fetch 异常）
   */
  get isNetworkError(): boolean {
    return this.code === 'network_error';
  }
}

/**
 * 用户基础资料与设置结构（DESIGN.md §4.1、§8.3，Go: internal/store/models.go:User）
 */
export interface UserProfile {
  id?: number;
  username?: string;
  email?: string;
  display_name: string;
  locale: string;
  timezone: string;
  day_cutoff_hour: number | null;
}

/**
 * 更新个人资料请求体结构
 */
export interface UpdateProfileRequest {
  display_name: string;
  locale: string;
  timezone: string;
  day_cutoff_hour?: number | null;
}

/**
 * 切换语言请求体结构（DESIGN.md §8.3）
 */
export interface UpdateLocaleRequest {
  locale: string;
}

/**
 * 个人资料响应体结构
 */
export interface ProfileResponse {
  profile: UserProfile;
}

/**
 * 客户端表单校验结果结构
 */
export interface ProfileValidationResult {
  valid: boolean;
  errors: Partial<Record<'display_name' | 'locale' | 'timezone' | 'day_cutoff_hour', string>>;
  data?: UpdateProfileRequest;
}

/**
 * 会话与 CSRF 信息包结构
 */
export interface SessionInfo {
  authenticated: boolean;
  user?: UserProfile;
  csrf_token?: string;
}


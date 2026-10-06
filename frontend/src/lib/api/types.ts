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

/** GET /api/v1/decks/queue-counts response; counts are produced by the shared queue builder. */
export interface DeckQueueCountsResponse {
  decks: Array<{ deck_id: number; new_count: number; review_count: number }>;
}

/** POST /api/v1/decks 请求体与响应体。preset_id=0 使用服务端默认预设。 */
export interface CreateDeckRequest {
  name: string;
  description: string;
  visibility: 'private' | 'unlisted' | 'public';
  preset_id: number;
}

/**
 * 笔记数据结构（与 Go 后端 internal/api/notes.go:NoteJSON 对齐）
 * DESIGN.md §2.2、§6.2、§7.3
 */
export interface Note {
  id: number;
  deck_id: number;
  kind: string;
  fields: Record<string, unknown>;
  tags: string[];
  created_at: string;
  updated_at: string;
  external_ref: string;
}

/**
 * GET /api/v1/decks/:id/notes 响应体结构
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
  kind?: string;
  fields: Record<string, unknown>;
  tags?: string[];
}

/** POST /api/v1/decks/:id/notes/preview 响应；HTML 已由服务端清理。 */
export interface NotePreviewResponse {
  cards: Array<{ front_html: string; back_html: string }>;
}

/**
 * GET /api/v1/decks/:id/notes 查询参数
 * internal/store/note.go:NoteListOptions
 */
export interface NoteListParams {
  page?: number;
  per_page?: number;
  q?: string;
  tag?: string;
  kind?: string;
  status?: string;
}

/** 对齐 DESIGN.md §7.3 的批量写入契约；此界面只允许提交 basic 字段。 */
export interface CreateNotesRequest {
  notes: Array<{
    kind: 'basic';
    fields: { front: string; back: string };
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
 * 学习统计概要（与 Go 后端 internal/api/service.go:StatsSummary 对齐）
 * DESIGN.md §7.3、§9
 */
export interface StatsSummary {
  decks: number;
  due: number;
  reviews_today: number;
  reviews_total: number;
  retention: number;
  notes: number;
  cards: number;
}

/**
 * 到期卡片对外形态（与 Go 后端 internal/api/service.go:DueCard 对齐）
 * DESIGN.md §3.3、§7.3
 */
export interface DueCard {
  card_id: number;
  note_id: number;
  deck_id: number;
  state: string;
  due_at: string;
  retrievability: number;
  kind: string;
  fields: Record<string, unknown>;
  tags: string[];
  template?: string;
  version: number;
}

/**
 * GET /api/v1/review/due 响应体结构
 * DESIGN.md §3.3、§7.3
 */
export interface DueCardsResponse {
  cards: DueCard[];
}

export interface SubmitSelfReviewRequest {
  card_id: number;
  rating: number;
  expected_version: number;
  elapsed_ms?: number;
  deck?: number[];
}

export interface SubmitReviewResult {
  card_id: number;
  review_id: number;
  state: string;
  due_at: string | null;
  version: number;
  stability: number | null;
  cards: DueCard[];
  remaining: number;
}

/**
 * GET /api/v1/review/due 查询参数
 * DESIGN.md §3.3、§7.3:
 * deck 参数可重复传递多个卡组 ID（互斥/单/多），limit 取 [1, 500]
 */
export interface DueCardsQuery {
  deck?: number | number[];
  limit?: number;
}

/**
 * 用户信息结构（与 Go 后端 internal/web/spa_auth.go 对齐）
 * DESIGN.md §4.1、§8.3
 */
export interface User {
  id: number;
  username: string;
  email: string;
  display_name?: string;
  role: string;
  locale: string;
}

/**
 * GET /api/v1/auth/session 响应体结构
 * DESIGN.md §4.3、§8.3
 */
export interface SessionResponse {
  authenticated: boolean;
  user: User | null;
  csrf_token: string;
}

/**
 * POST /api/v1/auth/login 请求体结构
 */
export interface LoginRequest {
  username: string;
  password: string;
}

/**
 * POST /api/v1/auth/login 响应体结构
 */
export interface LoginResponse {
  authenticated?: boolean;
  user?: User | null;
  csrf_token?: string;
  requires_totp?: boolean;
}

/**
 * POST /api/v1/auth/logout 响应体结构
 */
export interface LogoutResponse {
  authenticated: boolean;
  csrf_token?: string;
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

  /**
   * 是否为 CSRF 校验失败
   */
  get isCsrfError(): boolean {
    return this.code === 'csrf_failed' || this.code === 'csrf_no_session';
  }

  /**
   * 是否为账号凭据错误
   */
  get isInvalidCredentials(): boolean {
    return this.code === 'invalid_credentials';
  }

  /**
   * 是否为账号被禁用
   */
  get isUserDisabled(): boolean {
    return this.code === 'user_disabled';
  }

  /**
   * 是否需要两步验证
   */
  get isTotpRequired(): boolean {
    return this.code === 'totp_required';
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

export interface APIKeyRecord {
  id: number;
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


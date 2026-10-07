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
 * 一条待接受的卡组共享邀请（DESIGN.md §4.4、§5 同意制；Go: internal/web/spa_share_invites.go）。
 *
 * 收件人视角读 deck_name + inviter_name（谁邀请我用哪个卡组），属主视角读 username
 * （我邀请了谁）。同一结构两种读法，字段按视角取用。
 */
export interface DeckShareInvite {
  deck_id: number;
  deck_name: string;
  user_id: number;
  username: string;
  role: 'reader' | 'editor' | 'owner';
  invited_by: number;
  inviter_name: string;
  created_at: string;
  expires_at: string;
}

/** 接收策略：谁可以把卡组分享给我。NULL/缺省按 anyone。 */
export type ShareAcceptPolicy = 'anyone' | 'whitelist' | 'nobody';

/** 白名单里的一行。带用户名是因为界面要显示「谁」，裸 id 不可读。 */
export interface ShareAllowRow {
  user_id: number;
  username: string;
}

/** GET /api/v1/sharing/invites（我的待接受邀请 + 我的接收策略）。 */
export interface ShareInvitesResponse {
  invites: DeckShareInvite[];
  policy: ShareAcceptPolicy;
  allow_list: ShareAllowRow[];
}

/** GET/PUT /api/v1/settings/share-policy 的响应。 */
export interface SharePolicyResponse {
  policy: ShareAcceptPolicy;
  allow_list: ShareAllowRow[];
}

/** GET /api/v1/decks/queue-counts response; counts are produced by the shared queue builder. */
export interface DeckQueueCountsResponse {
  decks: Array<{ deck_id: number; new_count: number; review_count: number }>;
}

/**
 * GET/PATCH /api/v1/decks/:id/settings（DESIGN.md §8.1、§3.3，Go: internal/web/spa_deck_settings.go）。
 * new_per_day / reviews_per_day 为 0 表示不限；new_unlimited / review_unlimited 显式表达
 * 「不限」，因为 0 与「今日剩余 0 张」在整数上同形。今日已用/剩余与复习队列同源。
 */
export interface DeckSettings {
  deck_id: number;
  deck_name: string;
  preset_id: number;
  new_per_day: number;
  reviews_per_day: number;
  new_used: number;
  review_used: number;
  new_left: number;
  review_left: number;
  new_unlimited: boolean;
  review_unlimited: boolean;
}

/**
 * PATCH /api/v1/decks/:id/settings 请求体；两个额度字段都必填，0 合法（不限）。
 * preset_id 可选：省略即不动预设（旧的只改额度调用保持原行为）。
 */
export interface UpdateDeckSettingsRequest {
  new_per_day: number;
  reviews_per_day: number;
  preset_id?: number;
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
 * GET /api/v1/media 的一项（与 Go internal/api.MediaItem 对齐，DESIGN.md §6.3、§7.3）。
 * url 由 sha256 拼成 /media/<sha256>，可直接用于 <img src> 与 Markdown 引用插入。
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

/** GET /api/v1/media 的一页；next_cursor 为空串表示已到底。 */
export interface MediaListResponse {
  items: MediaItem[];
  next_cursor: string;
}

/**
 * 编辑器上传成功响应（与 Go 后端 internal/web/media.go:storeUpload 对齐）。
 * 对外标识是 sha256，url 直接由哈希拼成 /media/<sha256>（DESIGN.md §6.3）。
 */
export interface MediaUploadResult {
  sha256: string;
  mime: string;
  bytes: number;
  url: string;
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
 * POST /api/v1/notes/bulk 请求体（DESIGN.md §7.3；权威 schema 是 schema/note-bulk.schema.json）。
 * action 的取值集合与 Go 侧 bulkAction* 常量逐项一致；note_ids 去重后 1..500；
 * tags 仅标签动作需要（1..20），delete 不得带 tags。
 */
export interface BulkNotesRequest {
  action: 'delete' | 'add_tags' | 'remove_tags' | 'set_tags';
  note_ids: number[];
  tags?: string[];
  dry_run?: boolean;
}

/** 批量动作里被逐行拒绝的 note；code 取值 not_found / insufficient_role。 */
export interface BulkNotesSkipped {
  note_id: number;
  code: string;
}

/**
 * POST /api/v1/notes/bulk 响应体。affected 只计库中状态确实变化的行，
 * 因此同一请求重复提交时第二次 affected=0；skipped 逐行给出拒绝原因，整批不回滚。
 */
export interface BulkNotesResponse {
  dry_run: boolean;
  affected: number;
  skipped: BulkNotesSkipped[];
}

/** POST /api/v1/decks/import report. */
export interface PackageImportReport {
  target: string;
  dry_run: boolean;
  deck_id?: number;
  notes_created: number;
  notes_updated: number;
  notes_skipped: number;
  cards_created: number;
  media_new: number;
  media_missing: number;
  progress_applied: number;
  progress_skipped: number;
  progress_discarded: boolean;
  match_rule?: string;
  errors: Array<{ entry: string; reason: string }>;
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
 * 统计明细（与 Go 后端 internal/web/spa_stats.go 的 spaStatsDetail 对齐）
 * DESIGN.md §9：与 SSR 统计页同源，字段返回原始计数/比例/毫秒，本地化与柱宽由前端负责。
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
  new_not_due: number;
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
  deck_id: number;
  name: string;
  due_count: number;
  reviews: number;
  retention: number;
  elapsed_ms: number;
}

export interface StatsTag {
  tag: string;
  reviews: number;
  retention: number;
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
  decks: StatsDeck[];
  tags: StatsTag[];
  grades: StatsGradeSource[];
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
 * 作答类题型提交的原始作答（DESIGN.md §6.2、§8.2）。
 * 评分由服务端判分器产生，客户端绝不提交档位。
 */
export type GradedAnswer = string | number | boolean | number[];

/**
 * POST /api/v1/review/grade 请求体。
 * action 省略时正常判分；'reveal' 只取清洗后的正确答案（不写库）；
 * 'give_up' 表示已揭示答案后放弃作答，按 Again 记一条自评。
 */
export interface SubmitGradedReviewRequest {
  card_id: number;
  expected_version?: number;
  elapsed_ms?: number;
  deck?: number[];
  action?: 'reveal' | 'give_up';
  answer?: GradedAnswer;
}

/**
 * 判分反馈：verdict 是判定，answer_html 是服务端清洗后的正确答案（唯一 HTML 汇）。
 */
export interface GradedFeedback {
  verdict: 'correct' | 'partial' | 'incorrect';
  score: number;
  rating: number;
  answer_html: string;
  given: string;
  parsed?: string;
}

/**
 * POST /api/v1/review/grade 响应体（与 Go 后端 internal/web/spa_review.go 对齐）。
 * 判分与放弃两条路径都返回新状态与同范围队列；reveal 走 RevealAnswerResponse。
 */
export interface GradedReviewResult {
  card_id: number;
  review_id: number;
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
 * POST /api/v1/review/grade（action=reveal）响应体：只返回清洗后的正确答案。
 */
export interface RevealAnswerResponse {
  revealed: boolean;
  card_id: number;
  answer_html: string;
}

/**
 * POST /api/v1/review/render 响应体（Go: internal/web/spa_review.go）。
 * front_html / back_html 已由服务端 goldmark + bluemonday 清洗，是复习页唯一的 HTML 汇
 * （DESIGN.md §6.1）；edit_href 指向卡片编辑页，供 `e` 快捷键跳转。
 */
export interface ReviewRenderResponse {
  card_id: number;
  front_html: string;
  back_html: string;
  edit_href: string;
}

/**
 * POST /api/v1/review/bury 响应体：埋藏只写本人进度，不产生 reviews 行；
 * cards / remaining 是同一范围重建后的队列（DESIGN.md §8.2）。
 */
export interface ReviewQueueResponse {
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
 * GET /api/v1/auth/totp 响应体结构：当前请求是否持有有效的登录第二步凭据。
 * 凭据只在第一因素（密码）通过后下发，因此 pending=true 不泄露任何账号是否启用 TOTP。
 */
export interface TOTPPendingResponse {
  pending: boolean;
}

/**
 * POST /api/v1/auth/totp 响应体结构：与登录成功同形（通过第二因素后才签发会话）。
 */
export interface TOTPLoginResponse {
  authenticated: boolean;
  user: User | null;
  csrf_token?: string;
}

/**
 * POST /api/v1/auth/logout 响应体结构
 */
export interface LogoutResponse {
  authenticated: boolean;
  csrf_token?: string;
}

/**
 * POST /api/v1/auth/register 请求体（DESIGN.md §4.2、§8.1）。
 * invite 为空时按站点注册策略判定；非空时走一次性邀请接受路径。
 */
export interface RegisterRequest {
  username: string;
  email: string;
  display_name?: string;
  password: string;
  invite?: string;
}

/** POST /api/v1/auth/register 响应体；注册成功不建立会话，前端随后跳转登录。 */
export interface RegisterResponse {
  created: boolean;
}

/**
 * POST /api/v1/auth/setup 请求体（DESIGN.md §4.1）。
 * email 为空且配置了 BOOTSTRAP_ADMIN_EMAIL 时由服务端兜底。
 */
export interface SetupRequest {
  username: string;
  email?: string;
  display_name?: string;
  password: string;
}

/** POST /api/v1/auth/setup 响应体；引导成功不建立会话。 */
export interface SetupResponse {
  created: boolean;
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

/**
 * TOTP 管理接口（DESIGN.md §4.3、§8.1，Go: internal/web/spa_totp.go）。
 *
 * GET 只报告状态，绝不返回 secret 或 otpauth；secret 只在 begin 响应里出现一次，
 * 恢复码明文只在 confirm / recovery 响应里出现一次。
 */
export interface TOTPStatus {
  enabled: boolean;
  pending: boolean;
  recovery_remaining: number;
}

/** POST /api/v1/settings/totp/begin 响应；secret 与 otpauth 链接只在此出现一次。 */
export interface TOTPBeginResponse {
  secret: string;
  otpauth_url: string;
  pending: boolean;
}

/** POST /api/v1/settings/totp/confirm 响应；启用成功时一次性返回恢复码。 */
export interface TOTPConfirmResponse {
  enabled: boolean;
  recovery_codes: string[];
  recovery_remaining: number;
}

/** POST /api/v1/settings/totp/disable 响应。 */
export interface TOTPDisableResponse {
  enabled: boolean;
}

/** POST /api/v1/settings/totp/recovery 响应；一次性返回新一批恢复码。 */
export interface TOTPRecoveryResponse {
  recovery_codes: string[];
  recovery_remaining: number;
}

/**
 * 邮件通知偏好接口（DESIGN.md §4.7、§8.1，Go: internal/web/spa_mail_prefs.go）。
 *
 * 分组与开关由服务端从 internal/mail 目录推导；前端只按稳定标识查自己的语言包，
 * 不另列一份类型清单。reminder_hour 为 null 表示站点默认，0–23 是显式小时（0 是合法午夜）。
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

/** GET /api/v1/settings/notifications 响应。 */
export interface NotificationPrefsResponse {
  groups: NotificationPrefGroup[];
  reminder_hour: number | null;
  default_reminder_hour: number;
  timezone: string;
}

/** PATCH /api/v1/settings/notifications 请求；choices 缺席的键按关闭处理（与复选框缺席一致）。 */
export interface UpdateNotificationPrefsRequest {
  choices: Record<string, boolean>;
  reminder_hour: number | null;
}

/**
 * 调度预设接口（DESIGN.md §3.5、§8.1，Go: internal/web/spa_presets.go）。
 *
 * 与 SSR 预设页同源：默认预设补齐、门槛、单并发入队、状态与回退全部复用同一批
 * store/jobs 方法。verdict 是服务端算出的三态枚举，前端据此查语言包，不复制判据。
 */
export interface PresetFitMetrics {
  log_loss: number;
  rmse: number;
  items: number;
}

/** 优化前后拟合结论的稳定枚举（M9-12）。 */
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

/** 优化作业状态；result 只在 succeeded 且报告可解析时非 null。 */
export interface PresetJob {
  id: number;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | string;
  stage: string | null;
  log_tail: string | null;
  error: string | null;
  result: PresetOptimizeResult | null;
}

/** 优化门槛：shortfall 只在 eligible 为 false 时有意义。 */
export interface OptimizeGate {
  reviews: number;
  min: number;
  shortfall: number;
  eligible: boolean;
}

export interface PresetRecord {
  id: number;
  name: string;
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
}

/** GET /api/v1/presets 与创建/编辑/回退共用的响应体。 */
export interface PresetsResponse {
  presets: PresetRecord[];
  gate: OptimizeGate;
}

/** 触发优化与轮询状态共用的响应体。 */
export interface PresetOptimizeResponse {
  job: PresetJob | null;
  gate: OptimizeGate;
}

/** 创建/编辑预设的请求体；enable_fuzz 必须显式给出，缺字段服务端拒绝。 */
export interface PresetWriteRequest {
  name: string;
  desired_retention: number;
  learning_steps: string;
  relearning_steps: string;
  maximum_interval_days: number;
  enable_fuzz: boolean;
}

/**
 * 管理面板接口（DESIGN.md §8.4，Go: internal/web/spa_admin_read.go）。
 * 响应只带原始值与稳定英文标识，任何本地化文案都由前端语言包按标识映射。
 */

/** GET /api/v1/admin/summary：实例级计数。 */
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

/** GET /api/v1/admin/health：健康页读数。schema_version / due 为 null 表示读不出来。 */
export interface AdminHealth {
  database: 'ok' | 'error';
  schema_version: number | null;
  media_bytes: number;
  media_truncated: boolean;
  due: number | null;
}

/** 审计行的操作者；null 表示系统动作（无 UserID）。 */
export interface AdminAuditActor {
  user_id: number | null;
  username: string;
}

/** 审计行的目标对象；null 表示无目标。 */
export interface AdminAuditTarget {
  type: string;
  id: number | null;
}

/** 一行审计记录；time 已按当前管理员时区格式化，detail 为原始 JSON 串（空串表示无详情）。 */
export interface AdminAuditRow {
  time: string;
  actor: AdminAuditActor | null;
  action: string;
  target: AdminAuditTarget | null;
  detail: string;
}

/** GET /api/v1/admin/audit 响应；notice 是稳定英文码（空串表示无提示）。 */
export interface AdminAuditResponse {
  rows: AdminAuditRow[];
  actions: string[];
  page: number;
  pages: number;
  total: number;
  notice: string;
}

/** 审计检索的过滤条件；空串/缺省表示不过滤该项。 */
export interface AdminAuditQuery {
  user?: string;
  action?: string;
  target_type?: string;
  target_id?: string;
  from?: string;
  to?: string;
  page?: number;
}

/** 用户管理（DESIGN.md §8.4，Go: internal/web/spa_admin_users.go）。 */

/** 用户列表里的一行；role/status 是存储取值，由前端映射文案。 */
export interface AdminUser {
  id: number;
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

/** GET /api/v1/admin/users 响应。 */
export interface AdminUsersResponse {
  users: AdminUser[];
  page: number;
  pages: number;
  total: number;
  query: string;
}

/** POST /api/v1/admin/users 请求。 */
export interface AdminUserCreateRequest {
  username: string;
  email: string;
  display_name: string;
  password: string;
  role: string;
}

/** 注册与邀请（Go: internal/web/spa_admin_registration.go）。 */

/** 一条邀请；role/status 是存储取值，link 供管理员复制。 */
export interface AdminInvite {
  id: number;
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

/** GET /api/v1/admin/registration 响应。 */
export interface AdminRegistrationResponse {
  policy: string;
  email_domains: string;
  invites: AdminInvite[];
}

/** POST /api/v1/admin/registration 请求。 */
export interface AdminRegistrationRequest {
  policy: string;
  email_domains: string;
}

/** POST /api/v1/admin/invites 请求；expires_days 为 null 或 0 表示不过期。 */
export interface AdminInviteCreateRequest {
  email: string;
  role: string;
  expires_days: number | null;
  send_email: boolean;
}

/** POST /api/v1/admin/invites 响应；mail_notice 是稳定英文码（空串表示未请求发信）。 */
export interface AdminInviteCreateResponse {
  invite: AdminInvite;
  mail_notice: string;
}

/** API Key 总览（Go: internal/web/spa_admin_keys.go）。 */

/** 一把 key 的元信息；时间已按管理员时区格式化，null 表示未设置。 */
export interface AdminAPIKey {
  id: number;
  user_id: number;
  owner: string;
  name: string;
  prefix: string;
  scopes: string[];
  last_used_at: string | null;
  expires_at: string | null;
  state: string;
}

/** GET /api/v1/admin/api-keys 响应。 */
export interface AdminAPIKeysResponse {
  keys: AdminAPIKey[];
  page: number;
  pages: number;
  total: number;
}

/** 系统设置 / SMTP / OIDC（Go: internal/web/spa_admin_{settings,smtp,oidc}.go）。 */

/** 设置表格里的一行；unit 非空时值按单位解释（当前只有 "bytes"）。 */
export interface AdminSettingRow {
  key: string;
  value: string;
  source: string;
  editable: boolean;
  sensitive: boolean;
  configured: boolean;
  unit?: string;
}

/** 一个设置分区（general / media / optimize / sensitive）。 */
export interface AdminSettingsSection {
  name: string;
  rows: AdminSettingRow[];
}

/** GET /api/v1/admin/settings 响应。 */
export interface AdminSettingsResponse {
  sections: AdminSettingsSection[];
}

/** outbox 读数。 */
export interface AdminOutbox {
  pending: number;
  failed: number;
  last_error: string;
  last_attempts: number;
}

/** GET /api/v1/admin/smtp 响应。 */
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

/** POST /api/v1/admin/smtp(/test) 请求；空字段表示沿用已保存值。 */
export interface AdminSMTPRequest {
  host?: string;
  port?: string;
  username?: string;
  from?: string;
  tls_mode?: string;
  password?: string;
}

/** 测试连接的响应；ok 为 true 时忽略 message。 */
export interface AdminTestResult {
  ok: boolean;
  code: string;
  message: string;
}

/** 一条已绑定身份。 */
export interface AdminOIDCIdentity {
  id: number;
  provider: string;
  subject: string;
  email: string;
  username: string;
  linked_at: string;
}

/** GET /api/v1/admin/oidc 响应。 */
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

/** POST /api/v1/admin/oidc(/test) 请求；空字符串字段表示不修改。 */
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

/** 作业与语言包报告（Go: internal/web/spa_admin_{jobs,i18n}.go）。 */

/** 一个作业的元信息；stage 为 null 表示尚未进入训练阶段。 */
export interface AdminJob {
  id: number;
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

/** GET /api/v1/admin/jobs 响应。 */
export interface AdminJobsResponse {
  jobs: AdminJob[];
  page: number;
  pages: number;
  total: number;
}

/** 一种语言的覆盖率。 */
export interface AdminLocaleCoverage {
  code: string;
  percent: number;
  present: number;
  total: number;
  complete: boolean;
  missing: string[];
}

/** GET /api/v1/admin/i18n 响应。 */
export interface AdminI18nResponse {
  locales: AdminLocaleCoverage[];
  all_complete: boolean;
}
// ---- 账号安全与邮件流程（DESIGN.md §4.3、§4.7、§8.1；Go: internal/web/spa_account.go）----

/** POST /api/v1/auth/forgot-password 请求。响应只含站点级邮件是否可用，绝不回显账号存在性。 */
export interface ForgotPasswordRequest {
  email: string;
}

export interface ForgotPasswordResponse {
  mail_ready: boolean;
}

/** POST /api/v1/auth/reset-password：一次性令牌 + 新密码。 */
export interface ResetPasswordRequest {
  token: string;
  password: string;
}

export interface ResetPasswordResponse {
  reset: boolean;
}

/** 只带一枚令牌的请求（邮箱验证、改邮箱确认）。 */
export interface TokenOnlyRequest {
  token: string;
}

export interface VerifyEmailResponse {
  verified: boolean;
}

export interface ConfirmEmailChangeResponse {
  changed: boolean;
}

/** GET /api/v1/settings/email（会话）：当前邮箱、验证状态与站点邮件是否可用。 */
export interface EmailSettingsResponse {
  email: string;
  email_verified: boolean;
  mail_ready: boolean;
}

/** POST /api/v1/settings/email（会话）：请求改邮箱，确认前库中地址不变。 */
export interface EmailChangeRequest {
  email: string;
}

export interface EmailChangeResponse {
  sent: boolean;
}

/** POST /api/v1/settings/verify-email（会话）：重发验证邮件。 */
export interface ResendVerificationResponse {
  sent: boolean;
}

// ---- 公开只读分享浏览（DESIGN.md §5、§6.1、§8.1；Go: internal/web/spa_share.go）----

/** 一条共享卡片的正反面，均为服务端清洗后的 HTML（客户端只做 {@html}，绝不再渲染 Markdown）。 */
export interface ShareNote {
  front_html: string;
  back_html: string;
}

/** GET /api/v1/share/:token 与 POST /api/v1/share/:token/unlock 的响应。 */
export interface ShareResponse {
  deck_name: string;
  /** 为真时 notes 恒为空：有口令的链接在解锁前不返回任何正文。 */
  password_required: boolean;
  notes: ShareNote[];
}

// ---- OIDC 登录入口探测（DESIGN.md §4.4、§8.1；Go: internal/web/spa_oidc.go）----

/** GET /api/v1/auth/oidc：入口是否可用与发起地址；不含任何凭据。 */
export interface OIDCInfo {
  enabled: boolean;
  start_url: string;
}

/**
 * GET /api/v1/unsubscribe?token=… 的响应（DESIGN.md §4.7、§8.1）。
 * type 是令牌指名的可选邮件类型；服务端只读取、不消费令牌。
 */
export interface UnsubscribeReadResponse {
  type: string;
}

/** POST /api/v1/unsubscribe 的响应：确认退订后回带被关掉的类型。 */
export interface UnsubscribeConfirmResponse {
  type: string;
}

/** 邮件模板（DESIGN.md §4.7）：每个邮件类型每种语言一份，缺失即回退内置正文。 */
export interface AdminMailTemplateVar {
  name: string;
  required: boolean;
  /** 变量用途的语言包键；界面按它本地化，不硬编码说明文字。 */
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
 * 某类型在某语言下的**内置正文**（DESIGN.md §4.7）。
 *
 * 与发信方兜底用的是同一段代码，只是代入占位符而不是真实值——所以编辑框预填它之后，
 * 「看到的默认」与「实际发出去的默认」不会是两份文本。
 */
export interface AdminMailTemplateDefault {
  type: string;
  locale: string;
  subject: string;
  body_md: string;
}

export interface AdminMailTemplatesResponse {
  locales: string[];
  /** 回退链第二级：没有自定义模板时按它取，界面据此说明。 */
  site_default_locale: string;
  types: AdminMailTemplateType[];
  rows: AdminMailTemplateRow[];
  /** 每个类型 × 每种语言的内置正文；编辑框据此预填，「恢复默认」后也回到它。 */
  defaults: AdminMailTemplateDefault[];
}

export interface AdminMailTemplatePreview {
  subject: string;
  text: string;
  html: string;
  variables: Record<string, string>;
}

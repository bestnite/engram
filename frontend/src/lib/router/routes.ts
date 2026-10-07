import type { RouteDefinition } from './types';
import HomeView from '../views/HomeView.svelte';
import DecksView from '../views/DecksView.svelte';
import DeckDetailView from '../views/DeckDetailView.svelte';
import StatsView from '../views/StatsView.svelte';
import SettingsView from '../views/SettingsView.svelte';
import APIKeysView from '../views/APIKeysView.svelte';
import LoginView from '../views/LoginView.svelte';
import TOTPLoginView from '../views/TOTPLoginView.svelte';
import RegisterView from '../views/RegisterView.svelte';
import SetupView from '../views/SetupView.svelte';
import ReviewView from '../views/ReviewView.svelte';
import NoteEditView from '../views/NoteEditView.svelte';
import NoteCreateView from '../views/NoteCreateView.svelte';
import ImportView from '../views/ImportView.svelte';
import DeckSharingView from '../views/DeckSharingView.svelte';
import DeckSettingsView from '../views/DeckSettingsView.svelte';
import TOTPView from '../views/TOTPView.svelte';
import NotificationPrefsView from '../views/NotificationPrefsView.svelte';
import PresetsView from '../views/PresetsView.svelte';
import AdminDashboardView from '../views/admin/AdminDashboardView.svelte';
import AdminHealthView from '../views/admin/AdminHealthView.svelte';
import AdminAuditView from '../views/admin/AdminAuditView.svelte';
import AdminUsersView from '../views/admin/AdminUsersView.svelte';
import AdminRegistrationView from '../views/admin/AdminRegistrationView.svelte';
import AdminAPIKeysView from '../views/admin/AdminAPIKeysView.svelte';
import AdminSettingsView from '../views/admin/AdminSettingsView.svelte';
import AdminSMTPView from '../views/admin/AdminSMTPView.svelte';
import AdminOIDCView from '../views/admin/AdminOIDCView.svelte';
import AdminJobsView from '../views/admin/AdminJobsView.svelte';
import AdminI18nView from '../views/admin/AdminI18nView.svelte';
// 邮件模板并入 AdminSMTPView（同一页），这里只留旧路径的搬家视图。
import AdminMailMovedView from '../views/admin/AdminMailMovedView.svelte';
import ForgotPasswordView from '../views/ForgotPasswordView.svelte';
import ResetPasswordView from '../views/ResetPasswordView.svelte';
import EmailSettingsView from '../views/EmailSettingsView.svelte';
import VerifyEmailView from '../views/VerifyEmailView.svelte';
import ConfirmEmailChangeView from '../views/ConfirmEmailChangeView.svelte';
import ShareBrowseView from '../views/ShareBrowseView.svelte';
import UnsubscribeView from '../views/UnsubscribeView.svelte';

/**
 * 前端骨架路由定义列表
 */
export const routes: RouteDefinition[] = [
  {
    path: '/import',
    name: 'import',
    component: ImportView as unknown as RouteDefinition['component'],
  },
  {
    path: '/',
    name: 'home',
    component: HomeView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks',
    name: 'decks',
    component: DecksView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/sharing',
    name: 'deck-sharing',
    component: DeckSharingView as unknown as RouteDefinition['component'],
  },
  {
    // 卡组每日上限设置（规范路径）。服务端 GET /decks/:id/settings 已切到
    // 应用壳（owner 门禁仍在服务端），读写走 /api/v1/decks/:id/settings。
    path: '/decks/:id/settings',
    name: 'deck-settings',
    component: DeckSettingsView as unknown as RouteDefinition['component'],
  },
  {
    // 迁移期的旧 SPA 地址，保留以兼容既有深链；新入口统一走 /decks/:id/settings。
    path: '/spa/decks/:id/settings',
    name: 'deck-settings-spa',
    component: DeckSettingsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id',
    name: 'deck-detail',
    component: DeckDetailView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes',
    name: 'deck-notes',
    component: DeckDetailView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes/:noteId/edit',
    name: 'note-edit',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes/new',
    name: 'note-create',
    component: NoteCreateView as unknown as RouteDefinition['component'],
  },
  {
    // 旧 SSR 编辑地址（规范路由）：服务端 GET /decks/:id/notes/:nid 现在返回
    // 应用壳，客户端必须能处理这个精确 URL，否则旧链接与刷新会落到 404 视图。
    // 参数名用 noteId，与 NoteEditView 读取的 $routeStore.params.noteId 保持一致。
    path: '/decks/:id/notes/:noteId',
    name: 'note-edit-nid',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    // 旧 SSR 新建地址：服务端 GET /decks/:id/new-note 返回应用壳，客户端处理该精确 URL。
    path: '/decks/:id/new-note',
    name: 'note-create-legacy',
    component: NoteCreateView as unknown as RouteDefinition['component'],
  },
  {
    // 复习页的规范路由。服务端 GET /review 已切到 SPA 应用壳，
    // 客户端必须能处理这个精确 URL，否则首页「开始复习」链接与刷新会落到 404 视图。
    path: '/review',
    name: 'review',
    component: ReviewView as unknown as RouteDefinition['component'],
  },
  {
    // 迁移期的旧 SPA 复习地址，保留以兼容既有深链；新入口统一走 /review。
    path: '/spa/review',
    name: 'review-spa',
    component: ReviewView as unknown as RouteDefinition['component'],
  },
  {
    path: '/stats',
    name: 'stats',
    component: StatsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/settings',
    name: 'settings',
    component: SettingsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/settings/keys',
    name: 'settings-keys',
    component: APIKeysView as unknown as RouteDefinition['component'],
  },
  {
    // 两步验证管理（规范路径）。服务端 GET /settings/totp 已切到应用壳，
    // 读写走 /api/v1/settings/totp*。
    path: '/settings/totp',
    name: 'totp-settings',
    component: TOTPView as unknown as RouteDefinition['component'],
  },
  {
    // 迁移期的旧 SPA 地址，保留以兼容既有深链；新入口统一走 /settings/totp。
    path: '/spa/settings/totp',
    name: 'totp-settings-spa',
    component: TOTPView as unknown as RouteDefinition['component'],
  },
  {
    // 邮件通知偏好（规范路径）。服务端 GET /settings/notifications 已切到
    // 应用壳，读写走 /api/v1/settings/notifications。
    path: '/settings/notifications',
    name: 'notification-settings',
    component: NotificationPrefsView as unknown as RouteDefinition['component'],
  },
  {
    // 迁移期的旧 SPA 地址，保留以兼容既有深链；新入口统一走 /settings/notifications。
    path: '/spa/settings/notifications',
    name: 'notification-settings-spa',
    component: NotificationPrefsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/login',
    name: 'login',
    component: LoginView as unknown as RouteDefinition['component'],
  },
  {
    // 登录入口的迁移期别名。规范路径 /login 由服务端 GET /login 应答
    // （spaLoginShell 返回应用壳并初始化会话前双提交 cookie），/spa/login 由同一处理器服务；
    // 登录协议走 /api/v1/auth/session + /api/v1/auth/login。
    path: '/spa/login',
    name: 'spa-login',
    component: LoginView as unknown as RouteDefinition['component'],
  },
  {
    // 登录第二步（TOTP）。服务端没有 /login/totp 的 GET 路由，规范路径由 NoRoute
    // 回退到应用壳（页面型 GET 一律回壳）。
    // 协议是 GET /api/v1/auth/totp（查询凭据状态）与 POST /api/v1/auth/totp（提交验证码）。
    path: '/login/totp',
    name: 'totp-login',
    component: TOTPLoginView as unknown as RouteDefinition['component'],
  },
  {
    // 迁移期的旧 SPA 第二步地址，保留以兼容既有深链；新入口统一走 /login/totp。
    path: '/spa/login/totp',
    name: 'totp-login-spa',
    component: TOTPLoginView as unknown as RouteDefinition['component'],
  },
  {
    // 注册入口的迁移期别名。规范路径 /register 由服务端 GET /register 应答（spaRegisterShell），
    // /spa/register 由同一处理器服务；
    // 注册协议走 POST /api/v1/auth/register，?invite= 由视图从 URL 读取。
    path: '/spa/register',
    name: 'spa-register',
    component: RegisterView as unknown as RouteDefinition['component'],
  },
  {
    // 首个管理员引导的迁移期别名。规范路径 /setup 由服务端 GET /setup 应答
    // （spaSetupShell 仅在没有活跃管理员时可达，否则 404），/spa/setup 由同一处理器服务；
    // 引导协议走 POST /api/v1/auth/setup。
    path: '/spa/setup',
    name: 'spa-setup',
    component: SetupView as unknown as RouteDefinition['component'],
  },
  {
    // 注册与首个管理员引导的规范路径。服务端 GET /register、/setup 由应用壳应答，
    // 写协议是 POST /api/v1/auth/register、POST /api/v1/auth/setup。
    path: '/register',
    name: 'register',
    component: RegisterView as unknown as RouteDefinition['component'],
  },
  {
    path: '/setup',
    name: 'setup',
    component: SetupView as unknown as RouteDefinition['component'],
  },
  {
    // 调度预设页（规范路径）。服务端 GET /presets 已切到应用壳，
    // 读写走 /api/v1/presets* 的会话 JSON 端点。
    path: '/presets',
    name: 'presets',
    component: PresetsView as unknown as RouteDefinition['component'],
  },
  {
    // 管理面板概览。服务端 GET /admin 已切到应用壳（requireAdmin 先于
    // 外壳），计数走 /api/v1/admin/summary。
    path: '/admin',
    name: 'admin-dashboard',
    component: AdminDashboardView as unknown as RouteDefinition['component'],
  },
  {
    // 管理面板健康页。服务端 GET /admin/health 已切到应用壳，读数走 /api/v1/admin/health。
    path: '/admin/health',
    name: 'admin-health',
    component: AdminHealthView as unknown as RouteDefinition['component'],
  },
  {
    // 管理面板审计检索。服务端 GET /admin/audit 已切到应用壳，检索走 /api/v1/admin/audit。
    path: '/admin/audit',
    name: 'admin-audit',
    component: AdminAuditView as unknown as RouteDefinition['component'],
  },
  {
    // 用户管理。服务端 GET /admin/users 已切到应用壳，读写走 /api/v1/admin/users*。
    path: '/admin/users',
    name: 'admin-users',
    component: AdminUsersView as unknown as RouteDefinition['component'],
  },
  {
    // 注册与邀请。服务端 GET /admin/registration 已切到应用壳，读写走 /api/v1/admin/(registration|invites)。
    path: '/admin/registration',
    name: 'admin-registration',
    component: AdminRegistrationView as unknown as RouteDefinition['component'],
  },
  {
    // API Key 总览。服务端 GET /admin/api-keys 已切到应用壳，读写走 /api/v1/admin/api-keys*。
    path: '/admin/api-keys',
    name: 'admin-api-keys',
    component: AdminAPIKeysView as unknown as RouteDefinition['component'],
  },
  {
    // 系统设置。服务端 GET /admin/settings 已切到应用壳，读写走 /api/v1/admin/settings。
    path: '/admin/settings',
    name: 'admin-settings',
    component: AdminSettingsView as unknown as RouteDefinition['component'],
  },
  {
    // 邮件（SMTP）。服务端 GET /admin/smtp 已切到应用壳，读写与测试走 /api/v1/admin/smtp*。
    path: '/admin/smtp',
    name: 'admin-smtp',
    component: AdminSMTPView as unknown as RouteDefinition['component'],
  },
  {
    // 身份与 OIDC。服务端 GET /admin/oidc 已切到应用壳，读写、测试与解绑走 /api/v1/admin/oidc*。
    path: '/admin/oidc',
    name: 'admin-oidc',
    component: AdminOIDCView as unknown as RouteDefinition['component'],
  },
  {
    // 作业。服务端 GET /admin/jobs 已切到应用壳，列表与取消走 /api/v1/admin/jobs*。
    path: '/admin/jobs',
    name: 'admin-jobs',
    component: AdminJobsView as unknown as RouteDefinition['component'],
  },
  {
    // 语言包完整度。服务端 GET /admin/i18n 已切到应用壳，报告走 /api/v1/admin/i18n。
    path: '/admin/i18n',
    name: 'admin-i18n',
    component: AdminI18nView as unknown as RouteDefinition['component'],
  },
{
    // 邮件模板已并入「邮件」页（上面那条）；这条只为让旧链接与书签不作废：
    // 它不渲染内容，只把地址换过去（读写仍走 /api/v1/admin/mail-templates*）。
    path: '/admin/mail-templates',
    name: 'admin-mail-templates-moved',
    component: AdminMailMovedView as unknown as RouteDefinition['component'],
  },
  {
    // 请求密码重置。服务端 GET /forgot-password 由应用壳应答。
    // 协议走 POST /api/v1/auth/forgot-password；响应只含站点级 mail_ready，不透露账号是否存在。
    path: '/forgot-password',
    name: 'forgot-password',
    component: ForgotPasswordView as unknown as RouteDefinition['component'],
  },
  {
    // 设置新密码。服务端 GET /reset-password 由应用壳应答；token 由邮件链接的查询串带入，
    // 提交走 POST /api/v1/auth/reset-password（一次性令牌，用掉即废）。
    path: '/reset-password',
    name: 'reset-password',
    component: ResetPasswordView as unknown as RouteDefinition['component'],
  },
  {
    // 账号与邮箱设置。服务端 GET /settings/email 已切到应用壳，未登录在服务端即重定向登录页。
    // 读取/改邮箱/重发验证走 /api/v1/settings/email 与 /api/v1/settings/verify-email。
    path: '/settings/email',
    name: 'email-settings',
    component: EmailSettingsView as unknown as RouteDefinition['component'],
  },
  {
    // 邮箱验证结果的入口别名。规范路径 /verify-email 由服务端 GET 返回应用壳并下发会话前
    // 双提交 cookie；令牌由前端从查询串读出，经 POST /api/v1/auth/verify-email 消费。
    path: '/spa/verify-email',
    name: 'spa-verify-email',
    component: VerifyEmailView as unknown as RouteDefinition['component'],
  },
  {
    // 改邮箱确认结果的入口别名，与 /verify-email 同构：规范路径 /confirm-email-change 由服务端
    // GET 返回应用壳，令牌经 POST /api/v1/auth/confirm-email-change 消费。
    path: '/spa/confirm-email-change',
    name: 'spa-confirm-email-change',
    component: ConfirmEmailChangeView as unknown as RouteDefinition['component'],
  },
  {
    // 公开只读分享浏览。服务端 GET /s/:token 已切到应用壳（无效/撤销链接仍在服务端 404），
    // 内容走 GET /api/v1/share/:token，口令解锁走 POST /api/v1/share/:token/unlock。
    path: '/s/:token',
    name: 'share-browse',
    component: ShareBrowseView as unknown as RouteDefinition['component'],
  },
  {
    // 邮箱验证结果的规范路径。服务端 GET /verify-email 返回应用壳，
    // 邮件里的一键链接就指向这里；令牌由视图读取 ?token= 后经 POST /api/v1/auth/verify-email 消费。
    // /spa/verify-email 保留为迁移期别名。
    path: '/verify-email',
    name: 'verify-email',
    component: VerifyEmailView as unknown as RouteDefinition['component'],
  },
  {
    // 改邮箱确认结果的规范路径。服务端 GET /confirm-email-change 返回应用壳，
    // 令牌经 POST /api/v1/auth/confirm-email-change 消费。/spa/confirm-email-change 保留为别名。
    path: '/confirm-email-change',
    name: 'confirm-email-change',
    component: ConfirmEmailChangeView as unknown as RouteDefinition['component'],
  },
  {
    // 一键退订的规范路径。服务端 GET /unsubscribe 返回应用壳并下发
    // 会话前双提交 cookie，令牌由视图读取 ?token= 后经 GET/POST /api/v1/unsubscribe 读取与确认。
    path: '/unsubscribe',
    name: 'unsubscribe',
    component: UnsubscribeView as unknown as RouteDefinition['component'],
  },
  {
    // 迁移期的旧 SPA 退订地址，保留以兼容既有深链；新入口统一走 /unsubscribe。
    path: '/spa/unsubscribe',
    name: 'unsubscribe-spa',
    component: UnsubscribeView as unknown as RouteDefinition['component'],
  },
];

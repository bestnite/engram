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
import ForgotPasswordView from '../views/ForgotPasswordView.svelte';
import ResetPasswordView from '../views/ResetPasswordView.svelte';
import EmailSettingsView from '../views/EmailSettingsView.svelte';
import VerifyEmailView from '../views/VerifyEmailView.svelte';
import ConfirmEmailChangeView from '../views/ConfirmEmailChangeView.svelte';

/**
 * 前端骨架路由定义列表（DESIGN.md §8.1）
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
    // 卡组每日上限设置（DESIGN.md §8.1 的规范路径）。服务端 GET /decks/:id/settings 已切到
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
    // 旧 SSR 编辑地址（DESIGN.md §8.1 的规范路由）：服务端 GET /decks/:id/notes/:nid 现在返回
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
    // 复习页的规范路由（DESIGN.md §8.1）。服务端 GET /review 已切到 SPA 应用壳，
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
    // 两步验证管理（DESIGN.md §8.1 的规范路径）。服务端 GET /settings/totp 已切到应用壳，
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
    // 邮件通知偏好（DESIGN.md §8.1 的规范路径）。服务端 GET /settings/notifications 已切到
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
    // SPA 登录入口。刻意走 /spa 前缀：SSR 仍占用 GET/POST /login，
    // 在浏览器端到端验证之前不遮蔽它（DESIGN.md §8.1 的迁移目标路径）。
    // 服务端 GET /spa/login 返回应用壳并初始化会话前双提交 cookie；
    // 登录协议仍走 /api/v1/auth/session + /api/v1/auth/login。
    path: '/spa/login',
    name: 'spa-login',
    component: LoginView as unknown as RouteDefinition['component'],
  },
  {
    // 登录第二步（TOTP）。服务端只注册 POST /login/totp（挑战页由第一步响应直接渲染），
    // 没有可遮蔽的 GET 页面，因此规范路径 /login/totp 由 NoRoute 回退到应用壳。
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
    // SPA 注册入口。刻意走 /spa 前缀：SSR 仍占用 GET/POST /register。
    // 服务端 GET /spa/register 返回应用壳并初始化会话前双提交 cookie；
    // 注册协议走 POST /api/v1/auth/register，?invite= 由视图从 URL 读取。
    path: '/spa/register',
    name: 'spa-register',
    component: RegisterView as unknown as RouteDefinition['component'],
  },
  {
    // SPA 首个管理员引导入口。刻意走 /spa 前缀：SSR 仍占用 GET/POST /setup。
    // 服务端 GET /spa/setup 仅在没有活跃管理员时返回应用壳（否则 404）；
    // 引导协议走 POST /api/v1/auth/setup。
    path: '/spa/setup',
    name: 'spa-setup',
    component: SetupView as unknown as RouteDefinition['component'],
  },
  {
    // 请求密码重置（DESIGN.md §8.1）。服务端 GET /forgot-password 已切到应用壳，SPA 缺失时回退 SSR。
    // 协议走 POST /api/v1/auth/forgot-password；响应只含站点级 mail_ready，不透露账号是否存在。
    path: '/forgot-password',
    name: 'forgot-password',
    component: ForgotPasswordView as unknown as RouteDefinition['component'],
  },
  {
    // 设置新密码。服务端 GET /reset-password 已切到应用壳；token 由邮件链接的查询串带入，
    // 提交走 POST /api/v1/auth/reset-password（一次性令牌语义与 SSR 相同）。
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
    // 邮箱验证结果的 SPA 入口。刻意走 /spa 前缀：免登录的一键链接 /verify-email 仍在服务端消费
    // 令牌并渲染结果（无脚本也能完成），SPA 客户端走此独立入口，协议 POST /api/v1/auth/verify-email。
    path: '/spa/verify-email',
    name: 'spa-verify-email',
    component: VerifyEmailView as unknown as RouteDefinition['component'],
  },
  {
    // 改邮箱确认结果的 SPA 入口。与 /spa/verify-email 同构：SSR 的免登录链接 /confirm-email-change
    // 保持服务端消费，SPA 走此入口，协议 POST /api/v1/auth/confirm-email-change。
    path: '/spa/confirm-email-change',
    name: 'spa-confirm-email-change',
    component: ConfirmEmailChangeView as unknown as RouteDefinition['component'],
  },
];

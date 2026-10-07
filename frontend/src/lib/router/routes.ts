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
    // 调度预设页（DESIGN.md §8.1 的规范路径）。服务端 GET /presets 已切到应用壳，
    // 读写走 /api/v1/presets* 的会话 JSON 端点。
    path: '/presets',
    name: 'presets',
    component: PresetsView as unknown as RouteDefinition['component'],
  },
];

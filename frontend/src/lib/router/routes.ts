import type { RouteDefinition } from './types';
import HomeView from '../views/HomeView.svelte';
import DecksView from '../views/DecksView.svelte';
import DeckDetailView from '../views/DeckDetailView.svelte';
import StatsView from '../views/StatsView.svelte';
import SettingsView from '../views/SettingsView.svelte';
import APIKeysView from '../views/APIKeysView.svelte';
import LoginView from '../views/LoginView.svelte';
import ReviewView from '../views/ReviewView.svelte';
import NoteEditView from '../views/NoteEditView.svelte';
import NoteCreateView from '../views/NoteCreateView.svelte';
import ImportView from '../views/ImportView.svelte';
import DeckSharingView from '../views/DeckSharingView.svelte';
import DeckSettingsView from '../views/DeckSettingsView.svelte';
import TOTPView from '../views/TOTPView.svelte';

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
    // 卡组每日上限设置。刻意走 /spa 前缀：SSR 仍占用 /decks/:id/settings，
    // 在浏览器端到端验证之前不遮蔽它（DESIGN.md §8.1 的迁移目标路径）。
    path: '/spa/decks/:id/settings',
    name: 'deck-settings',
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
    path: '/spa/review',
    name: 'review',
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
    // 两步验证管理。刻意走 /spa 前缀：SSR 仍占用 /settings/totp，
    // 在浏览器端到端验证之前不遮蔽它（DESIGN.md §8.1 的迁移目标路径）。
    path: '/spa/settings/totp',
    name: 'totp-settings',
    component: TOTPView as unknown as RouteDefinition['component'],
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
];

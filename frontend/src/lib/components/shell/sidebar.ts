import { writable } from 'svelte/store';
import type { ApiClient, DataChangeKind, Deck, DeckQueueCountsResponse } from '../../api';

/** 侧边栏收起状态的存储键。只是本机的显示偏好，不进账号设置。 */
export const SIDEBAR_COLLAPSED_KEY = 'engram-sidebar-collapsed';

/** 侧边栏里直接列出的卡组数上限；更多卡组从「全部卡组」进入。 */
export const SIDEBAR_DECK_LIMIT = 6;

/** 两次刷新卡组快捷入口之间的最短间隔：切页面会触发刷新，连续点击不该每次都打两个接口。 */
const REFRESH_INTERVAL_MS = 15_000;

function readCollapsed(): boolean {
  try {
    return typeof window !== 'undefined' && window.localStorage?.getItem(SIDEBAR_COLLAPSED_KEY) === '1';
  } catch {
    // 隐私模式下访问 localStorage 本身就可能抛错；按展开处理。
    return false;
  }
}

export const sidebarCollapsed = writable<boolean>(readCollapsed());

export function setSidebarCollapsed(collapsed: boolean): void {
  sidebarCollapsed.set(collapsed);
  try {
    window.localStorage?.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? '1' : '0');
  } catch {
    // 存储不可用时本次会话仍然生效，只是不记住。
  }
}

export interface SidebarDeck {
  id: string;
  name: string;
  /** 今日还能复习的卡数（新卡 + 到期复习）；队列计数取不到时为 null。 */
  due: number | null;
}

export interface SidebarDecksState {
  decks: SidebarDeck[];
  /** 全部卡组今日待复习总数；队列计数取不到时为 null，界面不显示数字。 */
  totalDue: number | null;
}

export const sidebarDecks = writable<SidebarDecksState>({ decks: [], totalDue: null });

/**
 * 由卡组列表与队列计数算出侧边栏要显示的卡组：待复习多的排前面，同数按名称排，
 * 只取前 SIDEBAR_DECK_LIMIT 个。已归档的卡组不进侧边栏。
 */
export function buildSidebarDecks(decks: Deck[], counts: DeckQueueCountsResponse | null): SidebarDecksState {
  const dueById = new Map<string, number>();
  for (const c of counts?.decks ?? []) {
    dueById.set(c.deck_id, c.new_count + c.review_count);
  }
  const visible = decks
    .filter((d) => !d.archived_at)
    .map((d) => ({ id: d.id, name: d.name, due: counts ? (dueById.get(d.id) ?? 0) : null }));
  visible.sort((a, b) => (b.due ?? 0) - (a.due ?? 0) || a.name.localeCompare(b.name));
  const totalDue = counts ? visible.reduce((sum, d) => sum + (d.due ?? 0), 0) : null;
  return { decks: visible.slice(0, SIDEBAR_DECK_LIMIT), totalDue };
}

let lastLoadedAt = 0;
let inflight: Promise<void> | null = null;
// 强制刷新撞上进行中的请求时置位：那个请求可能发于改动之前，结束后必须再取一次。
let refreshAgain = false;

/**
 * 刷新侧边栏的卡组快捷入口。force 为假时 15 秒内的重复调用直接返回。
 *
 * 失败只影响侧边栏这一处：保留上一次的结果，不抛给页面——导航栏取数失败不应该打断用户正在看的页面。
 * 队列计数单独失败时仍显示卡组名，只是不带数字。
 */
export function refreshSidebarDecks(client: ApiClient, force = false): Promise<void> {
  const now = Date.now();
  if (inflight) {
    if (force) refreshAgain = true;
    return inflight;
  }
  if (!force && now - lastLoadedAt < REFRESH_INTERVAL_MS) return Promise.resolve();
  lastLoadedAt = now;
  inflight = (async () => {
    try {
      const [decksRes, counts] = await Promise.all([
        client.getDecks(),
        client.getDeckQueueCounts().catch(() => null),
      ]);
      sidebarDecks.set(buildSidebarDecks(decksRes.decks, counts));
    } catch {
      lastLoadedAt = 0;
    } finally {
      inflight = null;
      if (refreshAgain) {
        refreshAgain = false;
        void refreshSidebarDecks(client, true);
      }
    }
  })();
  return inflight;
}

/**
 * 标记侧边栏数据已过期但不立即重取：下一次切页时会绕过节流重新取数。
 * 用于复习作答这类高频改动——每答一张卡都重取会多打两个请求。
 */
export function markSidebarStale(): void {
  lastLoadedAt = 0;
}

/**
 * 按接口层的改动通知同步侧边栏（ApiClient.onDataChanged 的订阅方）：
 * 卡组增删改、卡片与额度变化立即重取；复习作答只标记过期。
 */
export function handleDataChanged(client: ApiClient, kind: DataChangeKind): void {
  if (kind === 'review') {
    markSidebarStale();
    return;
  }
  void refreshSidebarDecks(client, true);
}

/** 退出登录后清空，避免下一个登录的人短暂看到上一个人的卡组。 */
export function resetSidebarDecks(): void {
  lastLoadedAt = 0;
  sidebarDecks.set({ decks: [], totalDue: null });
}

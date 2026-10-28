import { writable } from 'svelte/store';
import type { ApiClient, DataChangeKind, Deck, DeckQueueCountsResponse } from '../../api';

/** Storage key for the sidebar's collapsed state. It is only a local display preference and does not go into account settings. */
const SIDEBAR_COLLAPSED_KEY = 'engram-sidebar-collapsed';

/** Maximum number of decks listed directly in the sidebar; more decks are reached through "all decks". */
export const SIDEBAR_DECK_LIMIT = 6;

/** Minimum interval between two refreshes of the deck shortcuts: changing pages triggers a refresh, and rapid clicks should not hit two endpoints every time. */
const REFRESH_INTERVAL_MS = 15_000;

function readCollapsed(): boolean {
  try {
    return typeof window !== 'undefined' && window.localStorage?.getItem(SIDEBAR_COLLAPSED_KEY) === '1';
  } catch {
    // Accessing localStorage itself can throw in private mode; treat it as expanded.
    return false;
  }
}

export const sidebarCollapsed = writable<boolean>(readCollapsed());

export function setSidebarCollapsed(collapsed: boolean): void {
  sidebarCollapsed.set(collapsed);
  try {
    window.localStorage?.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? '1' : '0');
  } catch {
    // When storage is unavailable the setting still applies for this session, it is just not remembered.
  }
}

export interface SidebarDeck {
  id: string;
  name: string;
  /** How many cards can still be reviewed today (new cards + due reviews); null when the queue counts are unavailable. */
  due: number | null;
}

export interface SidebarDecksState {
  decks: SidebarDeck[];
  /** Total due count today across all decks; null when the queue counts are unavailable, and the UI then shows no number. */
  totalDue: number | null;
}

export const sidebarDecks = writable<SidebarDecksState>({ decks: [], totalDue: null });

/**
 * Compute the decks to show in the sidebar from the deck list and the queue counts: more due cards first, ties sorted by name,
 * and only the first SIDEBAR_DECK_LIMIT are taken. The decks the user paused never enter the sidebar, so the total
 * due count matches the default review queue.
 */
export function buildSidebarDecks(decks: Deck[], counts: DeckQueueCountsResponse | null): SidebarDecksState {
  const dueById = new Map<string, number>();
  for (const c of counts?.decks ?? []) {
    dueById.set(c.deck_id, c.new_count + c.review_count);
  }
  const visible = decks
    .filter((d) => !d.paused)
    .map((d) => ({ id: d.id, name: d.name, due: counts ? (dueById.get(d.id) ?? 0) : null }));
  visible.sort((a, b) => (b.due ?? 0) - (a.due ?? 0) || a.name.localeCompare(b.name));
  const totalDue = counts ? visible.reduce((sum, d) => sum + (d.due ?? 0), 0) : null;
  return { decks: visible.slice(0, SIDEBAR_DECK_LIMIT), totalDue };
}

let lastLoadedAt = 0;
let inflight: Promise<void> | null = null;
// Set when a forced refresh collides with an in-flight request: that request may have started before the change, so another fetch is required once it finishes.
let refreshAgain = false;

/**
 * Refresh the sidebar's deck shortcuts. When force is false, repeated calls within 15 seconds return immediately.
 *
 * Failure only affects this one place in the sidebar: the previous result is kept and nothing is thrown to the page - a failed nav fetch should not interrupt the page the user is viewing.
 * When the queue counts fail on their own the deck names are still shown, just without numbers.
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
 * Mark the sidebar data stale without refetching immediately: the next page change bypasses the throttle and refetches.
 * Used for high-frequency changes such as answering reviews - refetching on every card answer would add two requests each time.
 */
function markSidebarStale(): void {
  lastLoadedAt = 0;
}

/**
 * Sync the sidebar from the API layer's change notifications (subscribers of ApiClient.onDataChanged):
 * deck create/rename/delete, card and quota changes refetch immediately; review answers only mark it stale.
 */
export function handleDataChanged(client: ApiClient, kind: DataChangeKind): void {
  if (kind === 'review') {
    markSidebarStale();
    return;
  }
  void refreshSidebarDecks(client, true);
}

/** Cleared on logout, so the next person to log in does not briefly see the previous user's decks. */
export function resetSidebarDecks(): void {
  lastLoadedAt = 0;
  sidebarDecks.set({ decks: [], totalDue: null });
}

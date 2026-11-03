import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import type { ApiClient, Deck, User } from '../lib/api';
import { apiClient } from '../lib/api';
import { authStore, clearSession, login } from '../lib/auth';
import { refreshSidebarDecks, resetSidebarDecks, sidebarDecks } from '../lib/components/shell/sidebar';

/**
 * The sidebar is a module-level cache shared by every account in one tab. A 401 followed by
 * another login must not let the first account's deck names survive, and a request still in
 * flight when the session changes must not write its (now foreign) result back.
 */

function deck(id: string, name: string): Deck {
  return {
    id,
    name,
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: '1',
    created_at: '2026-01-01T00:00:00Z',
    role: 'owner',
  };
}

function stubClient(decks: Deck[]) {
  return {
    getDecks: vi.fn(async () => ({ decks })),
    getDeckQueueCounts: vi.fn(async () => ({ decks: [] })),
  } as unknown as ApiClient;
}

function signedIn(id: string, username: string): void {
  const user: User = { id, username, email: `${username}@example.com`, role: 'user', locale: 'en', timezone: 'UTC', has_password: true };
  authStore.set({ initialized: true, loading: false, authenticated: true, user, error: null });
}

function names(): string[] {
  return get(sidebarDecks).decks.map((d) => d.name);
}

describe('sidebar session isolation', () => {
  beforeEach(() => {
    resetSidebarDecks();
    signedIn('1', 'alice');
  });

  afterEach(() => {
    vi.restoreAllMocks();
    clearSession();
  });

  it('shows only the second account after a 401 session switch', async () => {
    const alice = stubClient([deck('a1', 'A private deck')]);
    await refreshSidebarDecks(alice, true);
    expect(names()).toEqual(['A private deck']);

    // The session dies (401): the shell calls clearSession before showing the login page.
    clearSession();
    expect(names()).toEqual([]);

    signedIn('2', 'bob');
    const bob = stubClient([deck('b1', 'B deck')]);
    // No force: switching accounts must have cleared the throttle as well.
    await refreshSidebarDecks(bob);
    expect(bob.getDecks).toHaveBeenCalledTimes(1);
    expect(names()).toEqual(['B deck']);
  });

  it('clears the previous list when a new login succeeds', async () => {
    await refreshSidebarDecks(stubClient([deck('a1', 'A private deck')]), true);
    expect(names()).toEqual(['A private deck']);

    vi.spyOn(apiClient, 'login').mockResolvedValue({
      authenticated: true,
      user: { id: '2', username: 'bob', email: 'bob@example.com', role: 'user', locale: 'en', timezone: 'UTC', has_password: true },
    });
    await login('bob', 'secret');
    expect(names()).toEqual([]);
  });

  it('drops an in-flight refresh that belongs to the previous session', async () => {
    let releaseAlice!: () => void;
    const alice = {
      getDecks: vi.fn(
        () =>
          new Promise((resolve) => {
            releaseAlice = () => resolve({ decks: [deck('a1', 'A private deck')] });
          })
      ),
      getDeckQueueCounts: vi.fn(async () => ({ decks: [] })),
    } as unknown as ApiClient;

    const pending = refreshSidebarDecks(alice, true);
    clearSession();
    signedIn('2', 'bob');
    const bob = stubClient([deck('b1', 'B deck')]);
    const bobPending = refreshSidebarDecks(bob, true);
    // The switch already invalidated Alice's request, so Bob's refresh must not join it.
    expect(bob.getDecks).toHaveBeenCalledTimes(1);

    releaseAlice();
    await pending;
    await bobPending;
    expect(names()).toEqual(['B deck']);
  });
});

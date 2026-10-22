/**
 * Frontend entry point for card kind metadata (GET /api/v1/card-types).
 *
 * The server's card-kind self-description is the only source of field tables and answer controls: adding a card kind means changing only Go, and the frontend keeps no second
 * inventory. This wraps the response into a catalog and exposes accessors for fields/front-back/answer control by kind.
 *
 * Loading is async and idempotent: concurrent calls share a single promise, and on success the store is written and cached; on failure
 * inflight is cleared so it can be retried, and the store is not written. When the catalog is null views render loading/empty states and never print the machine kind word
 * or a bare language pack key as copy.
 */
import { get, writable } from 'svelte/store';
import type { ApiClient } from './api/client';
import type {
  CardTypeAnswerControl,
  CardTypeDescription,
  CardTypesResponse,
} from './api/types';
import type { FieldSpec } from './card-fields';

export type { CardTypeDescription };

/** Local view of the card-kind self-description: kinds keeps the server order (lexicographic by kind) and byKind enables O(1) lookup. */
export interface CardTypeCatalog {
  readonly kinds: readonly CardTypeDescription[];
  readonly byKind: ReadonlyMap<string, CardTypeDescription>;
}

/** Build a catalog from a batch of self-descriptions; a duplicate kind takes the later one (the server never sends duplicate kinds). */
export function createCatalog(kinds: readonly CardTypeDescription[]): CardTypeCatalog {
  const byKind = new Map<string, CardTypeDescription>();
  for (const description of kinds) {
    byKind.set(description.kind, description);
  }
  return { kinds: [...kinds], byKind };
}

/**
 * Card kind metadata store. null means not ready yet (loading or the fetch failed) - views render loading/empty states accordingly.
 */
export const cardTypes = writable<CardTypeCatalog | null>(null);

let inflight: Promise<CardTypeCatalog> | null = null;

/**
 * Idempotently load the card-kind self-description: concurrent calls share one promise, and on success the store is written and cached;
 * on failure inflight is cleared for the next retry and the store is not written (it stays null so views take the fallback path).
 */
export function loadCardTypes(client: ApiClient): Promise<CardTypeCatalog> {
  const cached = get(cardTypes);
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = client
      .getCardTypes()
      .then((response: CardTypesResponse) => {
        const catalog = createCatalog(response.kinds ?? []);
        cardTypes.set(catalog);
        return catalog;
      })
      .catch((cause) => {
        inflight = null;
        throw cause;
      });
  }
  return inflight;
}

/** Inject a catalog directly (for tests or shell prefetching); the production path only uses loadCardTypes. */
export function setCardTypes(catalog: CardTypeCatalog | null): void {
  inflight = null;
  cardTypes.set(catalog);
}

/** Current snapshot; returns null when not ready. */
export function currentCatalog(): CardTypeCatalog | null {
  return get(cardTypes);
}

/** Card kind list (for dropdowns/filters), in the same order as the server. */
export function kindOrder(catalog: CardTypeCatalog | null): string[] {
  return catalog ? catalog.kinds.map((description) => description.kind) : [];
}

/** Self-description of a card kind; returns null for an unknown kind or when the catalog is not ready. */
export function descriptionOf(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): CardTypeDescription | null {
  if (!catalog || !kind) return null;
  return catalog.byKind.get(kind) ?? null;
}

/**
 * A card kind's field table. The server has already appended the common optional fields (source_url / extra) at the end of every kind's fields,
 * so they are returned as-is here with no extra appending; an unknown kind returns an empty table and the view degrades to "no fields to edit".
 */
export function fieldsForKind(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): FieldSpec[] {
  const description = descriptionOf(catalog, kind);
  return description ? [...description.fields] : [];
}

/** Whether the card kind is graded by the server (equivalent to "the kind implements Grader"). */
export function isGraded(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): boolean {
  return descriptionOf(catalog, kind)?.graded ?? false;
}

/** Front field name; returns an empty string for an unknown kind. */
export function frontField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.front_field ?? '';
}

/** Back field name; returns an empty string for an unknown kind. */
export function backField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.back_field ?? '';
}

/** Prompt field name of an answer-graded kind; returns an empty string for non-answer kinds. */
export function promptField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.prompt_field ?? '';
}

/** Options field name of an answer-graded kind; returns an empty string when there is no options field. */
export function optionsField(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): string {
  return descriptionOf(catalog, kind)?.options_field ?? '';
}

/** Answer control type; returns none for an unknown kind. */
export function answerControl(
  catalog: CardTypeCatalog | null,
  kind: string | null | undefined
): CardTypeAnswerControl {
  return descriptionOf(catalog, kind)?.answer_control ?? 'none';
}

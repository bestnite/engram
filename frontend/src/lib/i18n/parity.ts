import type { LocaleCatalog } from './types';
import { pluralBaseKey } from './plural';

/**
 * The messages a catalog must carry: every key that is not a plural variant of a count-driven id.
 */
function baseKeys(catalog: LocaleCatalog): Set<string> {
  const keys = new Set<string>();
  for (const key of Object.keys(catalog)) {
    if (pluralBaseKey(key) === key) keys.add(key);
  }
  return keys;
}

/**
 * Check whether two language packs cover the same messages.
 *
 * Keys are compared after collapsing count-driven variants (`decks.count.one`, `decks.count.few`, ...)
 * to their base id, because a language stores exactly the plural categories its own rule can select:
 * English needs `one`, Russian needs `one`, `few` and `many`, Japanese needs none. Every other key must
 * exist in both catalogs, so a message cannot be forgotten in one language.
 */
export interface ParityResult {
  identical: boolean;
  missingInB: string[];
  extraInB: string[];
}

/**
 * Compare the key differences between two language packs
 */
export function checkCatalogParity(catalogA: LocaleCatalog, catalogB: LocaleCatalog): ParityResult {
  const keysA = baseKeys(catalogA);
  const keysB = baseKeys(catalogB);

  const missingInB = Array.from(keysA).filter((k) => !keysB.has(k)).sort();
  const extraInB = Array.from(keysB).filter((k) => !keysA.has(k)).sort();

  // A plural variant is allowed only where the other catalog carries its base message; without the
  // base the variant could never be reached and the message would fall back to another language.
  for (const key of Object.keys(catalogB)) {
    const base = pluralBaseKey(key);
    if (base !== key && !keysA.has(base) && !extraInB.includes(base)) {
      extraInB.push(key);
    }
  }

  return {
    identical: missingInB.length === 0 && extraInB.length === 0,
    missingInB,
    extraInB,
  };
}

/**
 * Assert that two language packs have exactly the same keys; throw an explicit English error when they do not
 */
export function assertCatalogParity(
  catalogA: LocaleCatalog,
  nameA: string,
  catalogB: LocaleCatalog,
  nameB: string
): void {
  const result = checkCatalogParity(catalogA, catalogB);
  if (!result.identical) {
    const details: string[] = [];
    if (result.missingInB.length > 0) {
      details.push(`missing in ${nameB}: [${result.missingInB.join(', ')}]`);
    }
    if (result.extraInB.length > 0) {
      details.push(`extra in ${nameB}: [${result.extraInB.join(', ')}]`);
    }
    throw new Error(`Locale catalog parity mismatch between ${nameA} and ${nameB}: ${details.join('; ')}`);
  }
}

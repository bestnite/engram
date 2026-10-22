import type { LocaleCatalog } from './types';

/**
 * Check whether two language packs have exactly the same keys (AGENTS.md §2.1)
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
  const keysA = new Set(Object.keys(catalogA));
  const keysB = new Set(Object.keys(catalogB));

  const missingInB = Array.from(keysA).filter((k) => !keysB.has(k)).sort();
  const extraInB = Array.from(keysB).filter((k) => !keysA.has(k)).sort();

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

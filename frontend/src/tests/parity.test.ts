import { describe, it, expect } from 'vitest';
import { checkCatalogParity, assertCatalogParity } from '../lib/i18n/parity';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

describe('Locale catalog parity validation', () => {
  it('detects mismatched keys and fails parity check when keys are missing or extra', () => {
    // Build a deliberately mismatched test dictionary
    const baseCatalog = {
      'app.name': 'Engram',
      'nav.today': '今日',
      'nav.decks': '卡组',
    };

    const mismatchedCatalog = {
      'app.name': 'Engram',
      'nav.today': 'Today',
      // Missing 'nav.decks', extra 'nav.extra'
      'nav.extra': 'Extra',
    };

    const result = checkCatalogParity(baseCatalog, mismatchedCatalog);
    expect(result.identical).toBe(false);
    expect(result.missingInB).toEqual(['nav.decks']);
    expect(result.extraInB).toEqual(['nav.extra']);

    // Verify the assertion function always throws when keys do not match
    expect(() => {
      assertCatalogParity(baseCatalog, 'base', mismatchedCatalog, 'mismatched');
    }).toThrowError(/Locale catalog parity mismatch/);
  });

  it('passes parity check for identical key sets', () => {
    const catalogA = { 'a.b': 'valA', 'c.d': 'valA2' };
    const catalogB = { 'a.b': 'valB', 'c.d': 'valB2' };

    const result = checkCatalogParity(catalogA, catalogB);
    expect(result.identical).toBe(true);
    expect(result.missingInB).toHaveLength(0);
    expect(result.extraInB).toHaveLength(0);
    expect(() => assertCatalogParity(catalogA, 'A', catalogB, 'B')).not.toThrow();
  });

  it('verifies production zh-CN and en catalogs have 100% identical keys', () => {
    // Force-verify that the production language pack skeletons zh-CN and en have exactly the same keys
    const result = checkCatalogParity(zhCN, en);
    expect(result.missingInB).toEqual([]);
    expect(result.extraInB).toEqual([]);
    expect(result.identical).toBe(true);

    // assertCatalogParity must pass without throwing
    expect(() => assertCatalogParity(zhCN, 'zh-CN', en, 'en')).not.toThrow();
  });
});

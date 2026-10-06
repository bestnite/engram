import { describe, it, expect } from 'vitest';
import { checkCatalogParity, assertCatalogParity } from '../lib/i18n/parity';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

describe('Locale catalog parity validation', () => {
  it('detects mismatched keys and fails parity check when keys are missing or extra', () => {
    // 构造故意不一致的测试字典
    const baseCatalog = {
      'app.name': 'Engram',
      'nav.today': '今日',
      'nav.decks': '卡组',
    };

    const mismatchedCatalog = {
      'app.name': 'Engram',
      'nav.today': 'Today',
      // 缺少 'nav.decks'，多出 'nav.extra'
      'nav.extra': 'Extra',
    };

    const result = checkCatalogParity(baseCatalog, mismatchedCatalog);
    expect(result.identical).toBe(false);
    expect(result.missingInB).toEqual(['nav.decks']);
    expect(result.extraInB).toEqual(['nav.extra']);

    // 验证断言函数在 key 不匹配时必然抛出异常
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
    // 强制验证生产语言包骨架 zh-CN 与 en 的 key 完全一致
    const result = checkCatalogParity(zhCN, en);
    expect(result.missingInB).toEqual([]);
    expect(result.extraInB).toEqual([]);
    expect(result.identical).toBe(true);

    // assertCatalogParity 必须无异常通过
    expect(() => assertCatalogParity(zhCN, 'zh-CN', en, 'en')).not.toThrow();
  });
});

import { describe, it, expect } from 'vitest';
import { checkCatalogParity, assertCatalogParity } from '../lib/i18n/parity';
import { pluralBaseKey, PLURAL_BASE_KEYS } from '../lib/i18n/plural';
import { SUPPORTED_LOCALES, type LocaleCatalog, type SupportedLocale } from '../lib/i18n/types';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { zhTW } from '../lib/i18n/locales/zh-TW';
import { en } from '../lib/i18n/locales/en';
import { ja } from '../lib/i18n/locales/ja';
import { ko } from '../lib/i18n/locales/ko';
import { es } from '../lib/i18n/locales/es';
import { fr } from '../lib/i18n/locales/fr';
import { de } from '../lib/i18n/locales/de';
import { ptBR } from '../lib/i18n/locales/pt-BR';
import { ru } from '../lib/i18n/locales/ru';

const production: Record<SupportedLocale, LocaleCatalog> = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  en,
  ja,
  ko,
  es,
  fr,
  de,
  'pt-BR': ptBR,
  ru,
};

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

  it('accepts the plural variants a language needs and still requires the base key', () => {
    // English stores only the singular variant; Russian stores few/many as well. Both cover the
    // same count-driven message, so they are aligned.
    const english = { 'decks.count': 'Deck count', 'decks.count.one': 'One deck' };
    const russian = {
      'decks.count': 'Количество колод',
      'decks.count.one': 'Одна колода',
      'decks.count.few': 'Две колоды',
      'decks.count.many': 'Пять колод',
    };
    expect(checkCatalogParity(english, russian)).toMatchObject({
      identical: true,
      missingInB: [],
      extraInB: [],
    });

    // A catalog that stores only a variant still lacks the base message the runtime selects from.
    const variantOnly = checkCatalogParity(russian, { 'decks.count.one': 'One deck' });
    expect(variantOnly.identical).toBe(false);
    expect(variantOnly.missingInB).toEqual(['decks.count']);
    // A variant of an id that is not a count-driven message is not collapsed: it must exist in both.
    expect(checkCatalogParity({}, { 'stats.grade.other': 'Other' }).extraInB).toEqual([
      'stats.grade.other',
    ]);
  });

  it('collapses only the declared count-driven ids', () => {
    expect(pluralBaseKey('decks.count.one')).toBe('decks.count');
    expect(pluralBaseKey('decks.count.few')).toBe('decks.count');
    // Not a count-driven id: the suffix is part of the key itself.
    expect(pluralBaseKey('stats.grade.other')).toBe('stats.grade.other');
    expect(pluralBaseKey('media.browser.class.other')).toBe('media.browser.class.other');
    // An unknown id with a plural suffix stays as it is and is therefore reported.
    expect(pluralBaseKey('unknown.message.one')).toBe('unknown.message.one');
    for (const key of PLURAL_BASE_KEYS) {
      expect(pluralBaseKey(`${key}.many`)).toBe(key);
    }
  });

  it('verifies every production catalog has the same messages as en', () => {
    // Every shipped language must be registered here; a catalog added to SUPPORTED_LOCALES without
    // an entry would otherwise never be checked.
    expect(Object.keys(production).sort()).toEqual([...SUPPORTED_LOCALES].sort());

    for (const code of SUPPORTED_LOCALES) {
      const result = checkCatalogParity(en, production[code]);
      expect(result.missingInB, `${code} is missing messages`).toEqual([]);
      expect(result.extraInB, `${code} has extra messages`).toEqual([]);
      expect(() => assertCatalogParity(en, 'en', production[code], code)).not.toThrow();
    }
  });

  it('gives every shipped language a display name in every catalog', () => {
    for (const code of SUPPORTED_LOCALES) {
      for (const catalogCode of SUPPORTED_LOCALES) {
        expect(
          production[catalogCode][`language.${code}`],
          `${catalogCode} has no label for ${code}`
        ).toBeTruthy();
      }
    }
  });
});

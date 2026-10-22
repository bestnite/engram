import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import HelpTip from '../lib/components/ui/HelpTip.svelte';
import { setLocale } from '../lib/i18n';

/**
 * HelpTip under server-side rendering: only the trigger button renders, and the
 * explanation body is absent from the page before opening; the button's accessible name
 * comes from the language pack and includes the name of the metric being explained.
 */
describe('HelpTip (server render)', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders a closed button labelled from the catalog', () => {
    const { html } = render(HelpTip, {
      props: { label: '留存率', text: '到期复习时答对的比例。', testId: 'tip' },
    });

    expect(html).toContain('data-testid="tip"');
    expect(html).toContain('aria-label="留存率是什么意思？"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toMatch(/<button[^>]*type="button"/);
    expect(html).not.toContain('到期复习时答对的比例。');
  });

  it('uses the English label when the locale is en', () => {
    setLocale('en');
    const { html } = render(HelpTip, { props: { label: 'Retention', text: 'x' } });

    expect(html).toContain('aria-label="What does “Retention” mean?"');
  });
});

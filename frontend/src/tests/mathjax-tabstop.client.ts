import { describe, it, expect, afterEach } from 'vitest';
import { removeMenuTabStops } from '../lib/mathjax';

/**
 * MathJax gives every rendered formula a tabindex of its own so the context menu is keyboard-reachable. Inside an interactive
 * control that tab stop nests a second focus stop in the control, so the review option containers strip it after typesetting.
 */
describe('removeMenuTabStops', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('removes tabindex from MathJax containers below the given nodes, including nested ones', () => {
    const control = document.createElement('div');
    control.innerHTML =
      '<mjx-container tabindex="0"></mjx-container><div><mjx-container tabindex="0"></mjx-container></div>';

    removeMenuTabStops([control]);

    expect(control.querySelectorAll('mjx-container[tabindex]')).toHaveLength(0);
    // Only the focus stop goes: both formulas stay rendered.
    expect(control.querySelectorAll('mjx-container')).toHaveLength(2);
  });

  it('skips null and undefined nodes', () => {
    const control = document.createElement('div');
    control.innerHTML = '<mjx-container tabindex="0"></mjx-container>';

    expect(() => removeMenuTabStops([null, undefined, control])).not.toThrow();
    expect(control.querySelectorAll('mjx-container[tabindex]')).toHaveLength(0);
  });

  it('leaves containers outside the given nodes untouched', () => {
    const outside = document.createElement('div');
    outside.innerHTML = '<mjx-container tabindex="0"></mjx-container>';
    const control = document.createElement('div');

    removeMenuTabStops([control]);

    expect(outside.querySelectorAll('mjx-container[tabindex]')).toHaveLength(1);
  });
});

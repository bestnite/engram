import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import AdminIndexView from '../lib/views/admin/AdminIndexView.svelte';
import { routeStore, navigate } from '../lib/router';

/**
 * /admin 没有概览页：挂载入口视图后，地址必须被替换成 /admin/users，
 * 并且是 replace 而不是 push——否则按返回键会回到 /admin，再被弹回用户管理，像卡住了一样。
 */
describe('AdminIndexView', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
  });

  it('replaces /admin with /admin/users without adding a history entry', () => {
    navigate('/admin');
    const lengthBefore = window.history.length;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(AdminIndexView, { target });
    flushSync();

    expect(window.location.pathname).toBe('/admin/users');
    expect(get(routeStore).route?.name).toBe('admin-users');
    expect(window.history.length).toBe(lengthBefore);
  });
});

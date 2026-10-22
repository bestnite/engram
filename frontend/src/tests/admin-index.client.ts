import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import AdminIndexView from '../lib/views/admin/AdminIndexView.svelte';
import { routeStore, navigate } from '../lib/router';

/**
 * /admin has no overview page: after mounting the entry view the address must be replaced with /admin/users,
 * and with replace rather than push — otherwise pressing back returns to /admin and bounces to user management again, as if stuck.
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

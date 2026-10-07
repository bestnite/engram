import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  ApiClient,
  ApiClientError,
  validateProfileForm,
} from '../lib/api';
import {
  setLocale,
  getLocale,
} from '../lib/i18n';

describe('Settings view form state and API gap handling', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    setLocale('zh-CN');
  });

  describe('API Gap detection and safe degradation', () => {
    it('detects 404 Not Found on getProfile without fabricating endpoints or calling form handlers', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: 'not_found',
              message: 'Endpoint not found',
            },
          }),
          { status: 404, headers: { 'Content-Type': 'application/json' } }
        )
      );

      let serverApiAvailable: boolean | null = null;
      try {
        await client.getProfile();
        serverApiAvailable = true;
      } catch (err) {
        if (err instanceof ApiClientError && err.isNotFound) {
          // 证明捕获到 404，UI 进入 serverApiAvailable = false 降级状态
          serverApiAvailable = false;
        }
      }

      expect(serverApiAvailable).toBe(false);
      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v1/profile',
        expect.objectContaining({ credentials: 'same-origin' })
      );
      // 严禁向 Go SSR 表单端点（/settings/profile 或 /settings/locale）发起 POST 表单模拟
      const calledUrls = mockFetch.mock.calls.map((call) => call[0]);
      expect(calledUrls).not.toContain('/settings/profile');
      expect(calledUrls).not.toContain('/settings/locale');
    });

    it('populates form data when profile endpoint is present', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            profile: {
              display_name: 'Existing User',
              locale: 'en',
              timezone: 'Asia/Tokyo',
              day_cutoff_hour: 0,
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      );

      const profile = await client.getProfile();
      expect(profile.display_name).toBe('Existing User');
      expect(profile.locale).toBe('en');
      expect(profile.timezone).toBe('Asia/Tokyo');
      expect(profile.day_cutoff_hour).toBe(0);

      // 同步测试设置 locale
      setLocale(profile.locale as 'en');
      expect(getLocale()).toBe('en');
    });
  });

  describe('Client-side form validation before submission', () => {
    it('blocks submission and returns error when display_name is empty', () => {
      const res = validateProfileForm({
        display_name: '   ',
        locale: 'zh-CN',
        timezone: 'Asia/Shanghai',
        day_cutoff_hour: '4',
      });

      expect(res.valid).toBe(false);
      expect(res.errors.display_name).toBe('settings.error.display_name_required');
      expect(res.data).toBeUndefined();
    });

    it('blocks submission and returns error when timezone is invalid', () => {
      const res = validateProfileForm({
        display_name: 'Valid Name',
        locale: 'zh-CN',
        timezone: 'Invalid/NonExistent_Zone',
        day_cutoff_hour: '4',
      });

      expect(res.valid).toBe(false);
      expect(res.errors.timezone).toBe('settings.error.timezone_invalid');
    });

    it('blocks submission and returns error when cutoff hour is invalid', () => {
      const res = validateProfileForm({
        display_name: 'Valid Name',
        locale: 'zh-CN',
        timezone: 'UTC',
        day_cutoff_hour: '25',
      });

      expect(res.valid).toBe(false);
      expect(res.errors.day_cutoff_hour).toBe('settings.error.cutoff_invalid');
    });

    it('allows valid submission and preserves midnight cutoff (0)', () => {
      const res = validateProfileForm({
        display_name: 'Night Owl',
        locale: 'zh-CN',
        timezone: 'Asia/Shanghai',
        day_cutoff_hour: '0',
      });

      expect(res.valid).toBe(true);
      expect(res.data?.day_cutoff_hour).toBe(0);
      expect(res.data?.display_name).toBe('Night Owl');
    });
  });

  describe('Locale persistence in SPA client store', () => {
    it('immediately reflects language changes in localeStore upon selection', () => {
      expect(getLocale()).toBe('zh-CN');

      setLocale('en');
      expect(getLocale()).toBe('en');

      setLocale('zh-CN');
      expect(getLocale()).toBe('zh-CN');
    });
  });
});

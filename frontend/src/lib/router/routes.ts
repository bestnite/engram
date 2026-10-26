import type { RouteDefinition } from './types';
import HomeView from '../views/HomeView.svelte';
import DecksView from '../views/DecksView.svelte';
import DeckDetailView from '../views/DeckDetailView.svelte';
import StatsView from '../views/StatsView.svelte';
import SettingsView from '../views/SettingsView.svelte';
import APIKeysView from '../views/APIKeysView.svelte';
import LoginView from '../views/LoginView.svelte';
import TOTPLoginView from '../views/TOTPLoginView.svelte';
import RegisterView from '../views/RegisterView.svelte';
import SetupView from '../views/SetupView.svelte';
import ReviewView from '../views/ReviewView.svelte';
import NoteEditView from '../views/NoteEditView.svelte';
import ImportView from '../views/ImportView.svelte';
import DeckSharingView from '../views/DeckSharingView.svelte';
import DeckSettingsView from '../views/DeckSettingsView.svelte';
import SecurityView from '../views/SecurityView.svelte';
import PresetsView from '../views/PresetsView.svelte';
import AdminIndexView from '../views/admin/AdminIndexView.svelte';
import AdminHealthView from '../views/admin/AdminHealthView.svelte';
import AdminAuditView from '../views/admin/AdminAuditView.svelte';
import AdminUsersView from '../views/admin/AdminUsersView.svelte';
import AdminRegistrationView from '../views/admin/AdminRegistrationView.svelte';
import AdminAPIKeysView from '../views/admin/AdminAPIKeysView.svelte';
import AdminSettingsView from '../views/admin/AdminSettingsView.svelte';
import AdminSMTPView from '../views/admin/AdminSMTPView.svelte';
import AdminOIDCView from '../views/admin/AdminOIDCView.svelte';
import AdminJobsView from '../views/admin/AdminJobsView.svelte';
import AdminI18nView from '../views/admin/AdminI18nView.svelte';
// Mail templates were merged into AdminSMTPView (the same page); only the legacy-path redirect view is left here.
import AdminMailMovedView from '../views/admin/AdminMailMovedView.svelte';
import ForgotPasswordView from '../views/ForgotPasswordView.svelte';
import ResetPasswordView from '../views/ResetPasswordView.svelte';
import EmailSettingsView from '../views/EmailSettingsView.svelte';
import VerifyEmailView from '../views/VerifyEmailView.svelte';
import ConfirmEmailChangeView from '../views/ConfirmEmailChangeView.svelte';
import UnsubscribeView from '../views/UnsubscribeView.svelte';
import ShortcodesHelpView from '../views/ShortcodesHelpView.svelte';

/**
 * Frontend skeleton route definition list
 */
export const routes: RouteDefinition[] = [
  {
    path: '/import',
    name: 'import',
    component: ImportView as unknown as RouteDefinition['component'],
  },
  {
    path: '/',
    name: 'home',
    component: HomeView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks',
    name: 'decks',
    component: DecksView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/sharing',
    name: 'deck-sharing',
    component: DeckSharingView as unknown as RouteDefinition['component'],
  },
  {
    // Deck daily limit settings (canonical path). The server's GET /decks/:id/settings has already switched to
    // the app shell (the owner gate is still on the server); reads and writes go through /api/v1/decks/:id/settings.
    path: '/decks/:id/settings',
    name: 'deck-settings',
    component: DeckSettingsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id',
    name: 'deck-detail',
    component: DeckDetailView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes',
    name: 'deck-notes',
    component: DeckDetailView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes/:noteId/edit',
    name: 'note-edit',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    // Canonical address for creating a card. It shares the same editor implementation as editing (NoteEditView):
    // no noteId in the address means create mode, and on success it always navigates back to /decks/:id.
    path: '/decks/:id/notes/new',
    name: 'note-create',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    // Legacy SSR edit address (canonical route): the server's GET /decks/:id/notes/:nid now returns
    // the app shell, so the client must handle this exact URL, otherwise old links and reloads land on the 404 view.
    // The param is named noteId, matching $routeStore.params.noteId read by NoteEditView.
    path: '/decks/:id/notes/:noteId',
    name: 'note-edit-nid',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    // Legacy SSR create address: the server's GET /decks/:id/new-note returns the app shell; the client handles that exact URL.
    // It likewise points at the shared editor (create mode).
    path: '/decks/:id/new-note',
    name: 'note-create-legacy',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    // Canonical route of the review page. The server's GET /review has switched to the SPA app shell,
    // so the client must handle this exact URL, otherwise the home page's "start review" link and reloads land on the 404 view.
    path: '/review',
    name: 'review',
    component: ReviewView as unknown as RouteDefinition['component'],
  },
  {
    path: '/stats',
    name: 'stats',
    component: StatsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/settings',
    name: 'settings',
    component: SettingsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/settings/keys',
    name: 'settings-keys',
    component: APIKeysView as unknown as RouteDefinition['component'],
  },
  {
    // Security: password change and two-factor auth. The server's GET /settings/security returns the app shell,
    // reads and writes go through /api/v1/settings/password and /api/v1/settings/totp*.
    path: '/settings/security',
    name: 'security-settings',
    component: SecurityView as unknown as RouteDefinition['component'],
  },
  {
    path: '/login',
    name: 'login',
    component: LoginView as unknown as RouteDefinition['component'],
  },
  {
    // Login step two (TOTP). The server has no GET route for /login/totp; the canonical path comes from NoRoute
    // falling back to the app shell (page-type GETs always fall back to the shell).
    // The protocol is GET /api/v1/auth/totp (query credential status) and POST /api/v1/auth/totp (submit the code).
    path: '/login/totp',
    name: 'totp-login',
    component: TOTPLoginView as unknown as RouteDefinition['component'],
  },
  {
    // Canonical paths for registration and first-admin setup. The server's GET /register and /setup are answered by the app shell,
    // and the write protocol is POST /api/v1/auth/register and POST /api/v1/auth/setup.
    path: '/register',
    name: 'register',
    component: RegisterView as unknown as RouteDefinition['component'],
  },
  {
    path: '/setup',
    name: 'setup',
    component: SetupView as unknown as RouteDefinition['component'],
  },
  {
    // Scheduling presets page (canonical path). The server's GET /presets has switched to the app shell,
    // reads and writes go through the /api/v1/presets* session JSON endpoints.
    path: '/presets',
    name: 'presets',
    component: PresetsView as unknown as RouteDefinition['component'],
  },
  {
    // Admin panel entry. The server's GET /admin has switched to the app shell (requireAdmin runs before the shell);
    // there is no overview page, so the frontend switches straight to user management.
    path: '/admin',
    name: 'admin-index',
    component: AdminIndexView as unknown as RouteDefinition['component'],
  },
  {
    // Admin panel health page. The server's GET /admin/health has switched to the app shell; readings come from /api/v1/admin/health.
    path: '/admin/health',
    name: 'admin-health',
    component: AdminHealthView as unknown as RouteDefinition['component'],
  },
  {
    // Admin panel audit search. The server's GET /admin/audit has switched to the app shell; searching goes through /api/v1/admin/audit.
    path: '/admin/audit',
    name: 'admin-audit',
    component: AdminAuditView as unknown as RouteDefinition['component'],
  },
  {
    // User management. The server's GET /admin/users has switched to the app shell; reads and writes go through /api/v1/admin/users*.
    path: '/admin/users',
    name: 'admin-users',
    component: AdminUsersView as unknown as RouteDefinition['component'],
  },
  {
    // Registration and invites. The server's GET /admin/registration has switched to the app shell; reads and writes go through /api/v1/admin/(registration|invites).
    path: '/admin/registration',
    name: 'admin-registration',
    component: AdminRegistrationView as unknown as RouteDefinition['component'],
  },
  {
    // API key overview. The server's GET /admin/api-keys has switched to the app shell; reads and writes go through /api/v1/admin/api-keys*.
    path: '/admin/api-keys',
    name: 'admin-api-keys',
    component: AdminAPIKeysView as unknown as RouteDefinition['component'],
  },
  {
    // System settings. The server's GET /admin/settings has switched to the app shell; reads and writes go through /api/v1/admin/settings.
    path: '/admin/settings',
    name: 'admin-settings',
    component: AdminSettingsView as unknown as RouteDefinition['component'],
  },
  {
    // Mail (SMTP). The server's GET /admin/smtp has switched to the app shell; reads, writes and tests go through /api/v1/admin/smtp*.
    path: '/admin/smtp',
    name: 'admin-smtp',
    component: AdminSMTPView as unknown as RouteDefinition['component'],
  },
  {
    // Identity and OIDC. The server's GET /admin/oidc has switched to the app shell; reads, writes, tests and unlinking go through /api/v1/admin/oidc*.
    path: '/admin/oidc',
    name: 'admin-oidc',
    component: AdminOIDCView as unknown as RouteDefinition['component'],
  },
  {
    // Jobs. The server's GET /admin/jobs has switched to the app shell; listing and cancellation go through /api/v1/admin/jobs*.
    path: '/admin/jobs',
    name: 'admin-jobs',
    component: AdminJobsView as unknown as RouteDefinition['component'],
  },
  {
    // Language pack completeness. The server's GET /admin/i18n has switched to the app shell; the report comes from /api/v1/admin/i18n.
    path: '/admin/i18n',
    name: 'admin-i18n',
    component: AdminI18nView as unknown as RouteDefinition['component'],
  },
{
    // Mail templates were merged into the "mail" page (the entry above); this one only exists so old links and bookmarks keep working:
    // it renders no content, it only swaps the address over (reads and writes still go through /api/v1/admin/mail-templates*).
    path: '/admin/mail-templates',
    name: 'admin-mail-templates-moved',
    component: AdminMailMovedView as unknown as RouteDefinition['component'],
  },
  {
    // Password reset request. The server's GET /forgot-password is answered by the app shell.
    // The protocol is POST /api/v1/auth/forgot-password; the response only contains the site-level mail_ready and does not reveal whether the account exists.
    path: '/forgot-password',
    name: 'forgot-password',
    component: ForgotPasswordView as unknown as RouteDefinition['component'],
  },
  {
    // Set a new password. The server's GET /reset-password is answered by the app shell; the token arrives in the email link's query string,
    // and the submission goes through POST /api/v1/auth/reset-password (one-time token, spent once used).
    path: '/reset-password',
    name: 'reset-password',
    component: ResetPasswordView as unknown as RouteDefinition['component'],
  },
  {
    // Account and email settings. The server's GET /settings/email has switched to the app shell; unauthenticated requests are redirected to the login page on the server.
    // Reading the email, changing it and resending verification go through /api/v1/settings/email and /api/v1/settings/verify-email.
    path: '/settings/email',
    name: 'email-settings',
    component: EmailSettingsView as unknown as RouteDefinition['component'],
  },
  {
    // Canonical path for the email verification result. The server's GET /verify-email returns the app shell,
    // the one-click link in the email points here; the view reads ?token= and then consumes it via POST /api/v1/auth/verify-email.
    path: '/verify-email',
    name: 'verify-email',
    component: VerifyEmailView as unknown as RouteDefinition['component'],
  },
  {
    // Canonical path for the email change confirmation result. The server's GET /confirm-email-change returns the app shell,
    // and the token is consumed via POST /api/v1/auth/confirm-email-change.
    path: '/confirm-email-change',
    name: 'confirm-email-change',
    component: ConfirmEmailChangeView as unknown as RouteDefinition['component'],
  },
  {
    // Canonical path for one-click unsubscribe. The server's GET /unsubscribe returns the app shell and issues
    // the pre-session double-submit cookie; the view reads ?token= and then reads/confirms it via GET/POST /api/v1/unsubscribe.
    path: '/unsubscribe',
    name: 'unsubscribe',
    component: UnsubscribeView as unknown as RouteDefinition['component'],
  },
  {
    // Shortcode reference page. It reads no API and needs no parameters: the content is static documentation
    // of the server's shortcode grammar, so the view renders in the same way on every visit.
    path: '/help/shortcodes',
    name: 'help-shortcodes',
    component: ShortcodesHelpView as unknown as RouteDefinition['component'],
  },
];

package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA's email notification preference API: the JSON version of M1-18.
//
// Semantics are exactly those of the catalog definition (internal/mail), only the transport is JSON:
//   - which types are optional, A being non-optional, and the default switches are all derived from the internal/mail catalog; the rules are not copied here;
//   - saving writes one explicit choice per optional type, and anything not submitted counts as off (consistent with a missing checkbox);
//   - D admin notifications go to admins only: a non-admin's response omits that group, submitting a preference for that class is refused with 403,
//     and no explicit choice for that class is written for a non-admin (otherwise someone promoted to admin later would take office with a stale "off");
//   - reminder_hour null means the site default (NULL), 0–23 are explicit hours; 0 is a valid midnight,
//     and "not set" is expressed only as null, never substituted with 0 (AGENTS.md §2.3 item 9).
//
// Reads require no CSRF; writes always go through session CSRF. Responses expose only the types registered in the catalog, unknown types never appear,
// and the frontend may not write to the database using anything outside the catalog. Email notification preferences are shown on the /settings/email page,
// and all preference reads and writes go through here.

// notificationType is one mail type's state on the preferences page: a stable English identifier plus catalog-derived results, with no copy.
// The frontend looks up its own language-pack key by type, so localized text is never returned here.
type notificationType struct {
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Locked  bool   `json:"locked"`
}

// notificationGroup is a set of types grouped by major class, in catalog order (A → B → C → D).
type notificationGroup struct {
	Class string             `json:"class"`
	Types []notificationType `json:"types"`
}

// notificationPrefsResponse is the response body shared by GET and PATCH.
type notificationPrefsResponse struct {
	Groups []notificationGroup `json:"groups"`
	// ReminderHour null means the user has not set it (the site default applies); 0–23 is an explicitly chosen hour.
	ReminderHour *int `json:"reminder_hour"`
	// DefaultReminderHour is the site's default send hour (reminder.DefaultSendHour),
	// so the frontend can render the "site default (HH:00)" option without copying a constant like 19 into the frontend.
	DefaultReminderHour int    `json:"default_reminder_hour"`
	Timezone            string `json:"timezone"`
}

// notificationPrefsRequest is the save request body: explicit choices plus the send hour.
// A missing or null choices means every optional type is off (consistent with all checkboxes absent).
type notificationPrefsRequest struct {
	Choices      map[string]bool `json:"choices"`
	ReminderHour *int            `json:"reminder_hour"`
}

// registerMailPrefsAPIRoutes mounts the SPA's email notification preference API; writes go through session CSRF.
// Skips when the dependency is not wired up, so the M0 stage and tests without the mail feature can still construct a Server.
func (s *Server) registerMailPrefsAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	router.GET("/api/v1/settings/notifications", s.notificationPrefsGet)
	router.PATCH("/api/v1/settings/notifications", s.sessions.CSRFMiddleware(), s.notificationPrefsPatch)
}

// notificationPrefsGet returns the current user's mail preferences and send hour, and accepts browser sessions only.
func (s *Server) notificationPrefsGet(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	resp, err := s.notificationPrefsPayload(c, user.ID)
	if err != nil {
		s.logger.Error("build SPA notification preferences failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// notificationPrefsPayload assembles the preference response: groups and switches are always derived from the internal/mail catalog.
// The send hour matches the latest value on the user row, so users is re-read (the session cache may be a stale snapshot).
// The role also comes from this re-read: type D goes to admins only, and a non-admin's response does not even contain that group.
func (s *Server) notificationPrefsPayload(c *gin.Context, userID uint64) (notificationPrefsResponse, error) {
	ctx := c.Request.Context()
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, userID)
	if err != nil {
		return notificationPrefsResponse{}, err
	}
	fresh, err := s.users.ByID(ctx, userID)
	if err != nil {
		return notificationPrefsResponse{}, err
	}
	admin := fresh.Role == store.RoleAdmin
	groups := make([]notificationGroup, 0, len(mail.ClassOrder()))
	for _, class := range mail.ClassOrder() {
		if mail.AdminOnly(class) && !admin {
			continue
		}
		group := notificationGroup{Class: string(class)}
		for _, def := range mail.Catalog() {
			if def.Class != class {
				continue
			}
			group.Types = append(group.Types, notificationType{
				Type:    string(def.Type),
				Enabled: mail.ResolveEnabled(choices, def.Type),
				Locked:  !mail.CanDisable(def.Class),
			})
		}
		groups = append(groups, group)
	}
	return notificationPrefsResponse{
		Groups:              groups,
		ReminderHour:        fresh.ReminderHour,
		DefaultReminderHour: reminder.DefaultSendHour,
		Timezone:            fresh.Timezone,
	}, nil
}

// notificationPrefsPatch saves the user's switches for the optional types and the send hour.
//
// The rules match the SSR version: validate every preference key present in the submission before writing to the database, so a refusal leaves no half-saved state;
// type A cannot be turned off, so any submission targeting A is refused with 400; type D is open to admins only, and a non-admin submitting it is
// refused with 403 (rather than silently ignored — ignoring would make a forged request believe it succeeded, and would also leave the "the page should not display it" rule
// with no server-side implementation); unknown types are refused as well;
// optional types get an explicit choice written with the value from the submission (absent = off), so the default-off type C can be turned on too.
// Send hour null = site default, 0–23 are explicit values, anything else is refused.
func (s *Server) notificationPrefsPatch(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	// The role is looked up per request (the session middleware re-reads the users row from the database), so demotion or promotion takes effect immediately.
	admin := user.Role == store.RoleAdmin
	var req notificationPrefsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		notificationPrefsError(c, http.StatusBadRequest, "invalid_request", "The notification preferences request is invalid.")
		return
	}

	// Validate every preference key present in the submission first, then write: a refusal leaves no half-saved state.
	for key := range req.Choices {
		def, found := mail.Lookup(mail.Type(key))
		if !found {
			notificationPrefsError(c, http.StatusBadRequest, "unknown_type", "An unknown email type was submitted.")
			return
		}
		if !mail.CanDisable(def.Class) {
			notificationPrefsError(c, http.StatusBadRequest, "class_locked", "Security and transactional mail cannot be turned off.")
			return
		}
		if mail.AdminOnly(def.Class) && !admin {
			notificationPrefsError(c, http.StatusForbidden, "forbidden_type", "Administrator notices can only be configured by administrators.")
			return
		}
	}
	// 0 is a valid value (midnight), so the range check only runs when non-nil; both null and absent mean the site default.
	if req.ReminderHour != nil && (*req.ReminderHour < 0 || *req.ReminderHour > 23) {
		notificationPrefsError(c, http.StatusBadRequest, "reminder_hour_invalid", "The send hour must be a whole hour from 0 to 23.")
		return
	}

	ctx := c.Request.Context()
	// A missing checkbox means off; write an explicit choice for every optional type instead of relying on the old value in storage.
	// Type D is only meaningful for admins, and no choice is written for it when the user is not an admin: leaving it empty falls back to the catalog default (on),
	// so someone promoted to admin later does not take office with a stale "off".
	choices := make(map[string]bool, len(mail.Catalog()))
	for _, def := range mail.Catalog() {
		if !mail.CanDisable(def.Class) {
			continue
		}
		if mail.AdminOnly(def.Class) && !admin {
			continue
		}
		choices[string(def.Type)] = req.Choices[string(def.Type)]
	}
	if err := store.NewEmailPrefStore(s.db).SetChoices(ctx, user.ID, choices, time.Now().UTC()); err != nil {
		s.logger.Error("save SPA email preferences failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}

	// The send hour lives on the users row and is updated separately from the preferences; re-read before changing so concurrent writes are not overwritten.
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for SPA email preferences failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	fresh.ReminderHour = req.ReminderHour
	if err := s.users.Update(ctx, fresh); err != nil {
		s.logger.Error("save SPA reminder send hour failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	// The audit action and Detail shape match the SSR save (internal/web/mail_prefs.go).
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailPrefsUpdate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"choices": choices, "reminder_hour": req.ReminderHour},
	})

	resp, err := s.notificationPrefsPayload(c, user.ID)
	if err != nil {
		s.logger.Error("build SPA notification preferences after save failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// notificationPrefsError writes the error envelope of the SPA mail preference API.
// Same shape as totpError: code is stable and English, message is an English fallback, and the frontend maps code to a localized hint.
func notificationPrefsError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

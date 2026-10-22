package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the RFC 8058 one-click unsubscribe endpoint.
//
// Design points:
//   - the unsubscribe entry is only added to mails of **optional types** (B/C/D, where CanDisable is true); class A never carries it
//     (the headers are built centrally by UnsubscribeHeaders in internal/mail, and passing class A in still yields nil).
//   - tokenized and **needing no login**: unsubscribing should not require logging in first. The token is single-use and has a lifetime,
//     only its digest is stored (store.HashActionToken), the plaintext appears only in the mail link and never in the logs.
//   - the token's Payload names **that one** optional type; the endpoint only reads the type from the token and accepts no caller-supplied type,
//     so one token can only disable the type it names and cannot be redirected to another type or another user.
//
// After the SSR page layer was removed, unsubscribe is carried by three transports that share the same token semantics and the same implementation
// (consumeUnsubscribe); this is "adding transports", the security model is unchanged:
//   - POST /unsubscribe: the RFC 8058 One-Click machine endpoint (a mail client POSTs directly),
//     returning only a status code and a minimal JSON body, never rendering a template;
//   - GET /unsubscribe: returns the SPA app shell and sets the pre-session
//     double-submit cookie (the recipient may be logged out), with the page rendered by the client;
//   - /api/v1/unsubscribe: the SPA's same-origin JSON transport. GET reads the type the token names (without consuming it),
//     POST consumes the token, writes the preference and writes the audit.
//
// CSRF exception (a justified exception, not a way around the protection):
//   - POST /unsubscribe deliberately does not mount sessions.CSRFMiddleware. The RFC 8058 One-Click POST is sent
//     directly by the mail client, carries no session cookie, and there is no session to bind a token to; requiring CSRF would make
//     one-click unsubscribe fail in every real mail client, which amounts to not building the feature at all.
//   - the "credential" here is the high-entropy one-time token in the URL itself: only whoever received that mail has it,
//     it can only disable one optional type, and it is dead once used. Compared with forcing CSRF on and making the feature unusable,
//     token-as-capability is the correct boundary RFC 8058 recognizes.
//   - the SPA's POST /api/v1/unsubscribe is a pre-session write, so following the convention of /api/v1/auth/verify-email it mounts
//     auth.PreSessionCSRFMiddleware (the double-submit cookie comes from GET /unsubscribe).
//   - the other writes that carry a session are unaffected and still go through CSRFMiddleware (see mail_prefs.go and the like).

// unsubscribeTypeInvalidCode is the stable English code for when the token payload is not an unsubscribable type.
// The normal flow never issues such a token; if one appears the data is anomalous, and the frontend maps the code to a hint.
const unsubscribeTypeInvalidCode = "unsubscribe_type_invalid"

// errUnsubscribableType means the token payload is not an unsubscribable optional type (anomalous data).
// It is kept separate from store's token sentinel errors so callers can map it to a dedicated code.
var errUnsubscribableType = errors.New("unsubscribe token names an unsubscribable mail type")

// registerUnsubscribeRoutes mounts the login-free one-click unsubscribe routes.
// Skips when the token service is not wired (a Server can still be constructed at M0).
func (s *Server) registerUnsubscribeRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// GET returns the app shell (used when a person clicks the link in a mail), POST performs the unsubscribe (RFC 8058 One-Click).
	// Neither mounts CSRFMiddleware — for the reason see the file header comment.
	router.GET("/unsubscribe", s.unsubscribeShell)
	router.POST("/unsubscribe", s.unsubscribeSubmit)
	// The SPA's same-origin JSON transport: the read does not consume the token, the confirm does; the confirm uses pre-session double-submit CSRF.
	router.GET("/api/v1/unsubscribe", s.unsubscribeRead)
	router.POST("/api/v1/unsubscribe", auth.PreSessionCSRFMiddleware(), s.unsubscribeConfirm)
}

// unsubscribeShell serves GET /unsubscribe: first set the pre-session double-submit cookie
// (the recipient may be logged out), then return the SPA app shell; the unsubscribe protocol is run by the client via /api/v1/unsubscribe.
//
// It deliberately **does not consume** the token: link prefetchers and security scanners issue GETs on the URL, and if a GET unsubscribed,
// users would be unsubscribed out of nowhere. The real unsubscribe is only done by POST.
func (s *Server) unsubscribeShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// unsubscribeTokenFromRequest pulls the plaintext token out of the query string or the form body (the machine endpoint's input shape).
func unsubscribeTokenFromRequest(c *gin.Context) string {
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}
	return strings.TrimSpace(c.PostForm("token"))
}

// apiUnsubscribeRequest is the body of POST /api/v1/unsubscribe (the SPA's confirm request).
type apiUnsubscribeRequest struct {
	Token string `json:"token"`
}

// unsubscribeRead is the SPA's read endpoint (GET /api/v1/unsubscribe?token=…):
// it only reads the optional type the token names, consuming no token and writing no preference. For an invalid/used/expired token it returns a stable code,
// so the client can say "this link is no longer valid" before the user clicks confirm, instead of wasting a click.
func (s *Server) unsubscribeRead(c *gin.Context) {
	tok, err := s.tokens.Peek(c.Request.Context(), store.ActionTokenUnsubscribe, strings.TrimSpace(c.Query("token")))
	if err != nil {
		s.writeUnsubscribeError(c, err)
		return
	}
	typ := mail.Type(strings.TrimSpace(tok.Payload))
	if !mail.CanUnsubscribe(typ) {
		s.writeUnsubscribeError(c, errUnsubscribableType)
		return
	}
	c.JSON(http.StatusOK, gin.H{"type": string(typ)})
}

// unsubscribeConfirm is the SPA's confirm endpoint (POST /api/v1/unsubscribe, pre-session double-submit CSRF):
// it consumes the one-time unsubscribe token and disables only the optional type the token names.
func (s *Server) unsubscribeConfirm(c *gin.Context) {
	var req apiUnsubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	typ, err := s.consumeUnsubscribe(c.Request.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		s.writeUnsubscribeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"type": string(typ)})
}

// unsubscribeSubmit is the RFC 8058 One-Click machine endpoint (POST /unsubscribe).
//
// The mail client POSTs directly (possibly without cookies) and only cares about 2xx; so this returns only a status code and a minimal JSON body,
// never rendering a template. The token semantics are exactly those of the SPA's JSON confirm — both share consumeUnsubscribe.
func (s *Server) unsubscribeSubmit(c *gin.Context) {
	typ, err := s.consumeUnsubscribe(c.Request.Context(), unsubscribeTokenFromRequest(c))
	if err != nil {
		s.writeUnsubscribeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unsubscribed": true, "type": string(typ)})
}

// consumeUnsubscribe consumes the unsubscribe token, disables the optional type the token names, and returns that type.
//
// The order is deliberate: consume the token first (single use), then use its Payload to locate the type and the user —
// callers cannot supply a type, so "use a type-A token to disable type B" is impossible.
// All three transports share it, guaranteeing exactly one type is disabled, one preference write and one audit entry.
//
// The errors returned fall into three classes: store's token sentinels (NotFound/Used/Expired), errUnsubscribableType
// (anomalous payload), and others (preference read/write failures, already wrapped with %w and containing no plaintext token).
func (s *Server) consumeUnsubscribe(ctx context.Context, token string) (mail.Type, error) {
	tok, err := s.tokens.Consume(ctx, store.ActionTokenUnsubscribe, token)
	if err != nil {
		return "", err
	}
	typ := mail.Type(strings.TrimSpace(tok.Payload))
	if !mail.CanUnsubscribe(typ) {
		// The token payload is issued by the server, so an unsubscribable type means anomalous data; the token has already been consumed,
		// and the user can just click again from the mail. Class A is never unsubscribed (CanUnsubscribe is always false for class A).
		return "", errUnsubscribableType
	}
	prefs := store.NewEmailPrefStore(s.db)
	choices, err := prefs.Choices(ctx, tok.UserID)
	if err != nil {
		return "", fmt.Errorf("load email preferences: %w", err)
	}
	// Disable only this one type: write an explicit false over the existing choice, leaving the other types as they are.
	choices[string(typ)] = false
	if err := prefs.SetChoices(ctx, tok.UserID, choices, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("save email preferences: %w", err)
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailUnsubscribe,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
		Detail:     map[string]any{"type": string(typ)},
	})
	return typ, nil
}

// writeUnsubscribeError maps consume/read unsubscribe-token errors to stable English codes and status codes.
// Token errors and payload anomalies are both 400 (the user can fix it by clicking the mail again), and the rest log in English as 500.
// The plaintext token never goes into the log.
func (s *Server) writeUnsubscribeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errUnsubscribableType):
		apiAuthError(c, http.StatusBadRequest, unsubscribeTypeInvalidCode,
			"The unsubscribe link names a mail type that cannot be unsubscribed.")
	case errors.Is(err, store.ErrActionTokenNotFound),
		errors.Is(err, store.ErrActionTokenUsed),
		errors.Is(err, store.ErrActionTokenExpired):
		apiAuthError(c, http.StatusBadRequest, actionTokenErrorCode(err),
			"The unsubscribe link is invalid or has expired.")
	default:
		s.logger.Error("unsubscribe: consume failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
	}
}

// optionalUnsubscribeLink issues a login-free unsubscribe token naming that type for an optional type, and returns the unsubscribe link.
//
// Class A (not disableable) or a failed issue returns an empty string — better that this mail lacks an unsubscribe entry than to give security mail
// an unsubscribe entry, or to let sending fail because the unsubscribe token failed (a failed send must not fail the triggering action).
//
// The "get the link" layer is separate because **the mail body needs it too**: with only the RFC 8058 headers, a reader of the plain-text part
// sees no unsubscribe entry at all (the headers are there for the mail client to read).
func (s *Server) optionalUnsubscribeLink(c *gin.Context, userID uint64, typ mail.Type) string {
	if s.tokens == nil || !mail.CanUnsubscribe(typ) {
		return ""
	}
	token, err := s.tokens.Issue(c.Request.Context(), userID, store.ActionTokenUnsubscribe, string(typ), auth.UnsubscribeTTL)
	if err != nil {
		s.logger.Error("unsubscribe: issue token failed", "user_id", userID, "type", string(typ), "error", err)
		return ""
	}
	return s.securityAbsoluteURL(c, "/unsubscribe?token="+url.QueryEscape(token))
}

// optionalUnsubscribeHeaders is a thin wrapper over the above: get the link → turn it into the RFC 8058 headers.

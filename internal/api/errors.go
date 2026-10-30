// Package api provides the /api/v1 REST handlers plus the auth and rate-limit middleware.
package api

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// Stable error codes (English constants, for callers to branch on programmatically).
//
// REST and MCP errors uniformly return a stable English code and an English message,
// never varying with the request's Accept-Language. The SPA frontend maps localized hints from the stable code and does not parse backend messages.
// When adding a code you must register its English text in errorMessages.
const (
	CodeUnauthorized   = "unauthorized"
	CodeInvalidAPIKey  = "invalid_api_key"
	CodeScopeRequired  = "scope_required"
	CodeRateLimited    = "rate_limited"
	CodeInvalidRequest = "invalid_request"
	CodeNotFound       = "not_found"
	CodeForbidden      = "forbidden"
	// CodeInsufficientRole means the user can access the deck but the role is not enough (e.g. a reader trying to edit a card),
	// distinguished from CodeForbidden for "no access at all", so callers can decide precisely.
	CodeInsufficientRole = "insufficient_role"
	// CodeScopeNotGrantable means the key the request wants to create carries a scope only admins may hold
	// (currently only admin): "admin can only be issued to admin accounts".
	CodeScopeNotGrantable = "scope_not_grantable"
	CodeConflict          = "conflict"
	CodeVersionConflict   = "version_conflict"
	CodeInternal          = "internal_error"
	// CodeDeckNameInvalid means the deck name breaks the length or character rules when creating or renaming a deck
	// (same source as the deck package manifest; store-level ErrDeckNameInvalid).
	CodeDeckNameInvalid = "deck_name_invalid"
	// CodeDeckDescriptionInvalid means the description is too long or contains illegal characters.
	CodeDeckDescriptionInvalid = "deck_description_invalid"
	// CodeMediaNotReadable means a note write referenced media the writer cannot read, so the write was rejected (pre-write check).
	// Same value as store.CodeMediaNotReadable.
	CodeMediaNotReadable = "media_not_readable"
	// CodeNoteTypeInUse means a note type still backs at least one live note, so deleting it would orphan that content.
	CodeNoteTypeInUse = "note_type_in_use"
	// CodeNoteTypePurgeUnconfirmed means the note type only backs soft-deleted notes, and the request carried no
	// confirm_purge=true. The response carries the counts so the caller can decide whether to confirm the purge.
	CodeNoteTypePurgeUnconfirmed = "note_type_purge_unconfirmed"
	// CodeCardRemovalUnconfirmed means a note type update would soft-delete at least one card, and the body did not
	// set confirm_card_removal. The response carries the counts so the caller can decide whether to confirm.
	CodeCardRemovalUnconfirmed = "card_removal_unconfirmed"
	// CodeCSRFFailed means the CSRF token is missing or failed validation.
	CodeCSRFFailed = "csrf_failed"
	// CodeCSRFNoSession means there is no active session to validate CSRF against.
	CodeCSRFNoSession = "csrf_no_session"
	// CodeInvalidCredentials means the login credentials are wrong.
	CodeInvalidCredentials = "invalid_credentials"
	// CodeUserDisabled means the account is disabled.
	CodeUserDisabled = "user_disabled"
	// CodeTOTPRequired means TOTP two-factor verification is required.
	CodeTOTPRequired = "totp_required"
	// CodeTOTPInvalid means the code / recovery code submitted in the second login step is invalid.
	CodeTOTPInvalid = "totp_invalid"
	// CodeTOTPChallengeExpired means the second-step credential is missing or expired (the user must log in again from step one).
	CodeTOTPChallengeExpired = "totp_challenge_expired"
	// CodeGradingRequired means a self-grade was submitted for an answer-type card type: such cards are graded by the server only.
	CodeGradingRequired = "grading_required"
	// CodeImportURLInvalid means the direct-link import URL is missing, invalid, not HTTPS, or carries user credentials.
	CodeImportURLInvalid = "deck_import_url_invalid"
	// CodeImportURLBlocked means the URL host resolves to a non-public address (rejected by SSRF protection).
	CodeImportURLBlocked = "deck_import_url_blocked"
	// CodeImportURLFetchFailed means the direct-link download failed: connection / timeout / too many redirects / non-2xx.
	CodeImportURLFetchFailed = "deck_import_url_fetch_failed"
	// CodeImportURLNotPackage means the direct link returned something that is not a deck package (e.g. an HTML page).
	CodeImportURLNotPackage = "deck_import_url_not_package"
	// CodeImportUploadInvalid means the upload ticket does not exist, has expired, was already used, or the account / API key that issued it
	// is no longer valid. The reasons collapse into one code: the remedy is the same (request a new ticket), and it does not reveal the reason to whoever holds the URL.
	CodeImportUploadInvalid = "deck_import_upload_invalid"
)

// errorBody is the error object of the error envelope: {"error":{"code":"...","message":"..."}}.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorMessages is the table of English text for each stable code.
//
// It is the single registry of "code -> English text": both the REST and MCP error exits read from it via ErrorMessage / formatErrorMessage.
// It also covers the codes the store layer's deck package import surfaces (mapPackageError returns them unchanged).
var errorMessages = map[string]string{
	CodeUnauthorized:         "Authentication is required.",
	CodeInvalidAPIKey:        "The API key is invalid, expired, or revoked.",
	CodeScopeRequired:        "This API key does not have the required scope.",
	CodeRateLimited:          "Rate limit exceeded. Try again later.",
	CodeInvalidRequest:       "The request is invalid.",
	CodeNotFound:             "The requested resource was not found.",
	CodeForbidden:            "You do not have access to this resource.",
	CodeInsufficientRole:     "Your role on this deck is not sufficient for this action.",
	CodeScopeNotGrantable:    "Only administrator accounts can be granted the admin scope.",
	CodeConflict:             "The request conflicts with the current state.",
	CodeVersionConflict:      "The resource was changed by someone else. Reload and try again.",
	CodeInternal:             "An internal error occurred.",
	CodeCSRFFailed:           "Invalid or missing CSRF token.",
	CodeCSRFNoSession:        "No active session for CSRF validation.",
	CodeInvalidCredentials:   "Invalid username or password.",
	CodeUserDisabled:         "This account has been disabled.",
	CodeTOTPRequired:         "Two-factor authentication required.",
	CodeTOTPInvalid:          "The two-factor code is not valid.",
	CodeTOTPChallengeExpired: "The two-factor challenge has expired. Sign in again.",
	CodeGradingRequired:      "This card type is graded by the server: send an answer or give up instead of a rating.",

	// Stable codes for direct-link import (POST /decks/import-url).
	CodeImportURLInvalid:     "The URL is not a valid public HTTPS link.",
	CodeImportURLBlocked:     "The URL points to a network address that is not allowed.",
	CodeImportURLFetchFailed: "The package could not be downloaded from the URL.",
	CodeImportURLNotPackage:  "The URL did not return a deck package.",

	// Stable codes for upload-ticket import (PUT /decks/import-uploads/:token).
	CodeImportUploadInvalid: "The upload URL is invalid, expired or already used; request a new one.",

	// Stable codes for deck package import (store.PackageError.Code).
	store.CodePackageUnsafeEntry:     "The package contains an unsafe entry.",
	store.CodePackageTooLarge:        "The package exceeds the allowed size.",
	store.CodePackageBadFormat:       "The package format is invalid.",
	store.CodePackageUnknownKind:     "The package contains a note type this instance does not know.",
	store.CodePackageFormatObsolete:  "This package uses a format this release cannot read. Import it with an older engram release and export it again.",
	store.CodePackageDeckMetaInvalid: "The deck name or description in the package is invalid.",
	store.CodePackageQuotaExceeded:   "The imported media would exceed your media quota.",
	store.CodePackageMediaForbidden:  "The package references media you cannot read.",

	// Stable codes for the pre-write media readability check (store.MediaWriteError.Code).
	CodeMediaNotReadable: "You referenced media you cannot read; upload it first or obtain access.",

	// Stable code for deleting a note type that notes still use.
	CodeNoteTypeInUse: "The note type is still used by one or more notes.",

	// Stable code for deleting a note type whose notes are all soft-deleted.
	CodeNoteTypePurgeUnconfirmed: "The note type is only used by deleted notes; resend the request with confirm_purge=true to delete them permanently.",

	// Stable code for a note type update that would remove cards without confirmation.
	CodeCardRemovalUnconfirmed: "The update would remove cards from existing notes; resend it with confirm_card_removal set.",

	// Stable codes for deck create/rename (store.ErrDeckNameInvalid).
	CodeDeckNameInvalid:        "The deck name is invalid.",
	CodeDeckDescriptionInvalid: "The deck description is invalid.",
}

// defaultErrorMessage returns the English fallback text for a code; unregistered codes return the empty string.
func defaultErrorMessage(code string) string { return errorMessages[code] }

// ErrorMessage returns the stable English text for a code.
// Unregistered codes fall back to the code itself.
func ErrorMessage(_ context.Context, code string) string {
	if msg := errorMessages[code]; msg != "" {
		return msg
	}
	return code
}

// formatErrorMessage assembles the final message: the English base plus optional dynamic detail.
// When the detail is empty, or exactly equal to the code's English text, only the base is returned, avoiding duplication.
func formatErrorMessage(ctx context.Context, code, detail string) string {
	base := ErrorMessage(ctx, code)
	detail = strings.TrimSpace(detail)
	if detail == "" || detail == base || detail == errorMessages[code] {
		return base
	}
	if strings.HasPrefix(detail, base+" — ") {
		return detail
	}
	return base + " — " + detail
}

// ErrorText renders any service error as "code: message", with message in stable English.
// MCP tools have no HTTP error envelope; this keeps their code + English text consistent with REST.
func ErrorText(ctx context.Context, err error) string {
	se := asServiceError(err)
	return se.Code + ": " + formatErrorMessage(ctx, se.Code, se.Message)
}

// abortError writes the unified error envelope and aborts the request; every API error must go through here so the shape does not drift.
//
// An empty message means "take the English text by code"; a non-empty one is dynamic detail supplied by the caller (e.g. a field name,
// a limit value), appended after the English text and never replacing the code's text.
func abortError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": errorBody{
		Code:    code,
		Message: formatErrorMessage(c.Request.Context(), code, message),
	}})
}

// writeServiceError maps a service-layer *ServiceError to the unified error envelope; unrecognized errors become 500.
// REST handlers and MCP tools share the same service methods; this is the REST-side exit.
func writeServiceError(c *gin.Context, err error) {
	se := asServiceError(err)
	abortError(c, se.Status, se.Code, se.Message)
}

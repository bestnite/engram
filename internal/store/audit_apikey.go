package store

// Audit action constants for API key overview writes.
//
// A separate file instead of extending audit.go: audit.go is a shared hotspot and parallel milestones may all
// add constants to it; a constant has the same effect in any file of the package, so splitting avoids needless conflicts.
const (
	// ActionAPIKeyCreate is the audit action written when a user creates their own API key at /settings/keys.
	ActionAPIKeyCreate = "api_key.create"
	// ActionAPIKeyRevoke is the audit action written when the admin panel revokes any user's API key.
	ActionAPIKeyRevoke = "api_key.revoke"
)

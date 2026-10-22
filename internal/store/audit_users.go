package store

// Audit action constants for each user-management write operation.
//
// A separate file instead of extending audit.go: audit.go is a shared hotspot and parallel milestones may all
// add constants to it; a constant has the same effect in any file of the package, so splitting avoids needless conflicts.
const (
	ActionUserDisable       = "user.disable"
	ActionUserEnable        = "user.enable"
	ActionUserRoleChange    = "user.role_change"
	ActionUserPasswordReset = "user.password_reset"
	ActionUserForceLogout   = "user.force_logout"
	ActionUserDelete        = "user.delete"
)

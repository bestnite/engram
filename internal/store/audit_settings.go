package store

// Audit action constants for the personal settings page.
// A separate file instead of extending audit.go: audit.go is a shared hotspot and parallel milestones may all
// add constants to it; a constant has the same effect in any file of the package, so splitting avoids needless conflicts.
const (
	// ActionUserProfileUpdate records a user changing their own display name / language / timezone / review day cutoff.
	ActionUserProfileUpdate = "user.profile_update"
	// ActionUserPasswordChange records a user changing their own password on the settings page.
	ActionUserPasswordChange = "user.password_change"
)

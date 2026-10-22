package main

import (
	"runtime/debug"
	"testing"
)

// TestFormatVersion covers the display version synthesis rules: release builds use the injected tag; local builds with no
// injection use the VCS revision from the build info (its first 7 characters, with -dirty when the worktree has uncommitted
// changes); and when no VCS info can be read it falls back to dev.
func TestFormatVersion(t *testing.T) {
	vcsInfo := func(revision, modified string) *debug.BuildInfo {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: modified},
		}}
	}

	tests := []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		want     string
	}{
		{name: "release tag wins over vcs", injected: "v0.1.4", info: vcsInfo("e223664f0c2d7ade", "true"), want: "v0.1.4"},
		{name: "release tag without build info", injected: "v2.0.0", info: nil, want: "v2.0.0"},
		{name: "dev build uses the short revision", injected: devVersion, info: vcsInfo("e223664f0c2d7ade6f16", "false"), want: "dev (e223664)"},
		{name: "dirty worktree is marked", injected: devVersion, info: vcsInfo("e223664f0c2d7ade6f16", "true"), want: "dev (e223664-dirty)"},
		{name: "empty injection is treated as dev", injected: "", info: vcsInfo("abcdef0123", "false"), want: "dev (abcdef0)"},
		{name: "seven-char revision is kept whole", injected: devVersion, info: vcsInfo("abc1234", "false"), want: "dev (abc1234)"},
		{name: "no build info", injected: devVersion, info: nil, want: devVersion},
		{name: "no vcs settings", injected: devVersion, info: &debug.BuildInfo{}, want: devVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatVersion(tc.injected, tc.info); got != tc.want {
				t.Errorf("formatVersion(%q, %+v) = %q, want %q", tc.injected, tc.info, got, tc.want)
			}
		})
	}
}

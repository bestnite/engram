package main

import (
	"runtime/debug"
	"testing"
)

// TestFormatVersion 覆盖展示版本的合成规则：发布构建用注入的标签；未注入的本地构建用
// 构建信息里的 VCS 修订号（取前 7 位，工作区有未提交改动时带 -dirty）；读不到 VCS 信息时
// 回落到 dev。
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

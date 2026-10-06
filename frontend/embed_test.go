package frontend

import (
	"embed"
	"io/fs"
	"strings"
	"testing"
)

func TestFSContainsBuiltSPAArtifacts(t *testing.T) {
	sub, err := FS()
	if err != nil {
		t.Fatalf("FS() failed: %v", err)
	}
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if !strings.Contains(string(data), `<div id="app"></div>`) {
		t.Errorf("index.html does not contain app mount point: %s", string(data))
	}
	entries, err := fs.ReadDir(sub, "assets")
	if err != nil {
		t.Fatalf("read assets dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("assets dir is empty")
	}
}

func TestSubFSEmptyFSReturnsError(t *testing.T) {
	var emptyFS embed.FS
	if _, err := SubFS(emptyFS); err == nil {
		t.Errorf("SubFS(emptyFS) error = nil, want error")
	}
}

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// F9 读侧兜底：库里可能已存在被旧导入链写入的污染行（mime=text/html、image/svg+xml …）。
// mediaServe 绝不能把这类 mime 原样回成 Content-Type，必须降级为下载/八位流，并带 nosniff。

// TestMediaServeNeutralisesPollutedMime 直接造一行 mime=text/html 的 media 记录与磁盘字节，
// 断言读取响应的 Content-Type 不是可执行类型、强制下载、并带 X-Content-Type-Options: nosniff。
func TestMediaServeNeutralisesPollutedMime(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	root := srv.media.Root()

	body := []byte("<html><body><script>alert(document.domain)</script></body></html>")
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	rel := filepath.Join(sha[:2], sha+".bin")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	// created_by 指向当前登录用户：本用例只关心 mime 兜底，读取鉴权（F2）由 owner 分支放行。
	row := store.Media{Sha256: sha, RelPath: rel, Mime: "text/html", Bytes: int64(len(body)), CreatedBy: store.Ptr(ownerID), CreatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed polluted media row: %v", err)
	}

	get := getWithCookies(t, srv, "/media/"+row.Sha256, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET polluted media status = %d, want 200 (body %s)", get.Code, get.Body.String())
	}
	if ct := get.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream (must not echo text/html)", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", cd)
	}
	if got := get.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	// 内容仍可取回（只是按下载处理），没有把文件吃掉。
	if !strings.Contains(get.Body.String(), "<script>") {
		t.Fatalf("polluted media body lost: %q", get.Body.String())
	}
}

// TestMediaServeKeepsAllowedMediaInline 断言合法图片仍按声明内联返回，新校验没有误伤正常读取。
func TestMediaServeKeepsAllowedMediaInline(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	up := uploadMedia(t, srv, cookies, csrf, "ok.png", "image/png", pngBody())
	if up.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", up.Code, up.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(up.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	get := getWithCookies(t, srv, saved.URL, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", saved.URL, get.Code)
	}
	if ct := get.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("legal image Content-Type = %q, want image/png", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("legal image Content-Disposition = %q, want empty (inline)", cd)
	}
	if got := get.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

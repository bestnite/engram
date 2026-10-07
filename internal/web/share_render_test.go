package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 分享内容的清洗渲染现在经 JSON 传输：GET /api/v1/share/:token 返回服务端清洗后的
// front_html/back_html（shareNoteView → renderSide），SPA 只把这段 HTML 交给 {@html}。
// 本文件锁定两件事：合法 Markdown/Math 以标签形式出现；同一张卡在分享与复习两条通道上
// 渲染出的正文 HTML 逐字相同（证明两者走同一条 internal/render 管线）。

// fetchShareHTML 打开无口令分享链接并取回第一张卡的正反面 HTML。
func fetchShareHTML(t *testing.T, srv *Server, deckID uint64, ownerCookies []*http.Cookie, ownerCSRF string) (front, back string) {
	t.Helper()
	token := createShareLinkJSON(t, srv, deckID, ownerCookies, ownerCSRF, "")
	rec := get(t, srv, "/api/v1/share/"+token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/share/<token> = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body shareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode share content: %v", err)
	}
	if len(body.Notes) == 0 {
		t.Fatalf("share content has no notes: %s", snippet(rec.Body.String()))
	}
	return body.Notes[0].FrontHTML, body.Notes[0].BackHTML
}

// TestShareBrowseRendersSanitizedCardHTML 锁定 F21：分享内容把卡面正文当已清洗 HTML 注入，
// 用户看到 <span> 而不是 &lt;span&gt;。它复用复习页同一套 internal/render.RenderMarkdown。
func TestShareBrowseRendersSanitizedCardHTML(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Render deck")
	front := `<span class="hl">高亮</span> 与 **加粗** 与 \(x+y\)`
	seedBasic(t, db, deck.ID, front, "背面")

	frontHTML, _ := fetchShareHTML(t, srv, deck.ID, ownerCookies, ownerCSRF)

	// 合法标记必须以标签形式出现。
	for _, want := range []string{`<span class="hl">高亮</span>`, `<strong>加粗</strong>`, `\(x+y\)`} {
		if !strings.Contains(frontHTML, want) {
			t.Errorf("shared card body missing rendered markup %q:\n%s", want, frontHTML)
		}
	}
	// 且绝不能再出现被转义的标签或未转换的 Markdown。
	for _, bad := range []string{"&lt;span", "&lt;strong", "&quot;hl&quot;", "**加粗**"} {
		if strings.Contains(frontHTML, bad) {
			t.Errorf("shared card body still shows escaped/raw source %q:\n%s", bad, frontHTML)
		}
	}
}

// TestShareBrowseSanitizesUnsafeCardHTML 是 F21 的安全负例端到端：
// 同一张卡的字段里放脚本、事件属性与 F8 的公式绕过样本，分享与复习两条通道都不得出现可执行形式，
// 且两条通道渲染出的正文 HTML 完全一致。
//
// 清洗本身由 internal/render 的用例穷尽覆盖：
//   - TestRenderMarkdownStripsUnsafe（script / onerror / javascript: URL）
//   - TestRenderMarkdownNeutralizesMathAttributeEscape（F8：公式还原后的属性逃逸）
func TestShareBrowseSanitizesUnsafeCardHTML(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Unsafe deck")
	front := strings.Join([]string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<span class="\(a"onmouseover="alert(2)//\)">xss-span</span>`,
		`[x](\(javascript:alert(3)\))`,
	}, "\n\n")
	back := `**背面** 与 \(a<b\)`
	note := seedBasic(t, db, deck.ID, front, back)
	cardID := cardIDOfNote(t, db, note.ID)

	shareFront, shareBack := fetchShareHTML(t, srv, deck.ID, ownerCookies, ownerCSRF)

	renderRec := postSPAJSON(t, srv, "/api/v1/review/render",
		map[string]any{"card_id": cardID, "deck": []uint64{deck.ID}}, ownerCookies, ownerCSRF)
	if renderRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/review/render = %d, want 200 (body %s)", renderRec.Code, snippet(renderRec.Body.String()))
	}
	var renderBody struct {
		FrontHTML string `json:"front_html"`
		BackHTML  string `json:"back_html"`
	}
	if err := json.Unmarshal(renderRec.Body.Bytes(), &renderBody); err != nil {
		t.Fatalf("decode render response: %v", err)
	}

	for name, frag := range map[string]string{
		"share front": shareFront, "share back": shareBack,
		"review front": renderBody.FrontHTML, "review back": renderBody.BackHTML,
	} {
		for _, bad := range []string{"<script", "onerror", "onmouseover", "onclick", "javascript:", "alert("} {
			if strings.Contains(frag, bad) {
				t.Errorf("%s still contains executable form %q:\n%s", name, bad, frag)
			}
		}
	}
	// 内容不得被整段吞掉：合法残留与文本都要在。
	for _, want := range []string{"xss-span", `<img src="x"`} {
		if !strings.Contains(shareFront, want) {
			t.Errorf("share front lost benign content %q:\n%s", want, shareFront)
		}
	}
	// 一致性：同一 note，分享与复习的正文 HTML 逐字相同。
	if shareFront != renderBody.FrontHTML {
		t.Errorf("share/review front HTML differ:\nshare=%q\nreview=%q", shareFront, renderBody.FrontHTML)
	}
	if shareBack != renderBody.BackHTML {
		t.Errorf("share/review back HTML differ:\nshare=%q\nreview=%q", shareBack, renderBody.BackHTML)
	}
}

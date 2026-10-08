package render

import (
	"strings"
	"testing"
)

// TestRenderMarkdownLocksAttributeWhitelist 锁定卡面的属性白名单（buildPolicy）。
//
// 为什么必须有这组用例：清洗后的 HTML 直接进卡面渲染，属性白名单是唯一的屏障。能执行代码的
// 载体不止 on* 一类——hx-*/data-hx-* 前缀（htmx 那类库会把它们交给 new Function 编译）、
// style（覆盖站点样式）、srcdoc（承载完整文档）都曾是真实存在的注入口，白名单放宽一条就是
// 开一个口子。所以逐条钉死，而不是只依赖「我们记得白名单是严的」。
//
// 每条用例同时断言两件事：forbidden 里的可执行形式不得出现在输出中；
// required 里的合法标签与文字必须保留，证明剥属性不等于吞内容。
// 另外对每条输出调用 assertSafeFragment，按 HTML 解析复核元素与属性都在白名单内、
// 没有 on* 事件属性、URL 属性没有脚本类 scheme——这是对 hx-*/data-hx-*/on*/style
// 四类属性的结构性封锁，字符串断言一旦被实体编码绕过它仍然会失败。
func TestRenderMarkdownLocksAttributeWhitelist(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		forbidden []string
		required  []string
	}{
		{
			// 2. img 的事件处理属性onerror。
			// 与 render_test.go 里那条 `src="javascript:…" + onerror` 不同：这条要求剥掉
			// onerror 之后 img 与它合法的 src 仍然存活。
			name:      "img onerror event attribute",
			src:       `<img src=x onerror=alert(1)>`,
			forbidden: []string{"onerror", "alert("},
			required:  []string{"<img", `src="x"`},
		},
		{
			// 3. div 不在白名单，其 onclick 必须随之消失，但“x”文字要留下。
			name:      "div onclick with non-whitelisted element",
			src:       `<div onclick="alert(1)">x</div>`,
			forbidden: []string{"onclick", "<div", "alert("},
			required:  []string{"x"},
		},
		{
			// 4a. htmx 的 hx-on: 单冒号形式：属性必须被剥除。
			name:      "span hx-on colon attribute",
			src:       `<span hx-on:click="alert(1)">x</span>`,
			forbidden: []string{"hx-", "alert("},
			required:  []string{"<span>x</span>"},
		},
		{
			// 4b. htmx 的 hx-on:: 双冒号形式（after-swap 等生命周期事件）。
			name:      "span hx-on double colon attribute",
			src:       `<span hx-on::after-swap="alert(1)">x</span>`,
			forbidden: []string{"hx-", "alert("},
			required:  []string{"<span>x</span>"},
		},
		{
			// 5. htmx 同时认 data-hx-* 前缀，必须与 hx-* 一并封死。
			name:      "span data-hx-get attribute",
			src:       `<span data-hx-get="/x">x</span>`,
			forbidden: []string{"data-hx-", "hx-", `"/x"`},
			required:  []string{"<span>x</span>"},
		},
		{
			// 6. 内联样式会让内容有机会覆盖站点样式，必须剥除。
			name:      "span style attribute",
			src:       `<span style="color:red">x</span>`,
			forbidden: []string{"style=", "color:red"},
			required:  []string{"<span>x</span>"},
		},
		{
			// 7a. iframe 不在白名单：标签连内容一并丢弃（同一条向量在 render_test.go 里
			// 也有一条，这里是属性白名单视角，保留是因为它同时断言 src 值不得留下）。
			name:      "iframe not in whitelist",
			src:       `<iframe src="https://example.com"></iframe>`,
			forbidden: []string{"<iframe", "example.com"},
			required:  nil,
		},
		{
			// 7b. svg 不在白名单，其 onload 无从存活。
			name:      "svg onload not in whitelist",
			src:       `<svg onload=alert(1)>`,
			forbidden: []string{"<svg", "onload", "alert("},
			required:  nil,
		},
		{
			// 7c. math 不在白名单（MathJax 只吃文本分隔符，不需要 math 标签）。
			name:      "math not in whitelist",
			src:       `<math><mtext></mtext></math>`,
			forbidden: []string{"<math", "<mtext"},
			required:  nil,
		},
		{
			// 8a. Markdown 链接形式的 javascript: 目标。
			name:      "markdown link javascript target",
			src:       `[x](javascript:alert(1))`,
			forbidden: []string{"javascript:", "href="},
			required:  []string{"x"},
		},
		{
			// 8b. Markdown 图片形式的 javascript: 目标：src 被剥，img 与 alt 保留。
			name:      "markdown image javascript target",
			src:       `![](javascript:alert(1))`,
			forbidden: []string{"javascript:", "src="},
			required:  []string{"<img", `alt=""`},
		},
		{
			// 9. srcdoc 是另一个可承载完整 HTML 文档的载体，必须剥除。
			name:      "iframe srcdoc attribute",
			src:       `<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
			forbidden: []string{"srcdoc", "<iframe", "<script", "alert("},
			required:  nil,
		},
		{
			// 10. 正向对照：白名单内的合法属性必须原样保留，
			// 证明上面每一条的“剥离”来自白名单收紧，而不是把合法内容也一起清掉。
			name: "allowed attributes survive",
			src: `<span class="hl">a</span>` +
				` <a href="https://example.com">b</a>` +
				` <img src="/media/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" alt="a">` +
				` <ol start="3"><li>c</li></ol>`,
			forbidden: nil,
			required: []string{
				`<span class="hl">a</span>`,
				`<a href="https://example.com">b</a>`,
				`<img src="/media/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" alt="a">`,
				`<ol start="3">`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			for _, bad := range tc.forbidden {
				if strings.Contains(out, bad) {
					t.Errorf("output still contains forbidden %q:\n%s", bad, out)
				}
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output lost required %q:\n%s", want, out)
				}
			}
			assertSafeFragment(t, out)
		})
	}
}

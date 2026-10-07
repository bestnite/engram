package mail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/render"
)

// 本文件是邮件模板的渲染器（DESIGN.md §4.7）。
//
// 渲染分四步，顺序是安全的一部分，不要调换：
//  1. 把模板里的 {{var}} 换成当次随机盐的令牌；
//  2. 渲染 Markdown 并过白名单清洗；
//  3. 把令牌替换成值，HTML 段按 HTML 转义；
//  4. 包进代码持有的固定外壳。
//
// **为什么必须先渲染再替换**：变量值里有用户可控的数据（用户名、卡组名、失败原因）。
// 先在原文里替换，就等于把用户数据交给 Markdown 解析器与白名单判断——值是 `**x**` 会被
// 当成粗体，是 `<b>x</b>` 会被当成原始 HTML。先渲染则值从来没进过渲染器，替换时按上下文
// 转义即可。这正是卡面公式占位符踩过的那条教训（DESIGN.md §6.1），邮件同理。

// placeholderRe 匹配模板里的 {{name}}。名字限定为小写字母、数字与下划线：这样模板里的
// 数学写法（`\frac{x}{y}` 那类单层花括号）不会被误判，也逼着变量名保持稳定英文标识。
var placeholderRe = regexp.MustCompile(`\{\{([a-z0-9_]+)\}\}`)

// Vars 是一封邮件的变量值。值是不可信内容，渲染时会按上下文转义。
type Vars map[string]string

// RenderInput 是一次渲染的全部输入。
type RenderInput struct {
	// Type 决定必填变量集合（见 vars.go）。
	Type Type
	// Site 是站点名，进外壳标题。
	Site string
	// Vars 是变量值。
	Vars Vars

	// Subject 与 BodyMD 是管理员的自定义模板。BodyMD 为空白表示没有自定义，
	// 此时用 FallbackSubject/FallbackText（发信方组装的内置正文）——**内置正文永远
	// 在代码里**，所以模板损坏或被删都不会让邮件发不出去。
	Subject string
	BodyMD  string

	// FallbackSubject 与 FallbackText 是内置正文。
	FallbackSubject string
	FallbackText    string

	// UnsubscribeURL 非空时在外壳底部与纯文本段末尾渲染退订入口（可选类邮件才有）。
	UnsubscribeURL string
	// FooterNote 与 UnsubscribeLabel 由调用方本地化后传入：本包不依赖 i18n，
	// 而邮件里的用户可见文案必须走语言包（AGENTS.md §2.1）。
	FooterNote       string
	UnsubscribeLabel string
}

// Rendered 是渲染结果：主题、纯文本段、HTML 段。HTML 段为空表示这封信只发纯文本。
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// Render 渲染一封邮件。没有自定义模板时返回内置正文（HTML 段为空，与今天的行为一致）。
func Render(in RenderInput) (Rendered, error) {
	if strings.TrimSpace(in.BodyMD) == "" {
		return Rendered{Subject: in.FallbackSubject, Text: in.FallbackText}, nil
	}
	if err := checkRequiredVars(in.Type, in.Vars); err != nil {
		return Rendered{}, err
	}

	// 主题：模板没写就沿用内置主题；主题必须是单行，否则会破坏 RFC 5322 头部。
	subject := strings.TrimSpace(substituteRaw(in.Subject, in.Vars))
	subject = strings.ReplaceAll(subject, "\r", " ")
	subject = strings.ReplaceAll(subject, "\n", " ")
	if subject == "" {
		subject = in.FallbackSubject
	}

	tokenised, order, salt := tokenise(in.BodyMD)
	bodyHTML, err := render.RenderMarkdown(tokenised)
	if err != nil {
		return Rendered{}, fmt.Errorf("render mail template: %w", err)
	}
	htmlBody := wrapLayout(in.Site, substituteTokens(bodyHTML, order, salt, in.Vars, true), in.UnsubscribeURL, in.FooterNote, in.UnsubscribeLabel)

	text := substituteRaw(in.BodyMD, in.Vars)
	if in.UnsubscribeURL != "" && in.UnsubscribeLabel != "" {
		text = strings.TrimRight(text, "\n") + "\n\n" + in.UnsubscribeLabel + ": " + in.UnsubscribeURL + "\n"
	}
	return Rendered{Subject: subject, Text: text, HTML: htmlBody}, nil
}

// Placeholders 返回模板里出现的变量名（去重、按首次出现排序），供管理页做校验与提示。
func Placeholders(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// ValidateBody 校验自定义正文：必填变量必须出现，且不得引用该类型不认识的变量。
// 「认识但没写」不算错（模板可以只用一部分），「写了却不认识」一定是打错了字，两种都
// 挡在保存这一步，而不是等邮件发出去才发现链接是空的。
func ValidateBody(t Type, body string) error {
	specs, ok := VarSpecs(t)
	if !ok {
		return fmt.Errorf("mail type %q does not support templates", t)
	}
	known := map[string]bool{}
	for _, s := range specs {
		known[s.Name] = true
	}
	used := Placeholders(body)
	for _, name := range used {
		if !known[name] {
			return fmt.Errorf("unknown variable {{%s}}", name)
		}
	}
	usedSet := map[string]bool{}
	for _, name := range used {
		usedSet[name] = true
	}
	for _, name := range RequiredVars(t) {
		if !usedSet[name] {
			return fmt.Errorf("required variable {{%s}} is missing", name)
		}
	}
	return nil
}

// checkRequiredVars 断言发信方把必填变量都传了。缺值只可能是代码 bug（模板缺变量已在
// 保存时拦住），所以这里返回错误让调用方记日志并且**不发信**——发一封没有重置链接的邮件
// 比不发更糟：用户会以为链接坏了。
func checkRequiredVars(t Type, vars Vars) error {
	for _, name := range RequiredVars(t) {
		if strings.TrimSpace(vars[name]) == "" {
			return fmt.Errorf("mail type %q: required variable %q is empty", t, name)
		}
	}
	return nil
}

// tokenise 把 {{var}} 换成带随机盐的令牌，并返回按令牌序号排列的变量名。
//
// 盐是必需的：否则管理员在正文里写字面量 `MAILVARX0X` 就能顶替一个变量。
func tokenise(src string) (string, []string, string) {
	salt := randomSalt()
	var order []string
	out := placeholderRe.ReplaceAllStringFunc(src, func(match string) string {
		name := placeholderRe.FindStringSubmatch(match)[1]
		order = append(order, name)
		return tokenPrefix + salt + "X" + strconv.Itoa(len(order)-1) + "X"
	})
	return out, order, salt
}

const tokenPrefix = "MAILVAR"

// substituteTokens 把令牌换成值。escape 为真时按 HTML 转义——HTML 段必须转义，纯文本段
// 不转义（否则 `&` 会变成 `&amp;` 出现在纯文本里）。
//
// 盐由调用方从 tokenise 取回并逐字传入，不从 src 里回捞：回捞要在替换过程中反复扫描自己
// 正在改写的字符串，最后一个令牌换掉后就再也找不到盐了。
func substituteTokens(src string, order []string, salt string, vars Vars, escape bool) string {
	for i, name := range order {
		value := vars[name]
		if escape {
			value = html.EscapeString(value)
		}
		src = strings.ReplaceAll(src, tokenPrefix+salt+"X"+strconv.Itoa(i)+"X", value)
	}
	return src
}

// substituteRaw 在**原文**上直接替换变量，用于主题与纯文本段（不经渲染器与转义）。
func substituteRaw(src string, vars Vars) string {
	return placeholderRe.ReplaceAllStringFunc(src, func(match string) string {
		return vars[placeholderRe.FindStringSubmatch(match)[1]]
	})
}

func randomSalt() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// 盐只用于避免正文里的字面量撞车，取不到随机源时退化成固定值也不会降低安全性
		//（转义才是防线），但记一条日志便于排查。
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// wrapLayout 把渲染后的正文包进固定外壳。
//
// 外壳由代码持有、管理员不可编辑：页头页脚、宽度、字体栈与退订入口都在这里，一次误编辑
// 不会破坏品牌一致性或把退订入口弄丢。样式一律内联——邮件客户端普遍不支持 <style>。
func wrapLayout(site, bodyHTML, unsubscribeURL, footerNote, unsubscribeLabel string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="margin:0;padding:0;background:#f4f4f5;">`)
	b.WriteString(`<div style="max-width:600px;margin:0 auto;padding:24px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:15px;line-height:1.6;color:#18181b;">`)
	if site != "" {
		b.WriteString(`<div style="font-size:13px;font-weight:600;letter-spacing:.02em;color:#71717a;padding-bottom:12px;">`)
		b.WriteString(html.EscapeString(site))
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div style="background:#ffffff;border:1px solid #e4e4e7;border-radius:12px;padding:24px;">`)
	b.WriteString(bodyHTML)
	b.WriteString(`</div>`)
	if footerNote != "" || unsubscribeURL != "" {
		b.WriteString(`<div style="padding-top:16px;font-size:12px;line-height:1.6;color:#71717a;">`)
		if footerNote != "" {
			b.WriteString(html.EscapeString(footerNote))
		}
		if unsubscribeURL != "" && unsubscribeLabel != "" {
			if footerNote != "" {
				b.WriteString(`<br />`)
			}
			b.WriteString(`<a href="` + html.EscapeString(unsubscribeURL) + `" style="color:#71717a;">` + html.EscapeString(unsubscribeLabel) + `</a>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

package mail

import (
	"log/slog"
	"strings"
)

// 本文件实现模板的**回退链**与「渲染失败退回内置正文」这条保险。

// ResolvedTemplate 是回退链前两级的结果；两者都为空表示没有自定义模板，用内置正文。
type ResolvedTemplate struct {
	Subject string
	Body    string
}

// LookupFunc 按 (类型, 语言) 取自定义模板；ok 为 false 表示该键没有自定义。
//
// 做成函数类型而不是接口：本包只关心「能不能取到」，存储形态（GORM 表、缓存、测试替身）
// 由调用方决定。
type LookupFunc func(t Type, locale string) (subject, body string, ok bool)

// Resolve 走回退链的前两级：先精确语言，再站点默认语言。
//
// 第三级是内置正文，不在本函数里——它由调用方作为 RenderInput 的 Fallback 传入。这样
// 「内置默认永远在代码里」是结构事实，而不是本函数的一条分支。
func Resolve(lookup LookupFunc, t Type, locale, siteDefault string) ResolvedTemplate {
	if lookup == nil {
		return ResolvedTemplate{}
	}
	if subject, body, ok := lookup(t, locale); ok {
		return ResolvedTemplate{Subject: subject, Body: body}
	}
	// 语言与站点默认相同时不重复查一次。
	if siteDefault != "" && siteDefault != locale {
		if subject, body, ok := lookup(t, siteDefault); ok {
			return ResolvedTemplate{Subject: subject, Body: body}
		}
	}
	return ResolvedTemplate{}
}

// RenderOrFallback 渲染模板；**渲染失败或必填变量缺失时退回内置正文**。
//
// 这条路径兑现「模板坏了也不会让邮件发不出去」的承诺：管理员把模板写进
// 一段渲染不出来的内容（或发信方漏传了必填变量）时，收件人拿到的仍是内置正文，而不是
// 一封空邮件或一个 500。失败一定记日志——静默降级会让模板问题永远没人发现。
func RenderOrFallback(in RenderInput) Rendered {
	// 模板内容先于渲染校验：行可能被直写库进来，或早于某个变量变成必填，而「丢掉了重置
	// 链接的模板」必须退到内置正文，不能发出一封没用的信。校验放在这一层而不是 Render 里：
	// Render 是纯渲染（只回答「这段 Markdown 渲染成什么」），策略归调用发信的那一层。
	if strings.TrimSpace(in.BodyMD) != "" {
		if err := Validate(in.Type, in.Subject, in.BodyMD); err != nil {
			slog.Error("mail template is invalid, falling back to the built-in body",
				"mail_type", string(in.Type), "error", err)
			return Rendered{Subject: in.FallbackSubject, Text: in.FallbackText}
		}
	}
	out, err := Render(in)
	if err == nil {
		return out
	}
	slog.Error("mail template render failed, falling back to the built-in body",
		"mail_type", string(in.Type), "error", err)
	return Rendered{Subject: in.FallbackSubject, Text: in.FallbackText}
}

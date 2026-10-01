// Package i18n 加载 YAML 语言包、按请求解析语言，并把本地化器放进请求 context。
//
// 边界（DESIGN.md §8.3、§10.4）：本包只负责"面向最终用户"的文案。日志恒为英文，
// 不从这里取值；错误 code 用英文常量，也不在这里定义。
package i18n

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// localesFS 把语言包打进二进制（DESIGN.md §10.2：语言包随二进制分发）。
//
//go:embed locales/*.yaml
var localesFS embed.FS

// localesDir 是嵌入FS里语言包所在的目录名。
const localesDir = "locales"

// DefaultLocaleCode 是站点默认语言；语言解析全部失败时用它（DESIGN.md §8.3）。
const DefaultLocaleCode = "zh-CN"

// supportedCodes 是首发语言，顺序即"语言切换器"的展示顺序。
// 新增语言 = 加一个 locales/<code>.yaml 并把 code 追加到这里（DESIGN.md §8.3）。
var supportedCodes = []string{"zh-CN", "en"}

// Translator 持有已加载的语言包和受支持语言的匹配信息。
type Translator struct {
	bundle     *goi18n.Bundle
	tags       map[string]language.Tag
	codes      []string
	defaultTag language.Tag
	// ids 记录每个语言包的 message id 集合，用于加载期的完备性检查。
	ids map[string]map[string]bool
}

// New 从嵌入的语言包构造 Translator。
func New() (*Translator, error) {
	return NewFromFS(localesFS, localesDir)
}

// NewFromFS 从任意文件系统读取 <dir>/*.yaml 语言包；文件名（去掉 .yaml）即语言码。
func NewFromFS(fsys fs.FS, dir string) (*Translator, error) {
	bundle := goi18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locale dir %q: %w", dir, err)
	}
	loaded := make(map[string]bool)
	ids := make(map[string]map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		file := path.Join(dir, e.Name())
		if _, err := bundle.LoadMessageFileFS(fsys, file); err != nil {
			return nil, fmt.Errorf("i18n: load locale file %q: %w", e.Name(), err)
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("i18n: read locale file %q: %w", e.Name(), err)
		}
		code := strings.TrimSuffix(e.Name(), ".yaml")
		if ids[code], err = messageIDs(data); err != nil {
			return nil, fmt.Errorf("i18n: parse locale file %q: %w", e.Name(), err)
		}
		loaded[code] = true
	}

	t := &Translator{bundle: bundle, tags: make(map[string]language.Tag), ids: ids}
	for _, code := range supportedCodes {
		if !loaded[code] {
			return nil, fmt.Errorf("i18n: supported locale %q has no catalog file", code)
		}
		tag, err := language.Parse(code)
		if err != nil {
			return nil, fmt.Errorf("i18n: parse locale %q: %w", code, err)
		}
		t.tags[code] = tag
		t.codes = append(t.codes, code)
	}
	defaultTag, ok := t.tags[DefaultLocaleCode]
	if !ok {
		return nil, fmt.Errorf("i18n: default locale %q is not supported", DefaultLocaleCode)
	}
	t.defaultTag = defaultTag
	// 缺 key 是硬性缺陷，在加载期就失败，避免未翻译文案漏到页面上（AGENTS.md §4）。
	if err := t.CheckParity(); err != nil {
		return nil, err
	}
	return t, nil
}

// CheckParity 断言每个语言包的 key 集合与默认语言完全一致。
// 返回的任一错误都说明语言包需要补齐或删减。
func (t *Translator) CheckParity() error {
	base := t.ids[DefaultLocaleCode]
	for _, code := range t.codes {
		for id := range base {
			if !t.ids[code][id] {
				return fmt.Errorf("i18n: locale %q is missing message %q", code, id)
			}
		}
		for id := range t.ids[code] {
			if !base[id] {
				return fmt.Errorf("i18n: locale %q has message %q that %q lacks", code, id, DefaultLocaleCode)
			}
		}
	}
	return nil
}

// messageIDs 解析一个 YAML 语言包里的 message id 集合（只用于完备性检查）。
func messageIDs(data []byte) (map[string]bool, error) {
	var msgs []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			out[m.ID] = true
		}
	}
	return out, nil
}

// SupportedCodes 返回受支持的语言码副本，顺序稳定。
func (t *Translator) SupportedCodes() []string {
	out := make([]string, len(t.codes))
	copy(out, t.codes)
	return out
}

// DefaultCode 返回站点默认语言码。
func (t *Translator) DefaultCode() string { return DefaultLocaleCode }

// Pick 按固定优先级选出请求语言：requested（?lang 显式覆盖）> userLocale（用户设置）
// > acceptLanguage > 站点默认。某一级给出的是不受支持的语言时跳到下一级，而不是做
// 就近匹配——否则用户把语言设成 "de" 会压过 Accept-Language 里的 "en"。
func (t *Translator) Pick(requested, userLocale, acceptLanguage string) language.Tag {
	for _, raw := range []string{requested, userLocale} {
		if raw == "" {
			continue
		}
		if tag, err := language.Parse(raw); err == nil {
			if matched, ok := t.match(tag); ok {
				return matched
			}
		}
	}
	if acceptLanguage != "" {
		if tags, _, err := language.ParseAcceptLanguage(acceptLanguage); err == nil {
			for _, tag := range tags {
				if matched, ok := t.match(tag); ok {
					return matched
				}
			}
		}
	}
	return t.defaultTag
}

// match 只接受与受支持语言同基的标签，并映射到受支持的规范标签（en-US→en、zh-TW→zh-CN）。
func (t *Translator) match(tag language.Tag) (language.Tag, bool) {
	base, _ := tag.Base()
	for _, code := range t.codes {
		want, _ := t.tags[code].Base()
		if base.String() == want.String() {
			return t.tags[code], true
		}
	}
	return language.Tag{}, false
}

// Localizer 绑定一种语言，供 handler 与模板取值。
type Localizer struct {
	code string
	tag  language.Tag
	lc   *goi18n.Localizer
}

// Localizer 为给定语言构造本地化器；不受支持的标签会落到站点默认语言。
func (t *Translator) Localizer(tag language.Tag) *Localizer {
	code := DefaultLocaleCode
	base, _ := tag.Base()
	for _, c := range t.codes {
		want, _ := t.tags[c].Base()
		if base.String() == want.String() {
			code = c
			break
		}
	}
	return &Localizer{code: code, tag: tag, lc: goi18n.NewLocalizer(t.bundle, code)}
}

// Locale 返回本本地化器实际使用的规范语言码。
func (l *Localizer) Locale() string { return l.code }

// Tag 返回请求选择的原始语言标签。
func (l *Localizer) Tag() language.Tag { return l.tag }

// T 返回 msgID 对应的译文。缺 key 是 bug：记一条英文日志并回退成 key 本身，
// 让缺失在页面上一眼可见；语言包完备性由单测兜底（AGENTS.md §2.1、§4）。
func (l *Localizer) T(msgID string) string {
	msg, err := l.lc.Localize(&goi18n.LocalizeConfig{MessageID: msgID})
	if err != nil {
		slog.Error("i18n: missing message", "message_id", msgID, "locale", l.code)
		return msgID
	}
	return msg
}

// Tf 与 T 相同，但把 data 作为模板数据代入带占位符的译文（例如“共 {{.count}} 张”）。
// 占位符语法由 go-i18n 的 text/template 决定；缺 key 的处理与 T 一致。
func (l *Localizer) Tf(msgID string, data map[string]any) string {
	msg, err := l.lc.Localize(&goi18n.LocalizeConfig{MessageID: msgID, TemplateData: data})
	if err != nil {
		slog.Error("i18n: missing message", "message_id", msgID, "locale", l.code)
		return msgID
	}
	return msg
}

// localizerKey 是本包私有的 context key，避免与其他包冲突。
type localizerKey struct{}

// WithLocalizer 把本地化器放进请求 context。
func WithLocalizer(ctx context.Context, l *Localizer) context.Context {
	return context.WithValue(ctx, localizerKey{}, l)
}

// FromContext 取出请求 context 里的本地化器；不存在时返回 nil。
func FromContext(ctx context.Context) *Localizer {
	l, _ := ctx.Value(localizerKey{}).(*Localizer)
	return l
}

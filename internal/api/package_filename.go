package api

import (
	"mime"
	"strings"
	"unicode"
)

// packageFilenameMaxRunes 限制下载文件名主体的长度：卡组名最长 200 字符，原样拼进文件名
// 会超出部分文件系统 255 字节的上限（中文一个字符占 3 字节）。
const packageFilenameMaxRunes = 80

// DeckPackageFilename 返回导出卡组包时的下载文件名：卡组标题 + .edeck。
//
// 标题里在常见文件系统上不合法或有特殊含义的字符（路径分隔符、Windows 保留字符、控制字符）
// 换成下划线，连续空白压成一个空格，首尾的空格和点去掉（Windows 不允许以点或空格结尾）。
// 清洗后为空时退回 deck-<对外 id>，保证总有一个可用的名字；文件名里从不出现自增主键。
func DeckPackageFilename(deckName, publicID string) string {
	var b strings.Builder
	space := false
	for _, r := range deckName {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r):
			r = '_'
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
	}
	name := strings.Trim(b.String(), " .")
	if runes := []rune(name); len(runes) > packageFilenameMaxRunes {
		name = strings.TrimRight(string(runes[:packageFilenameMaxRunes]), " .")
	}
	if name == "" {
		name = "deck-" + publicID
	}
	return name + ".edeck"
}

// AttachmentDisposition 生成下载用的 Content-Disposition。文件名含非 ASCII 字符（如中文卡组名）时
// 按 RFC 2231/6266 编码成 filename* 的 UTF-8 百分号转义形式，由标准库负责转义，不手拼引号。
func AttachmentDisposition(filename string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

package store

import (
	"context"
	"fmt"
	"regexp"

	"gorm.io/gorm"
)

// 每用户媒体总量配额（DESIGN.md §6.3、§13 #6；AGENTS.md M2-13）的计量口径。
//
// 计量：一个用户的「已用媒体量」= 该用户拥有的 note（notes.created_by = 该用户，且
// 未被软删除）所引用到的媒体，按 sha256 去重后求字节数之和。note 字段里的引用可能是
// 编辑器写入的 Markdown 图片 URL `/media/<sha256>`，也可能是卡组包里的 `media/<sha256>.<ext>`；
// 两种形态都识别。
//
// 为什么按「引用」而不是按 media.created_by（上传者）计费：
//   - 媒体按 sha256 内容寻址，同一份字节在全库只存一份。若按上传者计费，共享的 blob 只会
//     记在“第一个上传者”名下，之后所有引用它的人都不计费——既不反映谁在占用空间，也让
//     “删掉自己的引用”根本无法释放配额。
//   - 按引用计量天然满足两条硬要求：① 删除或替换引用即释放配额（note 软删除同样释放），
//     无需真删 blob，也就不会误删别人仍在引用的文件；② 同一 blob 被同一用户的多张 note
//     引用只计一次（按 sha256 去重），不会重复计费。
//   - 多个用户各自引用同一个 blob 时，每个用户各计一次：每个用户都能独立访问这份字节，
//     这是逻辑归属。磁盘物理上仍只有一份（内容寻址去重），所以配额不等于磁盘占用。
//
// 已知边界：上传后尚未插入任何卡片的文件不计入用量（它还没有引用）。配额在“引用”层而非
// “上传”层，这是与被计费对象一致的选择；上传端点的超限检查会把“本次将新增的字节”算进去，
// 因此已经在用满配额的账号传新文件仍会被挡下。
type MediaUsage struct {
	// Bytes 是去重后的已用媒体字节数。
	Bytes int64
	// Sha256 是该用户已计费的媒体集合（sha256 → true），用于上传时判断新文件是否已被计费。
	Sha256 map[string]bool
}

// mediaRefByURLRE 匹配编辑器写入的媒体 URL `/media/<sha256>`（同一字段里可能出现多次）。
// 左右用非十六进制字符界定，避免把更长的十六进制串截成 64 位而误配。
var mediaRefByURLRE = regexp.MustCompile(`(?:^|[^0-9a-f])/media/([0-9a-f]{64})(?:[^0-9a-f]|$)`)

// mediaRefByShaRE 匹配卡组包形态的 `media/<sha256>.<ext>`；左右用非十六进制字符界定，
// 避免把更长的串误当 sha256。
var mediaRefByShaRE = regexp.MustCompile(`(?:^|[^0-9a-f])media/([0-9a-f]{64})\.([A-Za-z0-9]+)`)

// UserMediaUsage 计算用户已用媒体量（口径见 MediaUsage 注释）。db 为空时返回空用量。
func UserMediaUsage(ctx context.Context, db *gorm.DB, userID uint64) (MediaUsage, error) {
	usage := MediaUsage{Sha256: map[string]bool{}}
	if db == nil || userID == 0 {
		return usage, nil
	}
	// GORM 默认作用域已排除软删除的 note：删 note 即释放其引用。
	var notes []Note
	if err := db.WithContext(ctx).Where("created_by = ?", userID).Find(&notes).Error; err != nil {
		return usage, fmt.Errorf("store: list user notes for media usage: %w", err)
	}
	shas := map[string]bool{}
	for i := range notes {
		fields, err := ParseFields(notes[i].FieldsJSON)
		if err != nil {
			// 坏字段不阻塞配额计算：跳过该 note，由调用方在别处报告内容问题。
			continue
		}
		scanMediaRefs(fields, shas)
	}

	// 去重后一次性取回媒体行；同一 sha256 无论被几张 note 引用都只累加一次。
	bySha := map[string]Media{}
	if len(shas) > 0 {
		list := make([]string, 0, len(shas))
		for sha := range shas {
			list = append(list, sha)
		}
		var rows []Media
		if err := db.WithContext(ctx).Where("sha256 IN ?", list).Find(&rows).Error; err != nil {
			return usage, fmt.Errorf("store: load media by sha256 for usage: %w", err)
		}
		for _, m := range rows {
			bySha[m.Sha256] = m
		}
	}
	for sha, m := range bySha {
		usage.Sha256[sha] = true
		usage.Bytes += m.Bytes
	}
	return usage, nil
}

// scanMediaRefs 递归扫描 note 字段，收集其中的媒体 sha256 引用（URL 形态与包内形态都识别）。
func scanMediaRefs(v any, shas map[string]bool) {
	switch x := v.(type) {
	case string:
		for _, m := range mediaRefByURLRE.FindAllStringSubmatch(x, -1) {
			shas[m[1]] = true
		}
		for _, m := range mediaRefByShaRE.FindAllStringSubmatch(x, -1) {
			shas[m[1]] = true
		}
	case map[string]any:
		for _, child := range x {
			scanMediaRefs(child, shas)
		}
	case []any:
		for _, child := range x {
			scanMediaRefs(child, shas)
		}
	}
}

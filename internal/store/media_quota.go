package store

import (
	"context"
	"fmt"
	"regexp"

	"gorm.io/gorm"
)

// 每用户媒体总量配额的计量口径。
//
// 「已用媒体量」= 下面两个集合的并集按 sha256 去重后的字节数之和：
//  1. 该用户创建的、未软删除的 note 引用到的媒体（经 media_notes 映射，映射由 note 写入路径维护）；
//  2. 该用户上传过、但**目前没有任何 note 引用**的媒体（待用的上传）。
//
// 为什么按「引用」计：媒体按 sha256 内容寻址，同一份字节全库只存一份。按上传者计会让共享的
// blob 只记在第一个上传者名下，也让「删掉自己的引用」无法释放配额。按引用计满足：删除或替换
// 引用即释放（note 软删除同样释放）；同一 blob 被同一用户多处引用只计一次；多个用户引用同一 blob
// 时各计一次（逻辑归属，磁盘物理上仍只有一份，所以配额不等于磁盘占用）。
//
// 为什么还要计第 2 类：只按引用计时，上传后不插进任何卡片的文件永远不计费，一个账号可以
// 反复上传「不引用的文件」把磁盘写满。待用的上传计到上传者名下，直到有 note 引用它（转为
// 第 1 类，由引用者计费）或被媒体回收删除。
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
// 两个集合都是索引上的子查询，耗时与该用户的 note 总数无关。
func UserMediaUsage(ctx context.Context, db *gorm.DB, userID uint64) (MediaUsage, error) {
	usage := MediaUsage{Sha256: map[string]bool{}}
	if db == nil || userID == 0 {
		return usage, nil
	}
	referenced := db.Table("media_notes AS mn").Select("mn.media_sha").
		Joins("JOIN notes AS n ON n.id = mn.note_id AND n.deleted_at IS NULL").
		Where("n.created_by = ?", userID)
	pending := db.Table("media_uploaders AS mu").Select("mu.media_sha").
		Where("mu.user_id = ?", userID).
		Where("NOT EXISTS (SELECT 1 FROM media_notes AS ref WHERE ref.media_sha = mu.media_sha)")
	var rows []Media
	if err := db.WithContext(ctx).Model(&Media{}).Select("sha256", "bytes").
		Where("sha256 IN (?) OR sha256 IN (?)", referenced, pending).
		Find(&rows).Error; err != nil {
		return usage, fmt.Errorf("store: compute media usage for user %d: %w", userID, err)
	}
	for _, m := range rows {
		usage.Sha256[m.Sha256] = true
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

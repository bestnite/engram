package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// 媒体目录占用的采样（M6-8 健康页）。
//
// 取舍：一次请求里对媒体目录做完整递归扫描，在文件很多时会把请求卡住（媒体文件按
// sha256 分两级目录散落，规模只增不减）。因此这里做两层保护：
//   - 上限：最多统计 mediaSizeMaxEntries 个目录项，超出即停止并标记 truncated。
//     此时返回的是「至少这么多字节」，宁可给一个下界也不让请求无限期挂着。
//   - 缓存：mediaSizeTTL 内复用上一次结果，避免每次刷新健康页都重扫。
//
// 代价：截断时读数偏小，缓存窗口内读数可能滞后。健康页对这两个情况显式标注，
// 不做静默近似。

const (
	// mediaSizeTTL 是媒体占用读数的缓存时长。
	mediaSizeTTL = 30 * time.Second
	// mediaSizeMaxEntries 是单次统计允许遍历的最大目录项数。
	mediaSizeMaxEntries = 20000
)

// mediaSizeSample 是一次媒体占用采样的结果。
type mediaSizeSample struct {
	bytes     int64
	truncated bool
	at        time.Time
}

// cappedDirSize 递归统计目录字节数，最多遍历 maxEntries 个目录项；
// 超出时返回已累计的字节数与 truncated=true。目录不存在按 0 计。
func cappedDirSize(root string, maxEntries int) (int64, bool, error) {
	var total int64
	entries := 0
	truncated := false
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entries >= maxEntries {
			truncated = true
			return fs.SkipAll
		}
		entries++
		if d.Type().IsRegular() {
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, false, err
	}
	return total, truncated, nil
}

// mediaUsage 返回媒体目录占用（字节）与是否因上限被截断。
// 未装配媒体存储时返回 0；采样在 TTL 内直接复用缓存。
func (s *Server) mediaUsage() (int64, bool) {
	if s.media == nil {
		return 0, false
	}
	now := time.Now()
	s.mediaSizeMu.Lock()
	if s.mediaSize != nil && now.Sub(s.mediaSize.at) < mediaSizeTTL {
		sample := *s.mediaSize
		s.mediaSizeMu.Unlock()
		return sample.bytes, sample.truncated
	}
	s.mediaSizeMu.Unlock()

	bytes, truncated, err := cappedDirSize(s.media.Root(), mediaSizeMaxEntries)
	if err != nil {
		// 读目录失败不阻塞健康页：记英文日志，返回 0（媒体目录可能尚未建立）。
		s.logger.Error("health: compute media directory size failed", "error", err)
		return 0, false
	}

	s.mediaSizeMu.Lock()
	s.mediaSize = &mediaSizeSample{bytes: bytes, truncated: truncated, at: now}
	s.mediaSizeMu.Unlock()
	return bytes, truncated
}

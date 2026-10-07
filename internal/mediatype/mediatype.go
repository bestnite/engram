// Package mediatype 只做一件事：用文件字节（magic bytes）判定媒体类型，并与白名单比对。
//
// 它是叶子包：只依赖标准库，既不 import internal/store，也不包含 internal/media 的存储部分。
// 这样普通上传链（internal/media）与卡组包导入链（internal/store）能共用同一份判定与白名单，
// 既避免重复实现，也避免 store → media 的循环导入（F9）。
package mediatype

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// HeadBytes 是判定所需的最大字节数；调用方至少读这么多（不足时有多少喂多少）即可判定。
// 它同时是对外契约：两条链都按这个长度取文件头，保证判定结果一致。
const HeadBytes = 512

// DefaultAllowedMimes 是媒体白名单的默认值。
func DefaultAllowedMimes() []string {
	return []string{
		"image/png", "image/jpeg", "image/webp", "image/gif",
		"audio/mpeg", "audio/ogg", "audio/mp4",
	}
}

// Detect 用 magic bytes 判断媒体类型，返回 mime、扩展名与是否识别成功。
// 只认白名单涉及的类型；扩展名由这里给出，绝不从文件名取。
func Detect(head []byte) (mime, ext string, ok bool) {
	switch {
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", "png", true
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg", "jpg", true
	case len(head) >= 6 && (bytes.Equal(head[:6], []byte("GIF87a")) || bytes.Equal(head[:6], []byte("GIF89a"))):
		return "image/gif", "gif", true
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp", "webp", true
	case len(head) >= 4 && bytes.Equal(head[:4], []byte("OggS")):
		return "audio/ogg", "ogg", true
	case len(head) >= 3 && bytes.Equal(head[:3], []byte("ID3")):
		return "audio/mpeg", "mp3", true
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		// MPEG 音频帧同步（无 ID3 标签的 mp3）。
		return "audio/mpeg", "mp3", true
	case isISOBaseMedia(head):
		// m4a 属于 ISO BMFF：偏移 4 起是 "ftyp"。
		return "audio/mp4", "m4a", true
	}
	return "", "", false
}

// isISOBaseMedia 判断 ISO BMFF 头（ftyp box）；m4a/mp4 共用。
func isISOBaseMedia(head []byte) bool {
	if len(head) < 12 {
		return false
	}
	if !bytes.Equal(head[4:8], []byte("ftyp")) {
		return false
	}
	// 盒大小是前 4 字节的大端整数，必须至少覆盖到品牌字段，避免畸形输入。
	size := binary.BigEndian.Uint32(head[:4])
	return size >= 8
}

// Normalize 归一化常见的等价写法，便于与探测结果比较。
func Normalize(raw string) string {
	m := strings.ToLower(strings.TrimSpace(strings.SplitN(raw, ";", 2)[0]))
	switch m {
	case "image/jpg":
		return "image/jpeg"
	case "audio/m4a", "audio/x-m4a":
		return "audio/mp4"
	case "audio/mp3":
		return "audio/mpeg"
	}
	return m
}

// Allowed 判断 mime 是否在 allowed 白名单内；两侧都先归一化，容忍 image/jpg 之类的等价写法。
func Allowed(mime string, allowed []string) bool {
	m := Normalize(mime)
	for _, a := range allowed {
		if Normalize(a) == m {
			return true
		}
	}
	return false
}

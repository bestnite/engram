package media

import (
	"bytes"
	"encoding/binary"
)

// detectMime 用 magic bytes 判断媒体类型，返回 mime、扩展名与是否识别成功。
// 只认白名单涉及的类型；扩展名由这里给出，绝不从文件名取。
func detectMime(head []byte) (mime, ext string, ok bool) {
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

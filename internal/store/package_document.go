package store

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// 卡组包的 JSON 文档形态与 zip 字节之间的互转（M5-8）。
//
// MCP/CLI 既可能拿到 `export_deck` 输出的 JSON 文档（键为 manifest.json 等，
// 媒体条目为 base64 字符串），也可能拿到 base64 编码的 .edeck zip。两条入口都归一到
// 存储层唯一的 zip 解析路径（ImportPackage -> ReadPackageArchive），避免第二套解析。

// PackageDocumentToZip 把 DeckPackage.Document 形态的文档还原成 .edeck zip 字节。
// 以 .json 结尾的条目按 JSON 序列化，其余条目（media/ 下的文件）视为 base64 字符串解码。
func PackageDocumentToZip(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var raw []byte
		var err error
		if strings.HasSuffix(name, ".json") {
			raw, err = json.Marshal(doc[name])
		} else {
			s, ok := doc[name].(string)
			if !ok {
				return nil, fmt.Errorf("package entry %s must be a base64 string", name)
			}
			raw, err = base64.StdEncoding.DecodeString(s)
		}
		if err != nil {
			return nil, fmt.Errorf("encode package entry %s: %w", name, err)
		}
		w, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("create package entry %s: %w", name, err)
		}
		if _, err := w.Write(raw); err != nil {
			return nil, fmt.Errorf("write package entry %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close package archive: %w", err)
	}
	return buf.Bytes(), nil
}

// PackageReader 把 MCP/CLI 传入的包参数归一成 zip 读取器：
//   - string：base64 编码的 .edeck zip 字节；
//   - map[string]any：export_deck 输出的 JSON 文档（内部转成 zip）；
//   - []byte：原始 zip 字节。
func PackageReader(pkg any) (io.Reader, error) {
	switch v := pkg.(type) {
	case string:
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("package is not a valid base64 archive: %w", err)
		}
		return bytes.NewReader(raw), nil
	case []byte:
		return bytes.NewReader(v), nil
	case map[string]any:
		raw, err := PackageDocumentToZip(v)
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(raw), nil
	default:
		return nil, fmt.Errorf("package must be a JSON document object or a base64-encoded .edeck archive")
	}
}

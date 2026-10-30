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

// Conversion between the deck package's JSON document form and zip bytes.
//
// MCP/CLI may receive either the JSON document output by `export_deck` (keyed by manifest.json etc.,
// with media entries as base64 strings) or a base64-encoded .edeck zip. Both entry points normalize onto
// the store layer's single zip parsing path (ImportPackage -> unzipPackageFile), avoiding a second parser.

// PackageDocumentToZip turns a document in DeckPackage.Document form back into .edeck zip bytes.
// Entries ending in .json are serialized as JSON; the other entries (files under media/) are treated as base64 strings and decoded.
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
			// A single media entry is length-checked against the cap before decoding too, so one huge base64 string is not decoded into memory wholesale.
			raw, err = decodeBase64Bounded(s, DefaultPackageLimits().MaxFileBytes)
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

// PackageReader normalizes the package argument passed in by MCP/CLI into a zip reader:
//   - string: base64-encoded .edeck zip bytes;
//   - map[string]any: the JSON document export_deck outputs (converted to a zip internally);
//   - []byte: raw zip bytes.
func PackageReader(pkg any) (io.Reader, error) {
	return packageReader(pkg, DefaultPackageLimits())
}

// packageReader is the version of PackageReader with injectable limits: the caller supplies the caps, so a test can verify
// "check the length before decoding" with small caps instead of building a 100 MiB string.
func packageReader(pkg any, limits PackageLimits) (io.Reader, error) {
	switch v := pkg.(type) {
	case string:
		raw, err := decodeBase64Bounded(strings.TrimSpace(v), limits.MaxTotalBytes)
		if err != nil {
			return nil, err
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

// decodeBase64Bounded decodes a base64 string: it first rejects oversized input against the base64 expansion bound of maxBytes,
// and when over the bound it never decodes at all, so bytes beyond the archive cap are not pulled into memory wholesale ("reject oversize before decompressing").
// The error reuses the existing package_too_large code, the same definition as ReadPackageArchive's caps, and invents no new code.
func decodeBase64Bounded(s string, maxBytes int64) ([]byte, error) {
	if maxBytes > 0 && int64(len(s)) > int64(base64.StdEncoding.EncodedLen(int(maxBytes))) {
		return nil, &PackageError{Code: CodePackageTooLarge, Message: "encoded archive exceeds the size limit"}
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("package is not a valid base64 archive: %w", err)
	}
	return raw, nil
}

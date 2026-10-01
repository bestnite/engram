package cardtype

import "fmt"

// 本文件集中 fields 的类型读取与校验。
// fields 来自 JSON 反序列化：数值是 float64、数组是 []any、对象是 map[string]any
// （标准库 encoding/json 的行为）；同时兼容 Go 侧直接构造的 []string 等类型。

// stringField 读取必填的非空字符串字段。
func stringField(fields map[string]any, name string) (string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required field %q", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", name)
	}
	if s == "" {
		return "", fmt.Errorf("field %q must not be empty", name)
	}
	return s, nil
}

// stringSliceField 读取必填的非空字符串数组字段，元素必须非空。
func stringSliceField(fields map[string]any, name string) ([]string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, fmt.Errorf("missing required field %q", name)
	}
	if ss, ok := v.([]string); ok {
		if len(ss) == 0 {
			return nil, fmt.Errorf("field %q must not be empty", name)
		}
		for i, s := range ss {
			if s == "" {
				return nil, fmt.Errorf("field %q[%d] must not be empty", name, i)
			}
		}
		return ss, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("field %q must be an array of strings", name)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("field %q must not be empty", name)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("field %q[%d] must be a string", name, i)
		}
		if s == "" {
			return nil, fmt.Errorf("field %q[%d] must not be empty", name, i)
		}
		out[i] = s
	}
	return out, nil
}

// boolField 读取可选布尔字段；缺失时返回默认值 def。
func boolField(fields map[string]any, name string, def bool) (bool, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q must be a boolean", name)
	}
	return b, nil
}

// validateCommonOptional 校验通用可选字段 extra / source_url（DESIGN.md §6.2）。
//
// 注意：DESIGN.md §6.2 把它们列为所有题型可用的可选字段；而
// schema/note-import.schema.json 把它们放在 note 层（与 fields 平级）。
// 两处位置不一致，本包按 DESIGN.md 在 fields 内接受并做类型校验；
// 放在 note 层时不由本包处理。该差异已在交付报告中说明，未改动 schema。
func validateCommonOptional(fields map[string]any) error {
	if v, ok := fields["source_url"]; ok && v != nil {
		s, ok := v.(string)
		if !ok || s == "" {
			return fmt.Errorf(`field "source_url" must be a non-empty string`)
		}
	}
	if v, ok := fields["extra"]; ok && v != nil {
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf(`field "extra" must be an object`)
		}
	}
	return nil
}

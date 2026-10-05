// Package config 负责读取启动配置。
// 优先级固定为：环境变量 > settings 表 > 内置默认值；访问器必须能报告生效值的来源，
// 否则管理面板无法回答"改了为什么没生效"（DESIGN.md §8.4）。
package config

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// Source 标识一个生效值的来源。
type Source string

const (
	SourceEnv     Source = "env"
	SourceDB      Source = "db"
	SourceDefault Source = "default"
)

// Key 是配置项的稳定英文标识（AGENTS.md §2.1：标识符一律英文）。
// 同一个 Key 既用于环境变量，也用于 settings 表的键。
type Key string

const (
	KeyHTTPAddr            Key = "http_addr"
	KeyBaseURL             Key = "base_url"
	KeyDBDriver            Key = "db_driver"
	KeyDBDSN               Key = "db_dsn"
	KeySessionSecret       Key = "session_secret"
	KeyEncryptionKey       Key = "encryption_key"
	KeyAutoMigrate         Key = "auto_migrate"
	KeyBootstrapAdminEmail Key = "bootstrap_admin_email"
	KeyMediaDir            Key = "media_dir"
	// KeyTrustedProxies 是允许改写 ClientIP() 的代理地址列表（逗号分隔的 IP 或 CIDR）。
	// 缺省为空 = 不信任任何代理（DESIGN.md §4.3、§11）。
	KeyTrustedProxies Key = "trusted_proxies"
	// KeyOptimizerPath 是 FSRS 优化器适配器二进制的路径（M9-10）。
	// 留空时由 internal/jobs 解析：先找服务二进制旁的 optimizer，再回退到仓库构建产物。
	KeyOptimizerPath Key = "optimizer_path"
)

// field 描述一个配置项在环境变量中的名字、是否必需、内置默认值。
type field struct {
	env      string
	required bool
	def      string
}

// fields 是唯一的配置项注册表：新增配置项必须同时改这里和 .env.example（有单测保证同步）。
var fields = map[Key]field{
	KeyHTTPAddr:            {env: "HTTP_ADDR", def: "127.0.0.1:8080"},
	KeyBaseURL:             {env: "BASE_URL", def: "http://localhost:8080"},
	KeyDBDriver:            {env: "DB_DRIVER", required: true},
	KeyDBDSN:               {env: "DB_DSN", required: true},
	KeySessionSecret:       {env: "SESSION_SECRET", required: true},
	KeyEncryptionKey:       {env: "ENCRYPTION_KEY", required: true},
	KeyAutoMigrate:         {env: "AUTO_MIGRATE", def: "1"},
	KeyBootstrapAdminEmail: {env: "BOOTSTRAP_ADMIN_EMAIL"},
	KeyMediaDir:            {env: "MEDIA_DIR", def: "data/media"},
	KeyTrustedProxies:      {env: "TRUSTED_PROXIES"},
	KeyOptimizerPath:       {env: "OPTIMIZER_PATH"},
}

// Value 是访问器的返回结果：生效值加来源。
type Value struct {
	Key    Key
	Value  string
	Source Source
}

// Config 保存一次加载的结果。两类来源分开保存，优先级在 Get 里实现。
type Config struct {
	env map[Key]string
	db  map[Key]string
}

// Load 读取环境变量并叠加 settings 表覆盖值。
// lookupEnv 为 nil 时使用 os.LookupEnv（测试注入替身）。settings 可为 nil。
// 缺少任一必需变量时返回英文错误，调用方应据此拒绝启动。
func Load(lookupEnv func(string) (string, bool), settings map[string]string) (*Config, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	c := &Config{env: make(map[Key]string, len(fields)), db: make(map[Key]string, len(fields))}
	var missing []string
	for key, f := range fields {
		if v, ok := lookupEnv(f.env); ok && strings.TrimSpace(v) != "" {
			c.env[key] = strings.TrimSpace(v)
		} else if f.required {
			missing = append(missing, f.env)
		}
	}
	if len(missing) > 0 {
		// 排序让错误信息稳定可断言。
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required environment variable(s): %s", strings.Join(missing, ", "))
	}
	for rawKey, rawValue := range settings {
		key := Key(rawKey)
		// settings 表可能存有只存在于数据库、不来自环境变量的键；这里只关心两者共有的项。
		if _, known := fields[key]; !known {
			continue
		}
		if v := strings.TrimSpace(rawValue); v != "" {
			c.db[key] = v
		}
	}
	return c, nil
}

// Get 是唯一访问器：返回生效值及其来源（env / db / default）。
func (c *Config) Get(key Key) Value {
	if v, ok := c.env[key]; ok {
		return Value{Key: key, Value: v, Source: SourceEnv}
	}
	if v, ok := c.db[key]; ok {
		return Value{Key: key, Value: v, Source: SourceDB}
	}
	return Value{Key: key, Value: fields[key].def, Source: SourceDefault}
}

// String 便于直接当字符串使用。
func (v Value) String() string { return v.Value }

// All 按 Key 排序返回全部配置项的生效值，供管理面板展示"值 + 来源"。
func (c *Config) All() []Value {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	out := make([]Value, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.Get(Key(k)))
	}
	return out
}

// TrustedProxies 解析并校验 TRUSTED_PROXIES：逗号分隔的 IP 或 CIDR 列表。
//
// 缺省为空 = 不信任任何代理：gin 不再采信 X-Forwarded-For / X-Real-IP，ClientIP() 回落到
// RemoteAddr，登录限流键、审计 IP 与新设备/新 IP 提醒都不再被请求头左右。只有列出的地址
// 发来的请求才被容许改写这些结果（DESIGN.md §4.3、§11）。
//
// 任一非法项都返回英文错误并点名该项，调用方据此拒绝启动；绝不静默丢弃（AGENTS.md §2.6）。
// 刻意只认 IP/CIDR：不把 X-Forwarded-Proto 之类的头当作信任依据（TLS 终止信息由 BASE_URL 决定）。
func (c *Config) TrustedProxies() ([]string, error) {
	raw := c.Get(KeyTrustedProxies).Value
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		if err := validateProxyEntry(entry); err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q: %w", entry, err)
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// validateProxyEntry 只接受单个 IP 或 CIDR；其余一律拒绝。
func validateProxyEntry(entry string) error {
	if strings.Contains(entry, "/") {
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("not a valid CIDR")
		}
		return nil
	}
	if net.ParseIP(entry) == nil {
		return fmt.Errorf("not a valid IP or CIDR")
	}
	return nil
}

// EnvNames 返回注册表中全部环境变量名，按字母序。
func EnvNames() []string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.env)
	}
	sort.Strings(names)
	return names
}

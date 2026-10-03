#!/usr/bin/env bash
# 仓库脱敏关键词扫描（AGENTS.md §2.2、DESIGN.md §10.4；M0-10 验收项之一）。
#
# 目的：任何被提交的文件都不得出现真实域名、主机名、内网地址、个人邮箱或真实凭据。
# 允许的占位值：example.com / example.org / example.net / localhost / CHANGE_ME（域名比较大小写不敏感）。
#
# 为什么只扫 git 跟踪的文件：未跟踪文件不属于提交内容，扫描它们会误伤本地草稿。
# 为什么排除 mathjax / htmx：它们是上游压缩包里的第三方代码，不是本仓库的文字。
# 为什么排除本目录：脚本自身必然包含用于匹配的关键词（如 “example.com”“.internal”），
# 自扫必然误报；脚本的改动由 code review 把关。
#
# 用法：在仓库根执行 `bash scripts/checks/no-private-data.sh`；有违规时退出码为 1。
set -u

cd "$(dirname "$0")/../.." || exit 2

ALLOWED_HOSTS_FILE="scripts/checks/allowed-hosts.txt"
fail=0

EXCLUDES=(
  ':(exclude)scripts/checks/*'
  ':(exclude)internal/web/static/js/mathjax/*'
  ':(exclude)internal/web/static/js/htmx.min.js'
)

rule() { # rule <描述> <命中文本>
  local desc="$1" hits="$2"
  [ -z "$hits" ] && return 0
  printf '\n[sanitize] %s\n' "$desc"
  printf '%s\n' "$hits"
  fail=1
}

# 取本仓库跟踪文件里符合正则的行；无匹配时输出空串。
scan() { git grep -nE --color=never -e "$1" -- "${EXCLUDES[@]}" 2>/dev/null || true; }
scan_i() { git grep -niE --color=never -e "$1" -- "${EXCLUDES[@]}" 2>/dev/null || true; }

# 1) 邮箱：只允许占位域。
emails=$(scan '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}')
# 大小写不敏感：域名在语义上大小写无关；只有三个占位域被豁免，真实域名照样会被抓出来。
emails=$(printf '%s\n' "$emails" | grep -viE '@(example\.(com|org|net)|localhost)\b' || true)
rule "邮箱地址不是占位域（允许 example.com/org/net、localhost）" "$emails"

# 2) 内网 / 链路本地 IPv4（127.0.0.1 之外的回环与 0.0.0.0 不算内网）。
ips=$(scan '(^|[^0-9])(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}|169\.254\.[0-9]{1,3}\.[0-9]{1,3})($|[^0-9])')
rule "内网或链路本地 IPv4 地址（请改用 localhost）" "$ips"

# 3) 仅内网使用的主机后缀。刻意不含 “.local” 与 “.home”：仓库用 config.local.* /
#    *.local.json 作文件名模式、用 s.home 等 Go 标识符，收录两者会造成长期误报。
internal=$(scan_i '\b[a-z0-9]([a-z0-9-]*[a-z0-9])?\.(internal|corp|lan|intranet|localdomain)\b')
rule "内网专用主机后缀（.internal/.corp/.lan/.intranet/.localdomain）" "$internal"

# 4) 配置/文档里的凭据赋值：只扫非源码文件，且放行占位值。
#    源码里的 `password: "..."` 是测试夹具或字段名，不是提交的凭据，故不在此列。
#    放行清单里的 `\$\{\{?[[:space:]]*[A-Za-z_]` 覆盖两类引用：shell 式 `${VAR}`，以及
#    GitHub Actions 的 `${{ secrets.X }}` / `${{ github.token }}`。工作流里 goreleaser、
#    action 的入参必须写成 `KEY: ${{ secrets.X }}`，没有别的合法写法，而它引用的仍是仓库
#    secret，仓库里不存在字面量——故此处按占位值处理，而不是放宽成「含 $ 即放行」。
creds=$(git grep -niE --color=never \
  -e '(password|passwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|encryption[_-]?key|master[_-]?key)[[:space:]]*[:=][[:space:]]*[^[:space:]]+' \
  -- "${EXCLUDES[@]}" '*.md' '*.yaml' '*.yml' '*.json' '*.toml' '*.ini' '*.conf' '*.properties' '*.sh' '*.env*' '.env.example' 'Dockerfile' 2>/dev/null || true)
creds=$(printf '%s\n' "$creds" | grep -viE '[=:][[:space:]]*["'"'"']?(CHANGE_ME|example|REDACTED|REDACT|placeholder|xxxx|\*\*\*|<[A-Za-z_]+>|\$\{\{?[[:space:]]*[A-Za-z_]|%s)' || true)
rule "疑似真实凭据赋值（非占位值）" "$creds"

# 5) 已知密钥前缀。
secrets=$(scan '(ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|glpat-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{35})')
rule "疑似真实密钥/令牌（已知前缀）" "$secrets"

# 6) 私钥块。
privkeys=$(git grep -nE --color=never -e '-----BEGIN [A-Z ]*PRIVATE KEY-----' -- "${EXCLUDES[@]}" 2>/dev/null || true)
rule "内嵌私钥内容" "$privkeys"

# 7) URL 主机允许名单：任何 http(s) 主机必须是公开占位域、文档域或已登记的公网依赖域。
urls=$(git grep -noE --color=never -e 'https?://[A-Za-z0-9._-]+' -- "${EXCLUDES[@]}" 2>/dev/null || true)
allowed_re=$(grep -vE '^[[:space:]]*(#|$)' "$ALLOWED_HOSTS_FILE" | paste -sd'|' -)
if [ -n "$allowed_re" ]; then
  bad_urls=$(printf '%s\n' "$urls" | grep -vE "://([A-Za-z0-9._-]+\.)?(${allowed_re})([:/]|$)" || true)
else
  bad_urls="$urls"
fi
rule "URL 主机不在允许名单（scripts/checks/allowed-hosts.txt）" "$bad_urls"

if [ "$fail" -ne 0 ]; then
  printf '\n[sanitize] FAILED: remove the strings above or replace them with example.com / localhost / CHANGE_ME.\n' >&2
  exit 1
fi

printf '[sanitize] OK: no real domains, host names, private addresses, emails or credentials found.\n'
exit 0

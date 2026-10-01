#!/usr/bin/env bash
# 模板硬编码文案检查（AGENTS.md §2.1、§5 M0-10、M8-3 的 lint 项）。
#
# 规则：*.templ 模板里所有面向用户的文字都必须由 handler 从语言包取出后经数据传入，
# 模板中不得出现字面文本节点，也不得在 placeholder/title/alt/aria-* 这些可见属性里
# 写死文字（类名、路由、HTML 结构不在此列）。
#
# 启发式做法：先去掉 templ 注释与 { ... } 表达式，再跳过 templ/if/for 等控制行，
# 然后剥掉 HTML 标签，最后清除纯标点与空白。若仍有剩余字符（拉丁字母或 CJK 字节），
# 那就是模板里写死的文字 → 违规。
#
# 用法：在仓库根执行 `bash scripts/checks/no-template-literals.sh`；有违规则退出码为 1。
set -u

cd "$(dirname "$0")/../.." || exit 2

fail=0
mapfile -t templates < <(git ls-files '*.templ')

# 1) 文本节点检查。awk 出错必须致命，否则扫描静默通过。
literals=$(awk '
  {
    line = $0
    sub(/^[ \t]*\/\/.*/, "", line)      # 整行注释
    sub(/[ \t]\/\/.*/, "", line)        # 行尾注释（不会吃掉 https://）
    while (match(line, /\{[^{}]*\}/)) line = substr(line, 1, RSTART-1) substr(line, RSTART+RLENGTH)
    t = line; sub(/^[ \t]+/, "", t)
    if (t ~ /^(templ|if |for |else|switch|case |default|import|package|@|\}|\{)/) next
    gsub(/<[^>]*>/, "", line)           # HTML 标签
    gsub(/[][(){}<>"'"'"',;:=_ \t.\\/*+|@#%!?&^0-9-]/, "", line)  # 标点/空白/数字
    if (length(line) > 0) printf "%s:%d: %s\n", FILENAME, FNR, $0
  }
' "${templates[@]}")
awk_status=$?
if [ "$awk_status" -ne 0 ]; then
  printf '[template-literals] ERROR: scanner failed (awk exit %d).\n' "$awk_status" >&2
  exit 2
fi

if [ -n "$literals" ]; then
  printf '\n[template-literals] hardcoded user-facing text found (move it into the translation catalog):\n'
  printf '%s\n' "$literals"
  fail=1
fi

# 2) 可见属性里的字面量。
attrs=""
for f in "${templates[@]}"; do
  [ -z "$f" ] && continue
  hits=$(grep -nE '\b(placeholder|title|alt|aria-label|aria-description)[[:space:]]*=[[:space:]]*"[^"]*"' "$f" \
    | grep -E '"[^"]*[[:alpha:]][^"]*"' \
    | grep -vE '="[[:space:]]*\{' || true)
  [ -n "$hits" ] && attrs="${attrs}${f}:${hits}"$'\n'
done

if [ -n "$attrs" ]; then
  printf '\n[template-literals] hardcoded literal in a user-visible attribute:\n'
  printf '%s\n' "$attrs"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  printf '\n[template-literals] FAILED: every user-facing string must come from internal/i18n/locales/*.yaml.\n' >&2
  exit 1
fi

printf '[template-literals] OK: no hardcoded user-facing text in %d template(s).\n' "${#templates[@]}"
exit 0

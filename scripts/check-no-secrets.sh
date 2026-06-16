#!/bin/sh
set -eu

fail=0
tmp="${TMPDIR:-/tmp}/middleware-auth-secret-scan.$$"
trap 'rm -f "$tmp"' EXIT HUP INT TERM
: > "$tmp"

env_files=$(
  find . \
    \( -path './.git' -o -path './.middleware-state' -o -path './bin' \) -prune -o \
    -type f \( -name '.env' -o -name '.env.*' -o -name '*.env' -o -name '*.env.*' -o -name '.envrc' \) \
    -print
)

if [ -n "$env_files" ]; then
  printf '%s\n' "ERRO: arquivos .env nao sao permitidos neste repo:" >&2
  printf '%s\n' "$env_files" >&2
  fail=1
fi

ignored_cmd=$(
  git check-ignore -v \
    cmd/middleware-codex-oauth/main.go \
    cmd/middleware-codex-oauth-mcp/main.go 2>/dev/null || true
)

if [ -n "$ignored_cmd" ]; then
  printf '%s\n' "ERRO: entrypoints em cmd/ estao ignorados pelo git:" >&2
  printf '%s\n' "$ignored_cmd" >&2
  fail=1
fi

find . \
  \( -path './.git' -o -path './.middleware-state' -o -path './bin' \) -prune -o \
  -type f -print |
while IFS= read -r file; do
  case "$file" in
    *.png|*.jpg|*.jpeg|*.gif|*.webp|*.ico|*.pdf|*.zip|*.gz|*.tgz|*.tar|*.test|*.out)
      continue
      ;;
  esac
  LC_ALL=C grep -nE 'sk-[A-Za-z0-9._~:+/=-]{8,}|Bearer[[:space:]]+[A-Za-z0-9._~+/=-]{24,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}' "$file" 2>/dev/null |
    sed "s#^#$file:#" >> "$tmp" || true
done

if [ -s "$tmp" ]; then
  printf '%s\n' "ERRO: possiveis segredos encontrados:" >&2
  cat "$tmp" >&2
  fail=1
fi

exit "$fail"

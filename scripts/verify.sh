#!/bin/sh
set -eu

for command_name in go jq shellcheck; do
	if ! command -v "$command_name" >/dev/null 2>&1; then
		printf '%s\n' "ERRO: comando obrigatorio ausente: $command_name" >&2
		exit 1
	fi
done

sh -n scripts/check-no-secrets.sh scripts/e2e-live-lmstudio.sh scripts/verify.sh
shellcheck -s sh scripts/check-no-secrets.sh scripts/e2e-live-lmstudio.sh scripts/verify.sh
jq empty docs/examples/llm-http-payloads.json
./scripts/check-no-secrets.sh
go test -count=1 ./...
go test -race ./...
go vet ./...
go build ./...

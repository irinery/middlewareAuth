#!/bin/sh
set -eu

umask 077

for command_name in curl go jq; do
	if ! command -v "$command_name" >/dev/null 2>&1; then
		printf '%s\n' "ERRO: comando obrigatorio ausente: $command_name" >&2
		exit 1
	fi
done

: "${LMSTUDIO_API_KEY:?ERRO: exporte LMSTUDIO_API_KEY com um token real do LM Studio}"
: "${LMSTUDIO_MODEL:?ERRO: exporte LMSTUDIO_MODEL com o identificador de um modelo carregado}"

LMSTUDIO_BASE_URL=${LMSTUDIO_BASE_URL:-http://127.0.0.1:1234}
E2E_MIDDLEWARE_PORT=${E2E_MIDDLEWARE_PORT:-18789}
E2E_PROJECT_ID=${E2E_PROJECT_ID:-middlewareauth-e2e}
E2E_PROFILE_ID=${E2E_PROFILE_ID:-live}
E2E_OTHER_PROJECT_ID=${E2E_OTHER_PROJECT_ID:-middlewareauth-isolated}
MIDDLEWARE_BASE_URL="http://127.0.0.1:$E2E_MIDDLEWARE_PORT"

case "$E2E_MIDDLEWARE_PORT" in
	''|*[!0-9]*)
		printf '%s\n' 'ERRO: E2E_MIDDLEWARE_PORT precisa ser numerica' >&2
		exit 1
		;;
esac

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/middlewareauth-e2e.XXXXXX")
server_pid=''

cleanup() {
	if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

random_hex() {
	dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n'
}

middleware_secret_key=$(random_hex)
middleware_client_token=$(random_hex)
server_bin="$tmp_dir/middleware-codex-oauth"
mcp_bin="$tmp_dir/middleware-codex-oauth-mcp"
state_dir="$tmp_dir/state"
runtime_dir="$tmp_dir/runtime"
mkdir -p "$runtime_dir"

provider_models=$(curl --fail --silent --show-error --max-time 15 \
	-H "Authorization: Bearer $LMSTUDIO_API_KEY" \
	-H 'Accept: application/json' \
	"${LMSTUDIO_BASE_URL%/}/v1/models")

if ! printf '%s' "$provider_models" | jq -e --arg model "$LMSTUDIO_MODEL" \
	'.data | type == "array" and any(.[]; .id == $model)' >/dev/null; then
	printf '%s\n' "ERRO: modelo '$LMSTUDIO_MODEL' nao esta carregado no LM Studio" >&2
	exit 1
fi

go build -trimpath -o "$server_bin" ./cmd/middleware-codex-oauth
go build -trimpath -o "$mcp_bin" ./cmd/middleware-codex-oauth-mcp

(
	cd "$runtime_dir"
	exec env \
		NODE_ENV=production \
		HTTP_BIND_ADDR=127.0.0.1 \
		HTTP_PORT="$E2E_MIDDLEWARE_PORT" \
		OAUTH_CALLBACK_PORT="$E2E_MIDDLEWARE_PORT" \
		MIDDLEWARE_STATE_DIR="$state_dir" \
		MIDDLEWARE_SECRET_KEY="$middleware_secret_key" \
		MIDDLEWARE_CLIENT_TOKEN="$middleware_client_token" \
		MIDDLEWARE_REDACT_LOGS=true \
		"$server_bin" >"$tmp_dir/server.stdout" 2>"$tmp_dir/server.stderr"
) &
server_pid=$!

ready=false
attempt=0
while [ "$attempt" -lt 80 ]; do
	if curl --fail --silent --max-time 2 "$MIDDLEWARE_BASE_URL/healthz" >/dev/null 2>&1; then
		ready=true
		break
	fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		printf '%s\n' 'ERRO: middleware encerrou durante o boot' >&2
		sed -n '1,120p' "$tmp_dir/server.stderr" >&2
		exit 1
	fi
	attempt=$((attempt + 1))
	sleep 0.25
done
if [ "$ready" != true ]; then
	printf '%s\n' 'ERRO: middleware nao ficou pronto no prazo' >&2
	exit 1
fi

api_get() {
	curl --fail --silent --show-error --max-time 120 \
		-H "Authorization: Bearer $middleware_client_token" \
		-H 'Accept: application/json' \
		"$1"
}

api_post() {
	curl --fail --silent --show-error --max-time 120 \
		-H "Authorization: Bearer $middleware_client_token" \
		-H 'Accept: application/json' \
		-H 'Content-Type: application/json' \
		--data-binary @- \
		"$1"
}

catalog=$(api_get "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/llm/providers")
printf '%s' "$catalog" | jq -e \
	'.contractVersion == "middlewareauth.llm.v1"
	 and ([.providers[].id] | index("openai") != null)
	 and ([.providers[].id] | index("lmstudio") != null)
	 and ([.providers[] | select(.id == "lmstudio") | .auth.fields[] | select(.id == "apiKey" and .secret == true)] | length == 1)' \
	>/dev/null

login_payload=$(jq -cn \
	--arg providerId lmstudio \
	--arg profileId "$E2E_PROFILE_ID" \
	--arg baseUrl "$LMSTUDIO_BASE_URL" \
	--arg apiKey "$LMSTUDIO_API_KEY" \
	'{providerId:$providerId,profileId:$profileId,mode:"api_key",authFields:{baseUrl:$baseUrl,apiKey:$apiKey}}')
login=$(printf '%s' "$login_payload" | api_post "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/llm/login")
printf '%s' "$login" | jq -e \
	--arg project "$E2E_PROJECT_ID" --arg profile "$E2E_PROFILE_ID" \
	'.authenticated == true and .status == "authenticated" and .providerId == "lmstudio"
	 and .projectId == $project and .profileId == $profile and .modelCount >= 1
	 and (.loginSessionId | type == "string" and length > 0)' >/dev/null
login_session_id=$(printf '%s' "$login" | jq -r '.loginSessionId')

if printf '%s' "$login" | grep -F "$LMSTUDIO_API_KEY" >/dev/null 2>&1; then
	printf '%s\n' 'ERRO: login HTTP devolveu a API key do provider' >&2
	exit 1
fi

login_status=$(api_get "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/llm/login-sessions/$login_session_id?providerId=lmstudio&profileId=$E2E_PROFILE_ID")
printf '%s' "$login_status" | jq -e --arg session "$login_session_id" \
	'.authenticated == true and .status == "authenticated" and .loginSessionId == $session' >/dev/null

status=$(api_get "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/llm/status?providerId=lmstudio&profileId=$E2E_PROFILE_ID")
printf '%s' "$status" | jq -e --arg project "$E2E_PROJECT_ID" --arg profile "$E2E_PROFILE_ID" \
	'.authenticated == true and .providerId == "lmstudio" and .projectId == $project and .profileId == $profile' >/dev/null

isolated_status=$(api_get "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_OTHER_PROJECT_ID/llm/status?providerId=lmstudio&profileId=$E2E_PROFILE_ID")
printf '%s' "$isolated_status" | jq -e --arg project "$E2E_OTHER_PROJECT_ID" \
	'.authenticated == false and .projectId == $project and (.accountId | not)' >/dev/null

response_payload=$(jq -cn --arg model "$LMSTUDIO_MODEL" --arg profile "$E2E_PROFILE_ID" \
	'{providerId:"lmstudio",profileId:$profile,model:$model,instructions:"Responda de forma objetiva.",input:[{role:"user",content:"Responda apenas E2E_OK"}],stream:false,store:false}')
response=$(printf '%s' "$response_payload" | api_post "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/llm/responses")
printf '%s' "$response" | jq -e \
	'.outputText | type == "string" and length > 0' >/dev/null

legacy_status=$(api_get "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/auth/lmstudio/status?profileId=$E2E_PROFILE_ID")
printf '%s' "$legacy_status" | jq -e '.authenticated == true and .providerId == "lmstudio"' >/dev/null
legacy_response=$(printf '%s' "$response_payload" | jq 'del(.providerId,.profileId)' | \
	api_post "$MIDDLEWARE_BASE_URL/v1/projects/$E2E_PROJECT_ID/lmstudio/responses?profileId=$E2E_PROFILE_ID")
printf '%s' "$legacy_response" | jq -e '.outputText | type == "string" and length > 0' >/dev/null

mcp_input="$tmp_dir/mcp-input.jsonl"
mcp_output="$tmp_dir/mcp-output.jsonl"
{
	jq -cn '{jsonrpc:"2.0",id:1,method:"initialize",params:{protocolVersion:"2025-11-25",capabilities:{},clientInfo:{name:"middlewareauth-live-e2e",version:"1.0.0"}}}'
	jq -cn '{jsonrpc:"2.0",method:"notifications/initialized"}'
	jq -cn '{jsonrpc:"2.0",id:2,method:"tools/list",params:{}}'
	jq -cn --arg project "$E2E_PROJECT_ID" \
		'{jsonrpc:"2.0",id:3,method:"tools/call",params:{name:"llm_providers",arguments:{projectId:$project}}}'
	jq -cn --arg project "$E2E_PROJECT_ID" --arg profile "$E2E_PROFILE_ID" --arg baseUrl "$LMSTUDIO_BASE_URL" --arg apiKey "$LMSTUDIO_API_KEY" \
		'{jsonrpc:"2.0",id:4,method:"tools/call",params:{name:"llm_login_start",arguments:{providerId:"lmstudio",projectId:$project,profileId:$profile,mode:"api_key",authFields:{baseUrl:$baseUrl,apiKey:$apiKey}}}}'
	jq -cn --arg project "$E2E_PROJECT_ID" --arg profile "$E2E_PROFILE_ID" --arg session "$login_session_id" \
		'{jsonrpc:"2.0",id:5,method:"tools/call",params:{name:"llm_login_status",arguments:{providerId:"lmstudio",projectId:$project,profileId:$profile,loginSessionId:$session}}}'
	jq -cn --arg project "$E2E_PROJECT_ID" --arg profile "$E2E_PROFILE_ID" \
		'{jsonrpc:"2.0",id:6,method:"tools/call",params:{name:"llm_status",arguments:{providerId:"lmstudio",projectId:$project,profileId:$profile}}}'
	jq -cn --arg project "$E2E_PROJECT_ID" --arg profile "$E2E_PROFILE_ID" --arg model "$LMSTUDIO_MODEL" \
		'{jsonrpc:"2.0",id:7,method:"tools/call",params:{name:"llm_responses",arguments:{providerId:"lmstudio",projectId:$project,profileId:$profile,model:$model,input:"Responda apenas E2E_OK",stream:false,store:false}}}'
} >"$mcp_input"

env \
	MIDDLEWARE_BASE_URL="$MIDDLEWARE_BASE_URL" \
	MIDDLEWARE_CLIENT_TOKEN="$middleware_client_token" \
	MCP_DEFAULT_PROJECT_ID="$E2E_PROJECT_ID" \
	MIDDLEWARE_LLM_PROVIDER=lmstudio \
	MIDDLEWARE_LLM_PROFILE_ID="$E2E_PROFILE_ID" \
	MIDDLEWARE_LLM_MODEL="$LMSTUDIO_MODEL" \
	"$mcp_bin" <"$mcp_input" >"$mcp_output" 2>"$tmp_dir/mcp.stderr"

for response_id in 3 4 5 6 7; do
	jq -se --argjson id "$response_id" \
		'any(.[]; .id == $id and .result.isError == false and (.result.content[0].text | type == "string"))' \
		"$mcp_output" >/dev/null
done

jq -se \
	'any(.[]; .id == 2 and ([.result.tools[].name] | index("llm_providers") != null and index("llm_login_start") != null and index("llm_login_status") != null and index("llm_status") != null and index("llm_refresh") != null and index("llm_responses") != null))' \
	"$mcp_output" >/dev/null

mcp_catalog=$(jq -r 'select(.id == 3) | .result.content[0].text' "$mcp_output")
if [ "$(printf '%s' "$catalog" | jq -cS .)" != "$(printf '%s' "$mcp_catalog" | jq -cS .)" ]; then
	printf '%s\n' 'ERRO: catalogos HTTP e MCP divergem' >&2
	exit 1
fi

mcp_response=$(jq -r 'select(.id == 7) | .result.content[0].text' "$mcp_output")
printf '%s' "$mcp_response" | jq -e '.outputText | type == "string" and length > 0' >/dev/null

if grep -F "$LMSTUDIO_API_KEY" "$mcp_output" >/dev/null 2>&1; then
	printf '%s\n' 'ERRO: resposta MCP devolveu a API key do provider' >&2
	exit 1
fi
if grep -R -F "$LMSTUDIO_API_KEY" "$state_dir" >/dev/null 2>&1; then
	printf '%s\n' 'ERRO: API key foi persistida em texto claro' >&2
	exit 1
fi

finished_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
jq -n \
	--arg suite middlewareauth-live-lmstudio \
	--arg finishedAt "$finished_at" \
	--arg provider lmstudio \
	--arg model "$LMSTUDIO_MODEL" \
	--arg projectId "$E2E_PROJECT_ID" \
	'{suite:$suite,status:"passed",finishedAt:$finishedAt,provider:$provider,model:$model,projectId:$projectId,checks:["provider-real-authenticated","http-canonical-catalog","http-login","http-login-status","http-status","http-project-isolation","http-responses","http-legacy-compatibility","mcp-protocol","mcp-canonical-catalog","mcp-login","mcp-login-status","mcp-status","mcp-responses","credential-redaction","encrypted-at-rest"]}'

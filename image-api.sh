#!/bin/bash
set -euo pipefail

# ============================================================
# ChatGPT2API async image task API examples
#
# Usage:
#   ./image-api.sh models        GET  /v1/models
#   ./image-api.sh generate      POST /api/creation-tasks/image-generations + poll
#   ./image-api.sh edit          POST /api/creation-tasks/image-edits + poll
#   ./image-api.sh poll <id>     GET  /api/creation-tasks?ids=<id>
#
# Why async:
#   /v1/images/generations is a synchronous long request and can hit client,
#   reverse-proxy, or upstream read timeouts. The web creation console uses
#   these async /api/creation-tasks routes, so API callers should use them too
#   for image generation/editing.
# ============================================================

# Update these values before running.
# BASE_URL can be either "https://your-domain.com" or "https://your-domain.com/v1".
BASE_URL="${BASE_URL:-http://localhost:8080}"
API_KEY="${API_KEY:-sk-xxx}"
INPUT_IMAGE="${INPUT_IMAGE:-./input.png}"

# Polling controls.
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-5}"
POLL_MAX_ATTEMPTS="${POLL_MAX_ATTEMPTS:-120}"

ROOT_URL="${BASE_URL%/}"
if [[ "${ROOT_URL}" == */v1 ]]; then
  ROOT_URL="${ROOT_URL%/v1}"
fi
V1_BASE="${ROOT_URL}/v1"

# Async task defaults and useful values:
#
# POST /api/creation-tasks/image-generations
#   Required:
#     client_task_id, prompt
#   Defaults:
#     model=auto
#     n=1
#     size=""                 # no explicit size hint
#     quality=""              # useful values: low, medium, high
#     output_format=png        # useful values: png, jpeg, webp
#     output_compression unset # jpeg only, range 0-100
#     visibility=private       # useful values: private, public
#   Useful model values:
#     auto, gpt-image-2, codex-gpt-image-2
#   Useful size values:
#     auto, 1:1, 3:2, 2:3, 16:9, 21:9, 4:3, 3:4, 9:16,
#     1080p, 2k, 4k, WIDTHxHEIGHT
#   Useful extra fields:
#     image_resolution=1080p|2k|4k
#     background, moderation, style, partial_images
#     frontend_conversation_id
#
# POST /api/creation-tasks/image-edits
#   Required:
#     client_task_id, prompt, image file
#   Defaults and useful values:
#     Same as image-generations.
#
# GET /api/creation-tasks?ids=<id1,id2>
#   Polls async task status. Typical terminal statuses are success/error/canceled.

pretty() {
  if command -v jq >/dev/null 2>&1; then
    jq .
  else
    cat
  fi
}

raw_json() {
  local method="$1"
  local path="$2"
  local body="$3"

  curl -sS -X "${method}" "${ROOT_URL}${path}" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d "${body}"
}

task_id() {
  local prefix="$1"
  if command -v uuidgen >/dev/null 2>&1; then
    echo "${prefix}-$(uuidgen | tr '[:upper:]' '[:lower:]')"
  else
    echo "${prefix}-$(date +%s)-${RANDOM}"
  fi
}

extract_task_id() {
  if command -v jq >/dev/null 2>&1; then
    jq -r '.id // .task.id // empty'
  else
    sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
  fi
}

extract_task_status() {
  if command -v jq >/dev/null 2>&1; then
    jq -r '(.items[0].status // .tasks[0].status // .data[0].status // .status // empty)'
  else
    sed -n 's/.*"status"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
  fi
}

is_terminal_status() {
  case "$1" in
    success|succeeded|completed|error|failed|canceled|cancelled) return 0 ;;
    *) return 1 ;;
  esac
}

# GET /v1/models
models() {
  curl -sS "${V1_BASE}/models" \
    -H "Authorization: Bearer ${API_KEY}" | pretty
}

poll() {
  local id="${1:-}"
  if [[ -z "${id}" ]]; then
    echo "Usage: $0 poll <task_id>" >&2
    exit 1
  fi

  curl -sS "${ROOT_URL}/api/creation-tasks?ids=${id}" \
    -H "Authorization: Bearer ${API_KEY}" | pretty
}

poll_until_done() {
  local id="$1"
  local attempt status response

  for ((attempt = 1; attempt <= POLL_MAX_ATTEMPTS; attempt++)); do
    response="$(curl -sS "${ROOT_URL}/api/creation-tasks?ids=${id}" \
      -H "Authorization: Bearer ${API_KEY}")"
    status="$(printf '%s' "${response}" | extract_task_status)"

    echo "poll ${attempt}/${POLL_MAX_ATTEMPTS}: task=${id} status=${status:-unknown}" >&2
    if is_terminal_status "${status}"; then
      printf '%s\n' "${response}" | pretty
      return 0
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  echo "Polling timed out locally. Task may still be running: ${id}" >&2
  poll "${id}"
}

# POST /api/creation-tasks/image-generations
generate() {
  local id response
  id="$(task_id img)"

  response="$(raw_json POST "/api/creation-tasks/image-generations" "{
    \"client_task_id\": \"${id}\",
    \"prompt\": \"a cat wearing a hat, digital art\",
    \"model\": \"auto\",
    \"n\": 1,
    \"size\": \"1:1\",
    \"output_format\": \"png\",
    \"visibility\": \"private\"
  }")"

  echo "submitted image generation task:" >&2
  printf '%s\n' "${response}" | pretty

  id="$(printf '%s' "${response}" | extract_task_id)"
  if [[ -z "${id}" ]]; then
    echo "Could not read task id from submit response." >&2
    exit 1
  fi
  poll_until_done "${id}"
}

# POST /api/creation-tasks/image-edits
edit() {
  local id response
  if [[ ! -f "${INPUT_IMAGE}" ]]; then
    echo "Missing input image: ${INPUT_IMAGE}" >&2
    echo "Set INPUT_IMAGE=/path/to/image.png or place ./input.png next to this script." >&2
    exit 1
  fi

  id="$(task_id edit)"
  response="$(curl -sS "${ROOT_URL}/api/creation-tasks/image-edits" \
    -H "Authorization: Bearer ${API_KEY}" \
    -F "client_task_id=${id}" \
    -F "image=@${INPUT_IMAGE}" \
    -F "prompt=add a hat to the cat" \
    -F "model=auto" \
    -F "n=1" \
    -F "size=1:1" \
    -F "output_format=png" \
    -F "visibility=private")"

  echo "submitted image edit task:" >&2
  printf '%s\n' "${response}" | pretty

  id="$(printf '%s' "${response}" | extract_task_id)"
  if [[ -z "${id}" ]]; then
    echo "Could not read task id from submit response." >&2
    exit 1
  fi
  poll_until_done "${id}"
}

usage() {
  echo "Usage: $0 {models|generate|edit|poll <task_id>}"
  echo ""
  echo "Environment overrides:"
  echo "  BASE_URL=https://your-domain.com or https://your-domain.com/v1"
  echo "  API_KEY=your-api-key"
  echo "  INPUT_IMAGE=./input.png"
  echo "  POLL_INTERVAL_SECONDS=5"
  echo "  POLL_MAX_ATTEMPTS=120"
}

case "${1:-}" in
  models) models ;;
  generate) generate ;;
  edit) edit ;;
  poll) poll "${2:-}" ;;
  *)
    usage
    exit 1
    ;;
esac

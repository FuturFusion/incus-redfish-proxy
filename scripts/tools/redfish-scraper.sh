#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat >&2 <<'EOF'
Usage: redfish-scraper.sh [options] OUTPUT_DIR

Scrape a Redfish API and store the JSON responses on disk, mirroring the
API path structure.

Options:
  --endpoint URL    Redfish base URL (default: http://localhost:8080)
  --user USER       basic-auth username
  --password PASS   basic-auth password
  --insecure        skip TLS certificate verification
  --exact FILE      file of exact endpoint paths to scrape (one per line)
  --prefix FILE     file of prefix paths to scrape recursively (one per line)
  --clear           remove OUTPUT_DIR before scraping (default: resume)
  --debug           verbose progress to stderr
  -h, --help        show this help and exit

At least one of --exact or --prefix is required.
Lines starting with '#' and blank lines in endpoint files are ignored.
EOF
}

log()   { printf '%s\n' "$*" >&2; }
err()   { printf '[ERROR] %s\n' "$*" >&2; }
stop()   { err "$*"; exit 1; }
debug() { [ "${opt_debug}" -eq 1 ] && printf '[DEBUG] %s\n' "$*" >&2 || true; }

check_deps() {
    local cmd
    if [ "${BASH_VERSINFO[0]}" -lt 4 ]; then
        stop "bash >= 4 required (found ${BASH_VERSION})"
    fi
    for cmd in curl jq grep sed; do
        command -v "$cmd" >/dev/null 2>&1 || stop "required command not found: ${cmd}"
    done
}

# Strip URL fragment and query string; ensure a leading slash.
normalize_path() {
    local p="${1%%#*}"
    p="${p%%\?*}"
    case "$p" in
        /*) ;;
        *) p="/$p" ;;
    esac
    printf '%s' "$p"
}

# Map a Redfish resource path to an output directory, rejecting unsafe paths.
# Prints the directory path on success (return 0) or returns 1 on unsafe input.
resource_dir() {
    local path="$1"
    local trimmed="${path#/}"
    trimmed="${trimmed%/}"
    local dir="${opt_output}"
    local -a segs
    local seg
    local saved_ifs="$IFS"

    IFS='/'
    read -ra segs <<< "$trimmed"
    IFS="$saved_ifs"

    [ "${#segs[@]}" -gt 0 ] || return 1

    for seg in "${segs[@]}"; do
        case "$seg" in
            ''|'.'|'..') return 1 ;;
        esac
        case "$seg" in
            *\\*) return 1 ;;
        esac
        dir="${dir}/${seg}"
    done

    case "$dir" in
        "${opt_output}/"*) ;;
        *) return 1 ;;
    esac

    printf '%s' "$dir"
}

# Print the path portion of raw if it refers to the same endpoint, else return 1.
same_endpoint() {
    local raw="$1"
    local path
    case "$raw" in
        http://*|https://*)
            local raw_host
            raw_host=$(printf '%s' "$raw" | sed 's|^https\?://\([^/?#]*\).*|\1|')
            [ "$raw_host" = "$base_host" ] || return 1
            path=$(printf '%s' "$raw" | sed 's|^https\?://[^/]*||')
            ;;
        /*)
            path="$raw"
            ;;
        *)
            path="/$raw"
            ;;
    esac
    [ -n "$path" ] || return 1
    printf '%s' "$path"
}

# Return 0 if path matches (equals or starts with /) one of the prefix endpoints.
matches_prefix() {
    local path="$1"
    local prefix
    [ "${#prefix_endpoints[@]}" -gt 0 ] || return 1
    for prefix in "${prefix_endpoints[@]}"; do
        [ "$path" = "$prefix" ] && return 0
        case "$path" in
            "${prefix}/"*) return 0 ;;
        esac
    done
    return 1
}

# Fetch a resource at path. Writes body to tmp_body, headers to tmp_headers.
# Prints the HTTP status code, or empty string on network failure.
fetch_resource() {
    local path="$1"
    local url="${opt_endpoint}${path}"
    local -a args=(-s -X GET -D "${tmp_headers}" -o "${tmp_body}" -w '%{http_code}')
    [ -n "${opt_user}" ] && args+=(-u "${opt_user}:${opt_password}")
    [ "${opt_insecure}" -eq 1 ] && args+=(-k)

    local code
    : > "${tmp_body}"
    : > "${tmp_headers}"
    code=$(curl "${args[@]}" "$url") || true

    printf '%s' "${code}"
}

# Print all @odata.id string values found anywhere in a JSON body file.
extract_body_links() {
    jq -r \
        '[.. | objects | select(has("@odata.id")) | .["@odata.id"] | select(type == "string")] | .[]' \
        "$1" 2>/dev/null || true
}

# Print all URIs from Link response headers in a curl header dump file.
extract_header_links() {
    grep -i '^Link:' "$1" 2>/dev/null \
        | grep -oE '<[^>]+>' \
        | sed 's/^<//; s/>$//' \
        || true
}

# ── defaults ────────────────────────────────────────────────────────────────

opt_endpoint="http://localhost:8080"
opt_user=""
opt_password=""
opt_insecure=0
opt_exact_file=""
opt_prefix_file=""
opt_clear=0
opt_debug=0
opt_output=""

# ── option parsing ──────────────────────────────────────────────────────────

while [ $# -gt 0 ]; do
    case "$1" in
        --endpoint)  shift; opt_endpoint="$1" ;;
        --user)      shift; opt_user="$1" ;;
        --password)  shift; opt_password="$1" ;;
        --insecure)  opt_insecure=1 ;;
        --exact)     shift; opt_exact_file="$1" ;;
        --prefix)    shift; opt_prefix_file="$1" ;;
        --clear)     opt_clear=1 ;;
        --debug)     opt_debug=1 ;;
        -h|--help)   usage; exit 0 ;;
        --)          shift; break ;;
        -*)          stop "unknown option: $1" ;;
        *)           break ;;
    esac
    shift
done

[ $# -eq 1 ] || stop "expected exactly one positional argument OUTPUT_DIR (got $#)"
opt_output="$1"

# ── validation ──────────────────────────────────────────────────────────────

check_deps

[ -n "${opt_exact_file}" ] || [ -n "${opt_prefix_file}" ] \
    || stop "at least one of --exact or --prefix is required"
[ -z "${opt_exact_file}"  ] || [ -f "${opt_exact_file}"  ] \
    || stop "exact endpoints file not found: ${opt_exact_file}"
[ -z "${opt_prefix_file}" ] || [ -f "${opt_prefix_file}" ] \
    || stop "prefix endpoints file not found: ${opt_prefix_file}"

base_host=$(printf '%s' "${opt_endpoint}" \
    | sed 's|^https\?://\([^/?#]*\).*|\1|')

# ── load endpoint lists ─────────────────────────────────────────────────────

declare -a exact_endpoints=()
declare -a prefix_endpoints=()

if [ -n "${opt_exact_file}" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
        line="${line%$'\r'}"
        case "$line" in ''|'#'*) continue ;; esac
        exact_endpoints+=("$line")
    done < "${opt_exact_file}"
fi

if [ -n "${opt_prefix_file}" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
        line="${line%$'\r'}"
        case "$line" in ''|'#'*) continue ;; esac
        prefix_endpoints+=("$line")
    done < "${opt_prefix_file}"
fi

# ── clear output if requested ───────────────────────────────────────────────

if [ "${opt_clear}" -eq 1 ] && [ -d "${opt_output}" ]; then
    log "Clearing output directory: ${opt_output}"
    rm -rf "${opt_output}"
fi

mkdir -p "${opt_output}"

# ── temp files ──────────────────────────────────────────────────────────────

tmp_body=$(mktemp)
tmp_headers=$(mktemp)
trap 'rm -f "${tmp_body}" "${tmp_headers}"' EXIT

# ── crawl ───────────────────────────────────────────────────────────────────

declare -A visited
declare -a queue_paths=()
declare -a queue_follow=()
queue_head=0
failures=0

enqueue() {
    queue_paths+=("$2")
    queue_follow+=("$1")
}

if [ "${#exact_endpoints[@]}" -gt 0 ]; then
    for ep in "${exact_endpoints[@]}"; do
        enqueue 0 "$ep"
    done
fi
if [ "${#prefix_endpoints[@]}" -gt 0 ]; then
    for ep in "${prefix_endpoints[@]}"; do
        enqueue 1 "$ep"
    done
fi

while [ "${queue_head}" -lt "${#queue_paths[@]}" ]; do
    path="${queue_paths[$queue_head]}"
    follow="${queue_follow[$queue_head]}"
    queue_head=$(( queue_head + 1 ))

    path=$(normalize_path "$path")

    if [ "${visited[$path]+x}" ]; then
        debug "Already visited: ${path}"
        continue
    fi
    visited["$path"]=1

    dir=$(resource_dir "$path") || {
        err "Unsafe path, skipping: ${path}"
        failures=$(( failures + 1 ))
        continue
    }

    index_file="${dir}/index.json"

    if [ -f "${index_file}" ]; then
        debug "Using cached: ${path}"
        if [ "${follow}" -eq 1 ]; then
            while IFS= read -r raw_link; do
                [ -z "$raw_link" ] && continue
                link=$(normalize_path "$raw_link")
                link_path=$(same_endpoint "$link") || continue
                matches_prefix "$link_path" || continue
                enqueue 1 "$link_path"
            done < <(extract_body_links "${index_file}")
        fi
        continue
    fi

    log "Fetching: ${path}"
    http_code=$(fetch_resource "$path")

    if [ -z "${http_code}" ]; then
        err "Network error fetching: ${path}"
        failures=$(( failures + 1 ))
        continue
    fi

    case "${http_code}" in
        2??) ;;
        *)
            err "HTTP ${http_code}: ${path}"
            failures=$(( failures + 1 ))
            continue
            ;;
    esac

    content_type=$(grep -i '^Content-Type:' "${tmp_headers}" 2>/dev/null | head -1 \
        | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r') || true
    if ! printf '%s' "${content_type}" | grep -q 'application/json'; then
        debug "Skipping non-JSON (${content_type:-none}): ${path}"
        continue
    fi

    mkdir -p "${dir}"
    if ! jq '.' "${tmp_body}" > "${index_file}" 2>/dev/null; then
        err "Failed to parse JSON for: ${path}"
        failures=$(( failures + 1 ))
        rm -f "${index_file}"
        continue
    fi

    if [ "${follow}" -eq 1 ]; then
        while IFS= read -r raw_link; do
            [ -z "$raw_link" ] && continue
            link=$(normalize_path "$raw_link")
            link_path=$(same_endpoint "$link") || continue
            matches_prefix "$link_path" || continue
            enqueue 1 "$link_path"
        done < <(extract_body_links "${index_file}")
    fi
done

# ── summary ─────────────────────────────────────────────────────────────────

if [ "${failures}" -eq 0 ]; then
    log "Scrape completed successfully."
    exit 0
else
    log "Scrape completed with ${failures} failure(s)."
    exit 1
fi

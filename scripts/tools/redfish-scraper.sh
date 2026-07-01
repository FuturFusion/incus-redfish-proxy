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
  --input FILE      paths/patterns to scrape, one per line (repeatable)
  --clear           remove OUTPUT_DIR before scraping (default: resume)
  --debug           verbose progress to stderr
  -h, --help        show this help and exit

--input is required and may be given more than once.
Lines starting with '#' and blank lines in input files are ignored.

Pattern syntax:
  /exact/path         fetch this resource once, no link following
  /path/*/resource    fetch for each * discovered under /path
  /path/**            recursively scrape everything under /path
EOF
}

log()   { printf '%s\n' "$*" >&2; }
err()   { printf '[ERROR] %s\n' "$*" >&2; }
stop()  { err "$*"; exit 1; }
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

# Return 0 if path matches (equals or starts with /) one of the recursive prefixes.
matches_prefix() {
    local path="$1"
    local prefix
    [ "${#recursive_prefixes[@]}" -gt 0 ] || return 1
    for prefix in "${recursive_prefixes[@]}"; do
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

# Expand wildcard patterns registered for cur_path using links from index_file.
# For each direct child discovered:
#   - More wildcards remain: register a new wildcard parent and enqueue its stem.
#   - Literal suffix remains: enqueue the derived exact target.
#   - Pattern ends at the wildcard: enqueue the child itself.
expand_wildcards() {
    local cur_path="$1" index_file="$2"
    [ "${wildcard_parents[$cur_path]+x}" ] || return 0

    local -a links
    mapfile -t links < <(extract_body_links "$index_file")

    local pattern stem after_pos rest link child_part
    local next_lit next_pos new_stem new_rest new_pat derived

    while IFS= read -r pattern; do
        [ -z "$pattern" ] && continue
        stem="${pattern%%/\**}"
        after_pos=$(( ${#stem} + 2 ))   # skip past "/*" in the pattern
        rest="${pattern:$after_pos}"

        for link in "${links[@]}"; do
            case "$link" in "${cur_path}/"*) ;; *) continue ;; esac
            child_part="${link#${cur_path}/}"
            case "$child_part" in */*) continue ;; esac  # not a direct child

            case "$rest" in
                *"/*"*)
                    # More wildcards: advance to the next wildcard level.
                    next_lit="${rest%%/\**}"
                    next_pos=$(( ${#next_lit} + 2 ))
                    new_rest="${rest:$next_pos}"
                    new_stem="${link}${next_lit}"
                    new_pat="${new_stem}/*${new_rest}"
                    wildcard_parents["$new_stem"]+="${new_pat}"$'\n'
                    enqueue 0 "$new_stem"
                    ;;
                ?*)
                    derived="${link}${rest}"
                    enqueue 0 "$derived"
                    ;;
                *)
                    enqueue 0 "$link"
                    ;;
            esac
        done
    done <<< "${wildcard_parents[$cur_path]}"
}

# ── defaults ────────────────────────────────────────────────────────────────

opt_endpoint="http://localhost:8080"
opt_user=""
opt_password=""
opt_insecure=0
declare -a opt_input_files=()
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
        --input)     shift; opt_input_files+=("$1") ;;
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

[ "${#opt_input_files[@]}" -gt 0 ] || stop "--input is required"
for _f in "${opt_input_files[@]}"; do
    [ -f "$_f" ] || stop "input file not found: ${_f}"
done

base_host=$(printf '%s' "${opt_endpoint}" \
    | sed 's|^https\?://\([^/?#]*\).*|\1|')

# ── load endpoint lists ─────────────────────────────────────────────────────

declare -a exact_endpoints=()
declare -a recursive_prefixes=()
declare -A wildcard_parents=()

for _input_file in "${opt_input_files[@]}"; do
    while IFS= read -r line || [ -n "$line" ]; do
        line="${line%$'\r'}"
        case "$line" in ''|'#'*) continue ;; esac
        case "$line" in
            *"/**")
                recursive_prefixes+=("${line%/**}")
                ;;
            *"/*"*)
                stem="${line%%/\**}"
                wildcard_parents["$stem"]+="${line}"$'\n'
                ;;
            *)
                exact_endpoints+=("$line")
                ;;
        esac
    done < "$_input_file"
done

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

for ep in "${exact_endpoints[@]}";    do enqueue 0 "$ep"; done
for pf in "${recursive_prefixes[@]}"; do enqueue 1 "$pf"; done
for st in "${!wildcard_parents[@]}";  do enqueue 0 "$st"; done

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

    # A path under a recursive prefix always gets link-following regardless of
    # how it was first enqueued (e.g. via wildcard expansion with follow=0).
    matches_prefix "$path" && follow=1

    dir=$(resource_dir "$path") || {
        err "Unsafe path, skipping: ${path}"
        failures=$(( failures + 1 ))
        continue
    }

    index_file="${dir}/index.json"
    not_found_file="${dir}/index.404.json"

    if [ -f "${not_found_file}" ]; then
        debug "Skipping previously 404'd: ${path}"
        continue
    fi

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
        while IFS= read -r raw_uri; do
            [ -z "$raw_uri" ] && continue
            uri=$(normalize_path "$raw_uri")
            uri_path=$(same_endpoint "$uri") || continue
            enqueue 0 "$uri_path"
        done < <(extract_location_uris "${index_file}")
        expand_wildcards "$path" "${index_file}"
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
        404)
            mkdir -p "${dir}"
            printf '{}\n' > "${not_found_file}"
            debug "Resource not found (404), saved marker: ${path}"
            continue
            ;;
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
    while IFS= read -r raw_uri; do
        [ -z "$raw_uri" ] && continue
        uri=$(normalize_path "$raw_uri")
        uri_path=$(same_endpoint "$uri") || continue
        enqueue 0 "$uri_path"
    done < <(extract_location_uris "${index_file}")
    expand_wildcards "$path" "${index_file}"
done

# ── summary ─────────────────────────────────────────────────────────────────

if [ "${failures}" -eq 0 ]; then
    log "Scrape completed successfully."
    exit 0
else
    log "Scrape completed with ${failures} failure(s)."
    exit 1
fi

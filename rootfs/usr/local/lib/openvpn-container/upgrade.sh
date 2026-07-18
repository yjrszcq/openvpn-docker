#!/usr/bin/env bash

OVPN_GITHUB_REPOSITORY="${OVPN_GITHUB_REPOSITORY:-yjrszcq/openvpn-docker}"
OVPN_GITHUB_API_URL="${OVPN_GITHUB_API_URL:-https://api.github.com/repos/$OVPN_GITHUB_REPOSITORY}"
OVPN_MANAGEMENT_STORE="${OVPN_MANAGEMENT_STORE:-$OVPN_DATA_DIR/repair/.scripts}"
OVPN_MANAGEMENT_KEYRING="${OVPN_MANAGEMENT_KEYRING:-/usr/local/share/openvpn-container/trusted-management-keys}"
OVPN_MANAGEMENT_VERIFIER="${OVPN_MANAGEMENT_VERIFIER:-/usr/local/lib/openvpn-verify-management-release.sh}"
OVPN_BOOTSTRAP_LIB="${OVPN_BOOTSTRAP_LIB:-/usr/local/lib/openvpn-bootstrap.sh}"

ovpn_upgrade_usage() {
  cat <<'EOF'
Usage: ovpn upgrade [--check] [--version VERSION] [--json] [--yes]
       ovpn upgrade --rollback [--yes]

Update signed management code without replacing or reloading OpenVPN.
EOF
}

ovpn_upgrade_curl() {
  local -a arguments
  arguments=(-fsSL --connect-timeout 15 --max-time 120 -H 'Accept: application/vnd.github+json')
  if [ -n "${OVPN_GITHUB_TOKEN:-}" ]; then
    arguments+=(-H "Authorization: Bearer $OVPN_GITHUB_TOKEN")
  fi
  "${OVPN_CURL_BIN:-curl}" "${arguments[@]}" "$@"
}

ovpn_upgrade_manifest_value() {
  awk -F= -v key="$2" '$1 == key { print $2 }' "$1"
}

ovpn_upgrade_runtime_facts() {
  OVPN_UPGRADE_CURRENT_VERSION="${OVPN_ACTIVE_MANAGEMENT_VERSION:-$(ovpn_version_short)}"
  OVPN_UPGRADE_PLATFORM_API="$(jq -r '.platform_api // empty' "$OVPN_BUILD_INFO")"
  OVPN_UPGRADE_RUNTIME_VERSION="$(ovpn_runtime_version)" || return 1
  ovpn_semver_normalize "$OVPN_UPGRADE_CURRENT_VERSION" >/dev/null || return 1
  [[ "$OVPN_UPGRADE_PLATFORM_API" =~ ^[1-9][0-9]*$ ]] || return 1
  ovpn_schema_probe
  case "$OVPN_SCHEMA_STATUS" in
  EMPTY) OVPN_UPGRADE_DATA_SCHEMA="$OVPN_CURRENT_DATA_SCHEMA" ;;
  CURRENT | CURRENT_INCOMPLETE) OVPN_UPGRADE_DATA_SCHEMA="${OVPN_SCHEMA_PROJECT_VERSION:-${OVPN_SCHEMA_FILE_VERSION:-$OVPN_CURRENT_DATA_SCHEMA}}" ;;
  OLD)
    [ -n "${OVPN_UPGRADE_SCHEMA_CHANGE_TARGET:-}" ] || return 1
    [ "$OVPN_SCHEMA_PROJECT_VERSION" = "$OVPN_SCHEMA_FILE_VERSION" ] || return 1
    OVPN_UPGRADE_DATA_SCHEMA="$OVPN_SCHEMA_PROJECT_VERSION"
    ;;
  *) return 1 ;;
  esac
}

ovpn_upgrade_manifest_compatible() {
  local manifest="$1" tag_version="$2"
  local version schema platform_min platform_max openvpn_versions features
  local feature help_output supported=false supported_version
  local -a target_features target_openvpn_versions

  version="$(ovpn_upgrade_manifest_value "$manifest" MANAGEMENT_VERSION)"
  schema="$(ovpn_upgrade_manifest_value "$manifest" DATA_SCHEMA)"
  platform_min="$(ovpn_upgrade_manifest_value "$manifest" PLATFORM_API_MIN)"
  platform_max="$(ovpn_upgrade_manifest_value "$manifest" PLATFORM_API_MAX)"
  openvpn_versions="$(ovpn_upgrade_manifest_value "$manifest" OPENVPN_SUPPORTED_VERSIONS)"
  features="$(ovpn_upgrade_manifest_value "$manifest" REQUIRED_FEATURES)"
  OVPN_UPGRADE_REJECTION=''

  if [ "$version" != "$tag_version" ]; then
    OVPN_UPGRADE_REJECTION='tag and signed management version differ'
    return 1
  fi
  if [ "$schema" != "$OVPN_UPGRADE_DATA_SCHEMA" ]; then
    if [ -z "${OVPN_UPGRADE_SCHEMA_CHANGE_TARGET:-}" ] ||
      [ "$schema" != "$OVPN_UPGRADE_SCHEMA_CHANGE_TARGET" ]; then
      OVPN_UPGRADE_REJECTION="data schema $schema requires ovpn migrate"
      return 1
    fi
  fi
  if [ "$OVPN_UPGRADE_PLATFORM_API" -lt "$platform_min" ] || [ "$OVPN_UPGRADE_PLATFORM_API" -gt "$platform_max" ]; then
    OVPN_UPGRADE_REJECTION="platform API $OVPN_UPGRADE_PLATFORM_API is outside [$platform_min,$platform_max]"
    return 1
  fi
  IFS=, read -ra target_openvpn_versions <<<"$openvpn_versions"
  for supported_version in "${target_openvpn_versions[@]}"; do
    if [ "$OVPN_UPGRADE_RUNTIME_VERSION" = "$supported_version" ]; then
      supported=true
      break
    fi
  done
  if [ "$supported" != true ]; then
    OVPN_UPGRADE_REJECTION="OpenVPN $OVPN_UPGRADE_RUNTIME_VERSION is not in verified set [$openvpn_versions]"
    return 1
  fi
  help_output="$(ovpn_compatibility_runtime_help)"
  IFS=, read -ra target_features <<<"$features"
  for feature in "${target_features[@]}"; do
    if ! ovpn_compatibility_probe_feature "$feature" "$help_output"; then
      OVPN_UPGRADE_REJECTION="OpenVPN lacks required feature $feature"
      return 1
    fi
  done
  return 0
}

ovpn_upgrade_download_manifest() {
  local releases_json="$1" version="$2" output="$3" manifest_url signature_url

  manifest_url="$(jq -r --arg tag "v$version" '.[] | select(.tag_name == $tag) | .assets[] | select(.name == "management-release.env") | .browser_download_url' "$releases_json" | head -1)"
  signature_url="$(jq -r --arg tag "v$version" '.[] | select(.tag_name == $tag) | .assets[] | select(.name == "management-release.env.sig") | .browser_download_url' "$releases_json" | head -1)"
  [ -n "$manifest_url" ] && [ -n "$signature_url" ] || return 1
  mkdir -p "$output"
  ovpn_upgrade_curl -o "$output/management-release.env" "$manifest_url" || return 69
  ovpn_upgrade_curl -o "$output/management-release.env.sig" "$signature_url" || return 69
  "$OVPN_MANAGEMENT_VERIFIER" --release-dir "$output" --keyring "$OVPN_MANAGEMENT_KEYRING" --manifest-only >/dev/null || return 74
}

ovpn_upgrade_select_release() {
  local requested="$1" work="$2" releases_json
  local version comparison candidate_dir download_status verification_failure=false
  releases_json="$work/releases.json"

  ovpn_upgrade_curl -o "$releases_json" "$OVPN_GITHUB_API_URL/releases?per_page=100" || return 69
  jq -e 'type == "array"' "$releases_json" >/dev/null 2>&1 || return 69
  : >"$work/skipped"
  OVPN_UPGRADE_SELECTED_VERSION=''
  while IFS= read -r version; do
    ovpn_semver_normalize "$version" >/dev/null 2>&1 || continue
    comparison="$(ovpn_semver_compare "$version" "$OVPN_UPGRADE_CURRENT_VERSION")" || continue
    [ "$comparison" = 1 ] || continue
    [ -z "$requested" ] || [ "$version" = "$requested" ] || continue
    candidate_dir="$work/releases/$version"
    if ovpn_upgrade_download_manifest "$releases_json" "$version" "$candidate_dir"; then
      :
    else
      download_status=$?
      [ "$download_status" -ne 69 ] || return 69
      verification_failure=true
      printf '%s|signature or manifest verification failed\n' "$version" >>"$work/skipped"
      [ -z "$requested" ] || return 74
      continue
    fi
    if ! ovpn_upgrade_manifest_compatible "$candidate_dir/management-release.env" "$version"; then
      printf '%s|%s\n' "$version" "$OVPN_UPGRADE_REJECTION" >>"$work/skipped"
      continue
    fi
    if [ -z "$OVPN_UPGRADE_SELECTED_VERSION" ] ||
      [ "$(ovpn_semver_compare "$version" "$OVPN_UPGRADE_SELECTED_VERSION")" = 1 ]; then
      OVPN_UPGRADE_SELECTED_VERSION="$version"
      OVPN_UPGRADE_SELECTED_DIR="$candidate_dir"
    fi
  done < <(jq -r '.[] | select(.draft == false and .prerelease == false) | .tag_name | select(startswith("v")) | ltrimstr("v")' "$releases_json")

  if [ -n "$requested" ] && [ -z "$OVPN_UPGRADE_SELECTED_VERSION" ]; then
    return 78
  fi
  if [ -z "$OVPN_UPGRADE_SELECTED_VERSION" ] && [ "$verification_failure" = true ]; then
    return 74
  fi
  return 0
}

ovpn_upgrade_print_result() {
  local json="$1" mode="$2" selected="${OVPN_UPGRADE_SELECTED_VERSION:-}"
  local skipped_json='[]' skipped_version skipped_reason
  local target_schema='' asset_name=''

  if [ "$json" = true ]; then
    if [ -s "${OVPN_UPGRADE_SKIPPED_FILE:-}" ]; then
      skipped_json="$(jq -Rn '[inputs | split("|") | {version:.[0],reason:.[1]}]' <"$OVPN_UPGRADE_SKIPPED_FILE")"
    fi
    if [ -n "$selected" ]; then
      target_schema="$(ovpn_upgrade_manifest_value "$OVPN_UPGRADE_SELECTED_DIR/management-release.env" DATA_SCHEMA)"
      asset_name="$(ovpn_upgrade_manifest_value "$OVPN_UPGRADE_SELECTED_DIR/management-release.env" ASSET_NAME)"
    fi
    jq -n \
      --arg current "$OVPN_UPGRADE_CURRENT_VERSION" \
      --arg target "$selected" \
      --arg mode "$mode" \
      --arg platform "$OVPN_UPGRADE_PLATFORM_API" \
      --arg openvpn "$OVPN_UPGRADE_RUNTIME_VERSION" \
      --arg current_schema "$OVPN_UPGRADE_DATA_SCHEMA" \
      --arg target_schema "$target_schema" \
      --arg asset "$asset_name" \
      --argjson compatible "$([ -n "$selected" ] && printf true || printf false)" \
      --argjson skipped "$skipped_json" \
      '{current_version:$current,target_version:(if $target == "" then null else $target end),mode:$mode,compatible:$compatible,platform_api:($platform|tonumber),openvpn_version:$openvpn,current_schema:($current_schema|tonumber),target_schema:(if $target_schema == "" then null else ($target_schema|tonumber) end),schema_change:(if $target_schema == "" then null else $target_schema != $current_schema end),download_asset:(if $asset == "" then null else $asset end),skipped:$skipped}'
  elif [ -n "$selected" ]; then
    printf 'management update available: %s -> %s\n' "$OVPN_UPGRADE_CURRENT_VERSION" "$selected"
  else
    printf 'management code is already at the latest compatible version (%s)\n' "$OVPN_UPGRADE_CURRENT_VERSION"
  fi
  if [ "$json" = false ] && [ -s "${OVPN_UPGRADE_SKIPPED_FILE:-}" ]; then
    while IFS='|' read -r skipped_version skipped_reason; do
      printf 'skipped %s: %s\n' "$skipped_version" "$skipped_reason"
    done <"$OVPN_UPGRADE_SKIPPED_FILE"
  fi
}

ovpn_upgrade_download_bundle() {
  local releases_json="$1" version="$2" output="$3" url
  url="$(jq -r --arg tag "v$version" '.[] | select(.tag_name == $tag) | .assets[] | select(.name == "management-bundle.tar.gz") | .browser_download_url' "$releases_json" | head -1)"
  [ -n "$url" ] || return 69
  ovpn_upgrade_curl -o "$output/management-bundle.tar.gz" "$url" || return 69
  "$OVPN_MANAGEMENT_VERIFIER" --release-dir "$output" --keyring "$OVPN_MANAGEMENT_KEYRING" >/dev/null || return 74
}

ovpn_upgrade_write_pointer() {
  local name="$1" value="$2" temporary
  temporary="$OVPN_MANAGEMENT_STORE/.$name.$$"
  printf '%s\n' "$value" >"$temporary" || return 1
  chmod 600 "$temporary" || return 1
  mv -f "$temporary" "$OVPN_MANAGEMENT_STORE/$name" || return 1
}

ovpn_upgrade_write_selector_transaction() {
  local state="$1" old_active="$2" old_previous="$3" new_active="$4" new_previous="$5"
  local transaction="$OVPN_MANAGEMENT_STORE/transactions/activation.env" temporary
  temporary="$OVPN_MANAGEMENT_STORE/transactions/.activation.$$"
  cat >"$temporary" <<EOF || return 1
STATE=$state
OLD_ACTIVE=$old_active
OLD_PREVIOUS=$old_previous
NEW_ACTIVE=$new_active
NEW_PREVIOUS=$new_previous
EOF
  chmod 600 "$temporary" || return 1
  mv -f "$temporary" "$transaction" || return 1
}

ovpn_upgrade_prune_releases() {
  local active="$1" previous="$2" directory version
  for directory in "$OVPN_MANAGEMENT_STORE"/releases/*; do
    [ -d "$directory" ] || continue
    version="${directory##*/}"
    [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue
    [ "$version" = "$active" ] && continue
    [ "$version" = "$previous" ] && continue
    rm -rf "$directory" || return 1
  done
}

ovpn_upgrade_start_management_broker() {
  local script="$1" python_bin raw_log broker_pid pid_file temporary

  python_bin="${OVPN_PYTHON_BIN:-python3}"
  raw_log="${OVPN_RAW_LOG_FILE:-$OVPN_DATA_DIR/logs/openvpn.log}"
  pid_file="$OVPN_RUNTIME_DIR/management-broker.pid"
  command -v setsid >/dev/null 2>&1 || return 1
  command -v "$python_bin" >/dev/null 2>&1 || return 1
  ovpn_config_load || return 1
  mkdir -p "$OVPN_RUNTIME_DIR" "$OVPN_DATA_DIR/logs" || return 1
  setsid "$python_bin" "$script" \
    --listen "$OVPN_MANAGEMENT_SOCKET" \
    --backend "$OVPN_OPENVPN_MANAGEMENT_SOCKET" \
    --raw-log "$raw_log" \
    --max-bytes "$OVPN_LOG_MAX_BYTES" \
    --backups "$OVPN_LOG_BACKUPS" \
    --reload-script "$script" \
    </dev/null >>"$OVPN_DATA_DIR/logs/management-broker.log" 2>&1 &
  broker_pid=$!
  temporary="${pid_file}.reload.$$"
  printf '%s\n' "$broker_pid" >"$temporary" || return 1
  chmod 600 "$temporary" || return 1
  mv -f "$temporary" "$pid_file" || return 1
}

ovpn_upgrade_reload_management_broker() {
  local pid_file="$OVPN_RUNTIME_DIR/management-broker.pid"
  local script="${OVPN_MANAGEMENT_BROKER_RELOAD_SCRIPT:-$OVPN_RUNTIME_MANAGEMENT_ROOT/current/lib/management-broker.py}"
  local broker_pid old_signature='' new_signature='' deadline

  [ -e "$pid_file" ] || return 0
  [ -r "$script" ] || return 1
  "${OVPN_PYTHON_BIN:-python3}" -c \
    'import pathlib,sys; compile(pathlib.Path(sys.argv[1]).read_bytes(), sys.argv[1], "exec")' \
    "$script" || return 1
  IFS= read -r broker_pid <"$pid_file" || return 1
  [[ "$broker_pid" =~ ^[1-9][0-9]*$ ]] || return 1
  if kill -0 "$broker_pid" >/dev/null 2>&1; then
    old_signature="$(stat -c '%i:%y' "$OVPN_MANAGEMENT_SOCKET" 2>/dev/null || true)"
    kill -HUP "$broker_pid" >/dev/null 2>&1 || return 1
  else
    ovpn_upgrade_start_management_broker "$script" || return 1
    IFS= read -r broker_pid <"$pid_file" || return 1
  fi
  deadline=$((SECONDS + 10))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if ! kill -0 "$broker_pid" >/dev/null 2>&1; then
      ovpn_upgrade_start_management_broker "$script" || return 1
      IFS= read -r broker_pid <"$pid_file" || return 1
      old_signature=''
    fi
    new_signature="$(stat -c '%i:%y' "$OVPN_MANAGEMENT_SOCKET" 2>/dev/null || true)"
    if [ -n "$new_signature" ] && { [ -z "$old_signature" ] || [ "$new_signature" != "$old_signature" ]; } &&
      ovpn_management_socket_request "$OVPN_MANAGEMENT_SOCKET" broker-health 2>/dev/null |
      grep -Fq 'SUCCESS: broker connected to OpenVPN'; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

ovpn_upgrade_activate() {
  local version="$1" selected_dir="$2" mode="${3:-activate}" lock_held="${4:-false}"
  local release_target stage previous current_source persisted_active old_previous

  current_source=embedded
  if [ "${OVPN_MANAGEMENT_SOURCE:-embedded}" = online ]; then
    current_source="$OVPN_UPGRADE_CURRENT_VERSION"
  fi
  # shellcheck source=/usr/local/lib/openvpn-bootstrap.sh
  . "$OVPN_BOOTSTRAP_LIB"
  mkdir -p "$OVPN_MANAGEMENT_STORE/releases" "$OVPN_MANAGEMENT_STORE/transactions" || return 74
  chmod 700 "$OVPN_MANAGEMENT_STORE" "$OVPN_MANAGEMENT_STORE/releases" "$OVPN_MANAGEMENT_STORE/transactions" || return 74
  if [ "$lock_held" = false ]; then
    exec {management_lock_fd}>"$OVPN_MANAGEMENT_STORE/.management.lock" || return 74
    flock -n -x "$management_lock_fd" || {
      ovpn_log 'another management update is in progress'
      return 74
    }
  else
    [ "$lock_held" = true ] || return 74
  fi
  persisted_active="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/active")" || persisted_active=embedded
  if [ "$persisted_active" = "$version" ]; then
    ovpn_bootstrap_activate_online "$version" || return 74
    [ "$lock_held" = true ] || flock -u "$management_lock_fd"
    return 0
  fi
  if [ "$persisted_active" != "$current_source" ]; then
    ovpn_log "active management release changed concurrently to $persisted_active"
    return 74
  fi

  release_target="$OVPN_MANAGEMENT_STORE/releases/$version"
  if [ ! -d "$release_target" ]; then
    stage="$OVPN_MANAGEMENT_STORE/transactions/.release-$version.$$"
    mkdir -p "$stage" || return 74
    chmod 700 "$stage" || return 74
    cp "$selected_dir/management-bundle.tar.gz" "$selected_dir/management-release.env" \
      "$selected_dir/management-release.env.sig" "$stage/" || return 74
    chmod 600 "$stage"/* || return 74
    mv "$stage" "$release_target" || return 74
  elif ! cmp -s "$selected_dir/management-release.env" "$release_target/management-release.env" ||
    ! cmp -s "$selected_dir/management-release.env.sig" "$release_target/management-release.env.sig"; then
    ovpn_log "persisted management release $version differs from the selected signed release"
    return 74
  fi
  "$OVPN_MANAGEMENT_VERIFIER" --release-dir "$release_target" --keyring "$OVPN_MANAGEMENT_KEYRING" >/dev/null || return 74
  if [ "$mode" = migration-prepare ]; then
    ovpn_bootstrap_activate_online "$version" hydrate-for-migration || return 74
  else
    [ "$mode" = activate ] || return 74
    ovpn_bootstrap_activate_online "$version" hydrate-only || return 74
  fi
  OVPN_LIB_DIR="$OVPN_BOOTSTRAP_SELECTED_ROOT/lib" \
    OVPN_ACTIVE_MANAGEMENT_VERSION="$version" OVPN_MANAGEMENT_SOURCE=online \
    "$OVPN_BOOTSTRAP_SELECTED_ROOT/lib/cli.sh" help >/dev/null || return 74
  if [ "$mode" = migration-prepare ]; then
    [ "$lock_held" = true ] || flock -u "$management_lock_fd"
    return 0
  fi

  previous="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/active")" || previous="$current_source"
  old_previous="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/previous")" || old_previous=embedded
  ovpn_upgrade_write_selector_transaction prepared "$persisted_active" "$old_previous" "$version" "$previous" || return 74
  ovpn_upgrade_write_pointer previous "$previous" || return 74
  ovpn_upgrade_write_pointer active "$version" || return 74
  ovpn_upgrade_write_selector_transaction committed "$persisted_active" "$old_previous" "$version" "$previous" || return 74
  ovpn_bootstrap_activate_online "$version" || return 74
  rm -f "$OVPN_MANAGEMENT_STORE/transactions/activation.env" || return 74
  ovpn_upgrade_prune_releases "$version" "$previous" ||
    ovpn_log 'unable to prune an older management release'
  [ "$lock_held" = true ] || flock -u "$management_lock_fd"
  return 0
}

ovpn_upgrade_rollback() {
  local yes="$1" previous active manifest embedded_schema old_previous

  [ "$yes" = true ] || {
    [ -t 0 ] || {
      ovpn_log 'non-interactive rollback requires --yes'
      return 64
    }
    printf 'Roll back management code? [y/N] ' >&2
    read -r answer
    [[ "$answer" =~ ^[Yy]$ ]] || return 64
  }
  # shellcheck source=/usr/local/lib/openvpn-bootstrap.sh
  . "$OVPN_BOOTSTRAP_LIB"
  mkdir -p "$OVPN_MANAGEMENT_STORE/transactions" || return 74
  chmod 700 "$OVPN_MANAGEMENT_STORE" "$OVPN_MANAGEMENT_STORE/transactions" || return 74
  exec {management_lock_fd}>"$OVPN_MANAGEMENT_STORE/.management.lock" || return 74
  flock -n -x "$management_lock_fd" || {
    ovpn_log 'another management update is in progress'
    return 74
  }
  previous="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/previous")" || {
    ovpn_log 'no previous management release is available'
    return 78
  }
  active="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/active")" || active=embedded
  old_previous="$previous"
  ovpn_upgrade_runtime_facts || return 78
  if [ "$previous" = embedded ]; then
    ovpn_bootstrap_read_metadata || return 78
    embedded_schema="$OVPN_BOOTSTRAP_DATA_SCHEMA"
    [ "$embedded_schema" = "$OVPN_UPGRADE_DATA_SCHEMA" ] || {
      ovpn_log "embedded management code does not support data schema $OVPN_UPGRADE_DATA_SCHEMA"
      return 78
    }
    ovpn_compatibility_runtime_supported || return 78
    ovpn_bootstrap_activate_embedded || return 74
  else
    manifest="$OVPN_MANAGEMENT_STORE/releases/$previous/management-release.env"
    "$OVPN_MANAGEMENT_VERIFIER" --release-dir "$OVPN_MANAGEMENT_STORE/releases/$previous" \
      --keyring "$OVPN_MANAGEMENT_KEYRING" >/dev/null || return 78
    ovpn_upgrade_manifest_compatible "$manifest" "$previous" || {
      ovpn_log "previous management release $previous is incompatible: $OVPN_UPGRADE_REJECTION"
      return 78
    }
    ovpn_bootstrap_activate_online "$previous" || {
      ovpn_log "previous management release $previous is incompatible or invalid"
      return 78
    }
  fi
  ovpn_upgrade_write_selector_transaction prepared "$active" "$old_previous" "$previous" "$active" || return 74
  ovpn_upgrade_write_pointer active "$previous" || return 74
  ovpn_upgrade_write_pointer previous "$active" || return 74
  ovpn_upgrade_write_selector_transaction committed "$active" "$old_previous" "$previous" "$active" || return 74
  rm -f "$OVPN_MANAGEMENT_STORE/transactions/activation.env" || return 74
  flock -u "$management_lock_fd"
  if ! ovpn_upgrade_reload_management_broker; then
    ovpn_log 'management broker failed to reload after rollback'
    return 74
  fi
  if declare -F ovpn_event_write >/dev/null 2>&1; then
    ovpn_event_write management_upgrade rollback applied "" "" \
      "$(jq -cn --arg from "$active" --arg to "$previous" \
        '{from_version:$from,to_version:$to}')" || true
  fi
  printf 'management code rolled back to %s\n' "$previous"
}

ovpn_upgrade_command() {
  local check=false json=false yes=false requested='' rollback=false work status
  local skipped_version skipped_reason answer requested_comparison

  if ovpn_help_requested "$@"; then
    ovpn_upgrade_usage
    return 0
  fi
  while [ "$#" -gt 0 ]; do
    case "$1" in
    --check) check=true ;;
    --json) json=true ;;
    --yes) yes=true ;;
    --rollback) rollback=true ;;
    --version)
      shift
      [ "$#" -gt 0 ] || {
        ovpn_log 'missing value for --version'
        return 64
      }
      requested="$1"
      ;;
    *)
      ovpn_log "unknown upgrade option '$1'"
      return 64
      ;;
    esac
    shift
  done
  if [ "$rollback" = true ]; then
    [ "$check" = false ] && [ "$json" = false ] && [ -z "$requested" ] || return 64
    ovpn_upgrade_rollback "$yes"
    return $?
  fi
  if [ "$check" = true ] && [ "$yes" = true ]; then
    ovpn_log '--yes is not valid with --check'
    return 64
  fi
  ovpn_semver_normalize "${requested:-0.0.0}" >/dev/null 2>&1 || {
    ovpn_log 'invalid management version'
    return 64
  }
  ovpn_upgrade_runtime_facts || {
    ovpn_log 'unable to determine runtime compatibility'
    return 78
  }
  if [ -n "$requested" ]; then
    requested_comparison="$(ovpn_semver_compare "$requested" "$OVPN_UPGRADE_CURRENT_VERSION")"
    if [ "$requested_comparison" = 0 ]; then
      if [ "$json" = true ]; then
        jq -n --arg version "$OVPN_UPGRADE_CURRENT_VERSION" \
          --arg platform "$OVPN_UPGRADE_PLATFORM_API" \
          --arg openvpn "$OVPN_UPGRADE_RUNTIME_VERSION" \
          --arg schema "$OVPN_UPGRADE_DATA_SCHEMA" \
          '{current_version:$version,target_version:$version,mode:"no-op",compatible:true,platform_api:($platform|tonumber),openvpn_version:$openvpn,current_schema:($schema|tonumber),target_schema:($schema|tonumber),schema_change:false,download_asset:null,skipped:[]}'
      else
        printf 'management code %s is already active\n' "$OVPN_UPGRADE_CURRENT_VERSION"
      fi
      return 0
    fi
    if [ "$requested_comparison" != 1 ]; then
      ovpn_log 'target version must not be older than the active management version'
      return 64
    fi
  fi
  work="$(mktemp -d)" || return 74
  if ovpn_upgrade_select_release "$requested" "$work"; then
    :
  else
    status=$?
    if [ -s "$work/skipped" ]; then
      while IFS='|' read -r skipped_version skipped_reason; do
        ovpn_log "skipped $skipped_version: $skipped_reason"
      done <"$work/skipped"
    fi
    rm -rf "$work"
    return "$status"
  fi
  OVPN_UPGRADE_SKIPPED_FILE="$work/skipped"
  if [ "$check" = true ] || [ -z "$OVPN_UPGRADE_SELECTED_VERSION" ]; then
    ovpn_upgrade_print_result "$json" check
    rm -rf "$work"
    return 0
  fi
  if [ "$yes" = false ]; then
    if [ ! -t 0 ]; then
      ovpn_log 'non-interactive upgrade requires --yes'
      rm -rf "$work"
      return 64
    fi
    printf 'Upgrade management code to %s? [y/N] ' "$OVPN_UPGRADE_SELECTED_VERSION" >&2
    read -r answer
    if ! [[ "$answer" =~ ^[Yy]$ ]]; then
      rm -rf "$work"
      return 64
    fi
  fi
  ovpn_upgrade_download_bundle "$work/releases.json" "$OVPN_UPGRADE_SELECTED_VERSION" "$OVPN_UPGRADE_SELECTED_DIR" || {
    status=$?
    rm -rf "$work"
    return "$status"
  }
  ovpn_upgrade_activate "$OVPN_UPGRADE_SELECTED_VERSION" "$OVPN_UPGRADE_SELECTED_DIR" || {
    status=$?
    rm -rf "$work"
    return "$status"
  }
  if ! ovpn_upgrade_reload_management_broker; then
    ovpn_log 'management broker failed to reload; rolling back management code'
    if declare -F ovpn_event_write >/dev/null 2>&1; then
      ovpn_event_write management_upgrade apply failed "" "" \
        "$(jq -cn --arg from "$OVPN_UPGRADE_CURRENT_VERSION" \
          --arg to "$OVPN_UPGRADE_SELECTED_VERSION" \
          '{from_version:$from,to_version:$to,reason:"management_broker_reload_failed"}')" || true
    fi
    if ! ovpn_upgrade_rollback true >/dev/null; then
      ovpn_log 'automatic management-code rollback failed'
    fi
    rm -rf "$work"
    return 74
  fi
  if declare -F ovpn_event_write >/dev/null 2>&1; then
    ovpn_event_write management_upgrade apply applied "" "" \
      "$(jq -cn --arg from "$OVPN_UPGRADE_CURRENT_VERSION" \
        --arg to "$OVPN_UPGRADE_SELECTED_VERSION" \
        '{from_version:$from,to_version:$to}')" || true
  fi
  ovpn_upgrade_print_result "$json" applied
  rm -rf "$work"
}

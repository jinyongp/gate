#!/usr/bin/env bash
set -euo pipefail

required="${GATE_REQUIRE_LINUX_LOW_PORT_TEST:-0}"
if [[ "$required" != "0" && "$required" != "1" ]]; then
  echo "error: GATE_REQUIRE_LINUX_LOW_PORT_TEST must be 0 or 1" >&2
  exit 1
fi

skip_or_fail() {
  local reason="$1"
  if [[ "$required" == "1" ]]; then
    echo "error: Linux low-port integration requirement unavailable: ${reason}" >&2
    exit 1
  fi
  echo "skip: Linux low-port integration: ${reason}"
  exit 0
}

if [[ "$(uname -s)" != "Linux" ]]; then
  skip_or_fail "Linux host required"
fi
if [[ "$(id -u)" == "0" ]]; then
  skip_or_fail "non-root test user required"
fi
if [[ ! -r /proc/self/status ]]; then
  skip_or_fail "/proc capability status unavailable"
fi
unset GATE_ISOLATED_ROOT

if ! command -v go >/dev/null 2>&1; then
  skip_or_fail "go is not installed"
fi

find_capability_tool() {
  local name="$1"
  local candidate
  for candidate in "/usr/sbin/${name}" "/sbin/${name}" "/usr/bin/${name}" "/bin/${name}"; do
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

getcap_bin="$(find_capability_tool getcap || true)"
setcap_bin="$(find_capability_tool setcap || true)"
sudo_bin="$(find_capability_tool sudo || true)"
mktemp_bin="$(find_capability_tool mktemp || true)"
install_bin="$(find_capability_tool install || true)"
if [[ -z "$getcap_bin" || -z "$setcap_bin" || -z "$sudo_bin" ||
  -z "$mktemp_bin" || -z "$install_bin" ]]; then
  skip_or_fail "trusted getcap, setcap, sudo, mktemp, or install tool unavailable"
fi
if ! "$sudo_bin" -n true >/dev/null 2>&1; then
  skip_or_fail "passwordless non-interactive sudo unavailable"
fi

fixture_root="$(mktemp -d)"
gate_bin="${fixture_root}/gate"
shell_gate_bin="${fixture_root}/shell-bin/gate"
config_home="${fixture_root}/config"
data_home="${fixture_root}/data"
state_home="${fixture_root}/state"
cache_home="${fixture_root}/cache"
test_home="${fixture_root}/home"
project_dir="${fixture_root}/project"
daemon_started=0

cleanup() {
  if [[ "$daemon_started" == "1" && -x "$gate_bin" ]]; then
    XDG_CONFIG_HOME="$config_home" XDG_STATE_HOME="$state_home" \
      "$gate_bin" daemon stop >/dev/null 2>&1 || true
  fi
  rm -rf "$fixture_root"
}
trap cleanup EXIT

mkdir -p \
  "$config_home" \
  "$data_home" \
  "$state_home" \
  "$cache_home" \
  "$test_home" \
  "$(dirname "$shell_gate_bin")" \
  "$project_dir"
cat >"${project_dir}/gate.toml" <<'EOF'
[project]
name = "capability-fixture"
base = "capability-fixture.localhost"

[services.probe]
port = 49191
EOF
go build -trimpath -o "$gate_bin" ./cmd/gate
cp "$gate_bin" "$shell_gate_bin"

if ! setup_output="$("$gate_bin" daemon setup --yes 2>&1)"; then
  skip_or_fail "file capability setup failed: ${setup_output}"
fi
capability_output="$("$getcap_bin" "$gate_bin")"
if [[ "${capability_output##* }" != "cap_net_bind_service=ep" ]]; then
  echo "error: unexpected gate capability: ${capability_output:-none}" >&2
  exit 1
fi

if ! start_output="$(
  XDG_CONFIG_HOME="$config_home" XDG_STATE_HOME="$state_home" \
    "$gate_bin" up -d --config "${project_dir}/gate.toml" 2>&1
)"; then
  if [[ "$start_output" == *"address already in use"* ]]; then
    skip_or_fail "TCP port 80 or 443 is already in use"
  fi
  echo "error: configured gate could not bind default ports" >&2
  echo "$start_output" >&2
  exit 1
fi
daemon_started=1

status_json="$(
  XDG_CONFIG_HOME="$config_home" XDG_STATE_HOME="$state_home" \
    "$gate_bin" daemon status --json
)"
if [[ "$status_json" != *'"running": true'* ]]; then
  echo "error: daemon status does not report running" >&2
  echo "$status_json" >&2
  exit 1
fi
for field_port in "https_addr:443" "http_addr:80"; do
  field="${field_port%%:*}"
  port="${field_port##*:}"
  if ! printf '%s\n' "$status_json" | grep -Eq "\"${field}\":[[:space:]]*\"[^\"]*:${port}\""; then
    echo "error: daemon status does not report ${field} on TCP :${port}" >&2
    echo "$status_json" >&2
    exit 1
  fi
done

daemon_pid="$(
  printf '%s\n' "$status_json" |
    sed -n 's/^[[:space:]]*"pid":[[:space:]]*\([0-9][0-9]*\),*$/\1/p'
)"
if [[ -z "$daemon_pid" || ! -r "/proc/${daemon_pid}/status" ]]; then
  echo "error: cannot inspect gate listener process capability" >&2
  exit 1
fi
listener_cap="$(awk '$1 == "CapEff:" { print $2; exit }' "/proc/${daemon_pid}/status")"
if (( (16#$listener_cap & 0x400) == 0 )); then
  echo "error: gate listener lacks effective CAP_NET_BIND_SERVICE: ${listener_cap}" >&2
  exit 1
fi

child_cap="$(
  XDG_CONFIG_HOME="$config_home" XDG_STATE_HOME="$state_home" \
    "$gate_bin" run --up --quiet --config "${project_dir}/gate.toml" probe -- \
    /bin/sh -c 'while IFS="	 " read -r key value rest; do
      if [ "$key" = "CapEff:" ]; then
        printf "%s" "$value"
        exit 0
      fi
    done </proc/self/status
    exit 1'
)"
if [[ -z "$child_cap" ]]; then
  echo "error: gate run child did not report CapEff" >&2
  exit 1
fi
if (( (16#$child_cap & 0x400) != 0 )); then
  echo "error: gate run child inherited CAP_NET_BIND_SERVICE: ${child_cap}" >&2
  exit 1
fi

printf 'ok: gate listener bound :443/:80 with CapEff=%s\n' "$listener_cap"
printf 'ok: gate run child omitted CAP_NET_BIND_SERVICE with CapEff=%s\n' "$child_cap"

XDG_CONFIG_HOME="$config_home" XDG_STATE_HOME="$state_home" \
  "$gate_bin" daemon stop >/dev/null
daemon_started=0

helper_pattern=".gate-capability-helper-$(id -u)-XXXXXXXX"
make_root_helper_residue() {
  local source_bin="$1"
  local residue
  residue="$("$sudo_bin" -n -- "$mktemp_bin" "/tmp/${helper_pattern}")"
  "$sudo_bin" -n -- "$install_bin" -m 0755 -- "$source_bin" "$residue"
  printf '%s\n' "$residue"
}

assert_no_root_helper_residue() {
  local found
  found="$(
    find /tmp /var/tmp -maxdepth 1 -type f -user 0 \
      -name ".gate-capability-helper-$(id -u)-*" -print -quit 2>/dev/null || true
  )"
  if [[ -n "$found" ]]; then
    echo "error: root-owned low-port helper residue remains: $found" >&2
    exit 1
  fi
}

"$sudo_bin" -n -- "$setcap_bin" -r "$gate_bin"
setup_residue="$(make_root_helper_residue "$gate_bin")"
if [[ ! -f "$setup_residue" ]]; then
  echo "error: failed to create setup recovery residue" >&2
  exit 1
fi
HOME="$test_home" \
  XDG_CACHE_HOME="$cache_home" \
  "$gate_bin" daemon setup --yes >/dev/null
assert_no_root_helper_residue

builtin_residue="$(make_root_helper_residue "$gate_bin")"
HOME="$test_home" \
  XDG_CONFIG_HOME="$config_home" \
  XDG_DATA_HOME="$data_home" \
  XDG_STATE_HOME="$state_home" \
  XDG_CACHE_HOME="$cache_home" \
  GATE_BIN_DIR="$fixture_root" \
  "$gate_bin" uninstall --yes --keep-trust >/dev/null
if [[ -e "$gate_bin" || -e "$builtin_residue" ]]; then
  echo "error: built-in uninstall left gate or helper residue" >&2
  exit 1
fi

shell_residue="$(make_root_helper_residue "$shell_gate_bin")"
HOME="$test_home" \
  XDG_CONFIG_HOME="${fixture_root}/shell-config" \
  XDG_DATA_HOME="${fixture_root}/shell-data" \
  XDG_STATE_HOME="${fixture_root}/shell-state" \
  XDG_CACHE_HOME="${fixture_root}/shell-cache" \
  GATE_BIN_DIR="$(dirname "$shell_gate_bin")" \
  sh scripts/uninstall.sh --yes --keep-trust >/dev/null
if [[ -e "$shell_gate_bin" || -e "$shell_residue" ]]; then
  echo "error: standalone uninstall left gate or helper residue" >&2
  exit 1
fi
assert_no_root_helper_residue

printf 'ok: setup recovery and both uninstall paths removed root helper residue\n'

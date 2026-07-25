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
if [[ -z "$getcap_bin" || -z "$setcap_bin" || -z "$sudo_bin" ]]; then
  skip_or_fail "trusted getcap, setcap, or sudo tool unavailable"
fi
if ! "$sudo_bin" -n true >/dev/null 2>&1; then
  skip_or_fail "passwordless non-interactive sudo unavailable"
fi

fixture_root="$(mktemp -d)"
gate_bin="${fixture_root}/gate"
config_home="${fixture_root}/config"
state_home="${fixture_root}/state"
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

mkdir -p "$config_home" "$state_home" "$project_dir"
cat >"${project_dir}/gate.toml" <<'EOF'
[project]
name = "capability-fixture"
base = "capability-fixture.localhost"

[services.probe]
port = 49191
EOF
go build -trimpath -o "$gate_bin" ./cmd/gate

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

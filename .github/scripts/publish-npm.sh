#!/usr/bin/env bash
set -euo pipefail

version_tag="${1:?Usage: publish-npm.sh vX.Y.Z [artifact-dir]}"
artifact_dir="${2:-.}"
summary_file="${NPM_PUBLISH_SUMMARY:-npm-publish-summary.tsv}"
npmjs_registry_config="--@jinyongp:registry=https://registry.npmjs.org"
publish_root="$(mktemp -d)"

cleanup() {
  rm -rf "$publish_root"
}
trap cleanup EXIT

if [[ ! "$version_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "npm version tag must be vX.Y.Z: ${version_tag}" >&2
  exit 1
fi

pnpm node:build

mkdir -p "$publish_root/packages/binaries" "$publish_root/scripts/node"
cp -R packages/node "$publish_root/packages/node"
cp -R packages/binaries/darwin-arm64 "$publish_root/packages/binaries/darwin-arm64"
cp -R packages/binaries/darwin-x64 "$publish_root/packages/binaries/darwin-x64"
cp -R packages/binaries/linux-arm64 "$publish_root/packages/binaries/linux-arm64"
cp -R packages/binaries/linux-x64 "$publish_root/packages/binaries/linux-x64"
cp scripts/node/verify-package-binary.mjs "$publish_root/scripts/node/verify-package-binary.mjs"

node scripts/node/prepare-publish-packages.mjs "$version_tag" "$publish_root"
node scripts/node/stage-binary-packages.mjs "$artifact_dir" "$publish_root"

: > "$summary_file"

package_dirs=(
  "$publish_root/packages/binaries/darwin-arm64"
  "$publish_root/packages/binaries/darwin-x64"
  "$publish_root/packages/binaries/linux-arm64"
  "$publish_root/packages/binaries/linux-x64"
  "$publish_root/packages/node"
)
package_specs=()
package_tarballs=()
package_actions=()

preflight_package() {
  local package_dir="$1"
  local spec
  local pack_dir
  local pack_json
  local filename
  local local_integrity
  local remote_integrity_json
  local remote_integrity
  local view_error
  local tarball
	local package_name

  spec="$(node -e 'const fs = require("fs"); const path = require("path"); const manifest = JSON.parse(fs.readFileSync(path.join(process.argv[1], "package.json"), "utf8")); console.log(`${manifest.name}@${manifest.version}`);' "$package_dir")"
  pack_dir="$(mktemp -d "$publish_root/pack.XXXXXX")"
  pack_json="$(npm pack "$package_dir" --json --pack-destination "$pack_dir")"
  filename="$(node -e 'const value = JSON.parse(process.argv[1]); process.stdout.write(value[0].filename);' "$pack_json")"
  local_integrity="$(node -e 'const value = JSON.parse(process.argv[1]); process.stdout.write(value[0].integrity);' "$pack_json")"
	package_name="${spec%@*}"
	node -e '
		const pack = JSON.parse(process.argv[1])[0];
		const name = process.argv[2];
		const common = ["package.json", "LICENSE"];
		const expected = name === "@jinyongp/gate"
			? [...common, "README.md", "bin/gate.mjs", "dist/index.d.mts", "dist/index.mjs"]
			: [...common, "bin/gate"];
		const actual = pack.files.map((file) => file.path).sort();
		const allowed = new Set(expected);
		if (actual.some((file) => !allowed.has(file)) || expected.some((file) => !actual.includes(file))) {
			throw new Error(`unexpected packed files for ${name}: ${actual.join(", ")}`);
		}
	' "$pack_json" "$package_name"
  tarball="$pack_dir/$filename"
  view_error="$pack_dir/npm-view.err"
  if remote_integrity_json="$(npm view "$spec" dist.integrity --json "$npmjs_registry_config" 2>"$view_error")"; then
    remote_integrity="$(node -e 'const value = JSON.parse(process.argv[1]); process.stdout.write(typeof value === "string" ? value : "");' "$remote_integrity_json")"
    if [ -z "$remote_integrity" ] || [ "$remote_integrity" != "$local_integrity" ]; then
      echo "npm package exists with different integrity; refusing immutable version mismatch: ${spec}" >&2
      printf "%s\t%s\n" "$spec" "integrity mismatch" >> "$summary_file"
      return 1
    fi
    package_specs+=("$spec")
    package_tarballs+=("$tarball")
    package_actions+=("skip")
    return
  fi
  if ! grep -Eiq 'E404|not in this registry|is not in this registry|not found' "$view_error"; then
    cat "$view_error" >&2
    printf "%s\t%s\n" "$spec" "lookup failed" >> "$summary_file"
    return 1
  fi
  package_specs+=("$spec")
  package_tarballs+=("$tarball")
  package_actions+=("publish")
}

for package_dir in "${package_dirs[@]}"; do
  preflight_package "$package_dir"
done

smoke_dir="$(mktemp -d "$publish_root/smoke.XXXXXX")"
printf '{"private":true}\n' > "$smoke_dir/package.json"
case "$(uname -s)-$(uname -m)" in
	Linux-x86_64|Linux-amd64) smoke_platform="@jinyongp/gate-linux-x64@" ;;
	Linux-aarch64|Linux-arm64) smoke_platform="@jinyongp/gate-linux-arm64@" ;;
	Darwin-x86_64|Darwin-amd64) smoke_platform="@jinyongp/gate-darwin-x64@" ;;
	Darwin-arm64|Darwin-aarch64) smoke_platform="@jinyongp/gate-darwin-arm64@" ;;
	*) echo "unsupported npm package smoke platform" >&2; exit 1 ;;
esac
smoke_tarballs=()
for index in "${!package_specs[@]}"; do
	if [[ "${package_specs[$index]}" == "@jinyongp/gate@"* || "${package_specs[$index]}" == "${smoke_platform}"* ]]; then
		smoke_tarballs+=("${package_tarballs[$index]}")
	fi
done
if [ "${#smoke_tarballs[@]}" -ne 2 ]; then
	echo "failed to select main and current-platform npm tarballs for smoke test" >&2
	exit 1
fi
npm install --ignore-scripts --no-audit --no-fund --prefix "$smoke_dir" "${smoke_tarballs[@]}"
"$smoke_dir/node_modules/.bin/gate" --version >/dev/null

for index in "${!package_specs[@]}"; do
  spec="${package_specs[$index]}"
  if [ "${package_actions[$index]}" = "skip" ]; then
    echo "npm package already exists with matching integrity; skipping ${spec}"
    printf "%s\t%s\n" "$spec" "already exists (verified)" >> "$summary_file"
    continue
  fi
  if npm publish "${package_tarballs[$index]}" --access public "$npmjs_registry_config"; then
    printf "%s\t%s\n" "$spec" "published" >> "$summary_file"
  else
    printf "%s\t%s\n" "$spec" "failed" >> "$summary_file"
    exit 1
  fi
done

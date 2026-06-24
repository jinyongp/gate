import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { chmod, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("../..", import.meta.url)));
const publishRoot = await mkdtemp(join(tmpdir(), "gate-publish-packages-check."));
const execFileAsync = promisify(execFile);

const binaryPackages = [
  "darwin-arm64",
  "darwin-x64",
  "linux-arm64",
  "linux-x64"
];

const artifacts = [
  "gate-darwin-arm64",
  "gate-darwin-amd64",
  "gate-linux-arm64",
  "gate-linux-amd64"
];

async function readManifest(path) {
  return JSON.parse(await readFile(path, "utf8"));
}

try {
  const sourceNodeManifestBefore = await readFile(
    join(repoRoot, "packages", "node", "package.json"),
    "utf8"
  );

  await cp(join(repoRoot, "packages", "node"), join(publishRoot, "packages", "node"), {
    recursive: true
  });
  await mkdir(join(publishRoot, "scripts", "node"), { recursive: true });
  await cp(
    join(repoRoot, "scripts", "node", "verify-package-binary.mjs"),
    join(publishRoot, "scripts", "node", "verify-package-binary.mjs")
  );

  for (const name of binaryPackages) {
    await cp(
      join(repoRoot, "packages", "binaries", name),
      join(publishRoot, "packages", "binaries", name),
      { recursive: true }
    );
  }

  const artifactDir = join(publishRoot, "bin");
  await mkdir(artifactDir, { recursive: true });
  for (const artifact of artifacts) {
    const path = join(artifactDir, artifact);
    await writeFile(path, "#!/usr/bin/env sh\necho gate\n", { mode: 0o755 });
    await chmod(path, 0o755);
  }

  await execFileAsync(process.execPath, [
    join(repoRoot, "scripts", "node", "prepare-publish-packages.mjs"),
    "v9.8.7",
    publishRoot
  ]);
  await execFileAsync(process.execPath, [
    join(repoRoot, "scripts", "node", "stage-binary-packages.mjs"),
    artifactDir,
    publishRoot
  ]);

  const sourceNodeManifestAfter = await readFile(
    join(repoRoot, "packages", "node", "package.json"),
    "utf8"
  );
  assert.equal(sourceNodeManifestAfter, sourceNodeManifestBefore);

  const publishNodeManifest = await readManifest(
    join(publishRoot, "packages", "node", "package.json")
  );
  assert.equal(publishNodeManifest.version, "9.8.7");
  assert.deepEqual(publishNodeManifest.optionalDependencies, {
    "@jinyongp/gate-darwin-arm64": "9.8.7",
    "@jinyongp/gate-darwin-x64": "9.8.7",
    "@jinyongp/gate-linux-arm64": "9.8.7",
    "@jinyongp/gate-linux-x64": "9.8.7"
  });

  for (const name of binaryPackages) {
    const manifest = await readManifest(
      join(publishRoot, "packages", "binaries", name, "package.json")
    );
    assert.equal(manifest.version, "9.8.7");
    await readFile(join(publishRoot, "packages", "binaries", name, "bin", "gate"));
    await execFileAsync("npm", ["pack", "--dry-run"], {
      cwd: join(publishRoot, "packages", "binaries", name),
      env: {
        ...process.env,
        npm_config_cache: join(publishRoot, ".npm-cache")
      }
    });
  }
} catch (error) {
  await rm(publishRoot, { recursive: true, force: true });
  throw error;
}

await rm(publishRoot, { recursive: true, force: true });

import { readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const version = process.argv[2];

if (!version || !/^v?\d+\.\d+\.\d+$/.test(version)) {
  throw new Error("usage: node scripts/node/prepare-publish-packages.mjs vX.Y.Z");
}

const packageVersion = version.startsWith("v") ? version.slice(1) : version;
const repoRoot = resolve(fileURLToPath(new URL("../..", import.meta.url)));

const binaryPackages = [
  "@gate/binary-darwin-arm64",
  "@gate/binary-darwin-x64",
  "@gate/binary-linux-arm64",
  "@gate/binary-linux-x64"
];

const packagePaths = [
  "packages/binaries/darwin-arm64/package.json",
  "packages/binaries/darwin-x64/package.json",
  "packages/binaries/linux-arm64/package.json",
  "packages/binaries/linux-x64/package.json",
  "packages/node/package.json"
];

for (const relativePath of packagePaths) {
  const path = join(repoRoot, relativePath);
  const manifest = JSON.parse(await readFile(path, "utf8"));
  manifest.version = packageVersion;
  manifest.publishConfig = { access: "public" };

  if (manifest.name === "@gate/node") {
    manifest.optionalDependencies = Object.fromEntries(
      binaryPackages.map((name) => [name, packageVersion])
    );
  }

  await writeFile(path, `${JSON.stringify(manifest, null, 2)}\n`);
  console.log(`${manifest.name}@${packageVersion}`);
}

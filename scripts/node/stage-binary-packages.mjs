import { chmod, cp, mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("../..", import.meta.url)));
const sourceDir = resolve(process.argv[2] ?? join(repoRoot, "bin"));

const targets = [
  { packageDir: "darwin-arm64", artifact: "gate-darwin-arm64" },
  { packageDir: "darwin-x64", artifact: "gate-darwin-amd64" },
  { packageDir: "linux-arm64", artifact: "gate-linux-arm64" },
  { packageDir: "linux-x64", artifact: "gate-linux-amd64" }
];

for (const target of targets) {
  const source = join(sourceDir, target.artifact);
  const destinationDir = join(repoRoot, "packages", "binaries", target.packageDir, "bin");
  const destination = join(destinationDir, "gate");
  await mkdir(destinationDir, { recursive: true });
  await cp(source, destination);
  await chmod(destination, 0o755);
  console.log(`${source} -> ${destination}`);
}

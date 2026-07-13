import { copyFile, readFile, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const version = process.argv[2]

if (!version || !/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) {
  throw new Error('usage: node scripts/node/prepare-publish-packages.mjs vX.Y.Z')
}

const packageVersion = version.startsWith('v') ? version.slice(1) : version
const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)))
const packageRoot = resolve(process.argv[3] ?? repoRoot)

const binaryPackages = [
  '@jinyongp/gate-darwin-arm64',
  '@jinyongp/gate-darwin-x64',
  '@jinyongp/gate-linux-arm64',
  '@jinyongp/gate-linux-x64',
]

const packagePaths = [
  'packages/binaries/darwin-arm64/package.json',
  'packages/binaries/darwin-x64/package.json',
  'packages/binaries/linux-arm64/package.json',
  'packages/binaries/linux-x64/package.json',
  'packages/node/package.json',
]

for (const relativePath of packagePaths) {
  const path = join(packageRoot, relativePath)
  const manifest = JSON.parse(await readFile(path, 'utf8'))
  manifest.version = packageVersion
  manifest.publishConfig = { access: 'public' }

  if (manifest.name === '@jinyongp/gate') {
    manifest.optionalDependencies = Object.fromEntries(
      binaryPackages.map((name) => [name, packageVersion]),
    )
  }

  await writeFile(path, `${JSON.stringify(manifest, null, 2)}\n`)
  await copyFile(join(repoRoot, 'LICENSE'), join(packageRoot, dirname(relativePath), 'LICENSE'))
  console.log(`${manifest.name}@${packageVersion}`)
}

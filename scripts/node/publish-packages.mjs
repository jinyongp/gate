import { spawn } from 'node:child_process'
import { appendFile, cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const strictVersionPattern = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/
const npmNotFoundPattern = /E404|not in this registry|is not in this registry|not found/i
const npmRegistryConfig = '--@jinyongp:registry=https://registry.npmjs.org'

export const packageRelativeDirectories = [
  'packages/binaries/darwin-arm64',
  'packages/binaries/darwin-x64',
  'packages/binaries/linux-arm64',
  'packages/binaries/linux-x64',
  'packages/node',
]

export class CommandFailure extends Error {
  constructor(command, code, stdout, stderr) {
    super(`${command} exited with code ${code}`)
    this.name = 'CommandFailure'
    this.code = code
    this.stdout = stdout
    this.stderr = stderr
    this.reported = false
  }
}

export function validateVersionTag(versionTag) {
  if (!strictVersionPattern.test(versionTag)) {
    throw new Error(`npm version tag must be vX.Y.Z: ${versionTag}`)
  }
}

export function expectedPackedFiles(packageName) {
  const common = ['LICENSE', 'package.json']
  if (packageName === '@jinyongp/gate') {
    return [...common, 'README.md', 'bin/gate.mjs', 'dist/index.d.mts', 'dist/index.mjs'].toSorted()
  }
  return [...common, 'bin/gate'].toSorted()
}

export function validatePackedFiles(packageName, files) {
  const actual = files.map((file) => file.path).toSorted()
  const expected = expectedPackedFiles(packageName)
  if (actual.length !== expected.length || actual.some((file, index) => file !== expected[index])) {
    throw new Error(`unexpected packed files for ${packageName}: ${actual.join(', ')}`)
  }
}

export function currentPlatformPackage(platform, architecture) {
  const key = `${platform}-${architecture}`
  const packages = new Map([
    ['linux-x64', '@jinyongp/gate-linux-x64@'],
    ['linux-arm64', '@jinyongp/gate-linux-arm64@'],
    ['darwin-x64', '@jinyongp/gate-darwin-x64@'],
    ['darwin-arm64', '@jinyongp/gate-darwin-arm64@'],
  ])
  const selected = packages.get(key)
  if (!selected) {
    throw new Error(`unsupported npm package smoke platform: ${key}`)
  }
  return selected
}

export function selectSmokeTarballs(packages, platform, architecture) {
  const platformPrefix = currentPlatformPackage(platform, architecture)
  const selected = packages
    .filter(
      (entry) => entry.spec.startsWith('@jinyongp/gate@') || entry.spec.startsWith(platformPrefix),
    )
    .map((entry) => entry.tarball)
  if (selected.length !== 2) {
    throw new Error('failed to select main and current-platform npm tarballs for smoke test')
  }
  return selected
}

export async function runCommand(name, args, options = {}) {
  const captureStdout = options.captureStdout ?? false
  const captureStderr = options.captureStderr ?? false
  return new Promise((resolve, reject) => {
    const child = spawn(name, args, {
      cwd: options.cwd,
      env: options.env,
      stdio: ['inherit', captureStdout ? 'pipe' : 'inherit', captureStderr ? 'pipe' : 'inherit'],
    })
    let stdout = ''
    let stderr = ''
    child.stdout?.setEncoding('utf8')
    child.stderr?.setEncoding('utf8')
    child.stdout?.on('data', (chunk) => {
      stdout += chunk
    })
    child.stderr?.on('data', (chunk) => {
      stderr += chunk
    })
    child.once('error', reject)
    child.once('close', (code, signal) => {
      if (code === 0) {
        resolve({ stdout, stderr })
        return
      }
      const status = signal ? `signal ${signal}` : `code ${code ?? 'unknown'}`
      reject(new CommandFailure(`${name} ${args.join(' ')}`, status, stdout, stderr))
    })
  })
}

export async function publishPackages(options) {
  const {
    versionTag,
    artifactDir = '.',
    summaryFile = 'npm-publish-summary.tsv',
    root = process.cwd(),
    executor = runCommand,
    platform = process.platform,
    architecture = process.arch,
    temporaryDirectory = tmpdir(),
    stdout = process.stdout,
    stderr = process.stderr,
  } = options
  validateVersionTag(versionTag)

  const publishRoot = await mkdtemp(path.join(temporaryDirectory, 'gate-publish-'))
  try {
    await executor('pnpm', ['node:build'], { cwd: root })
    await preparePublishTree(root, publishRoot)
    await executor(
      'node',
      [path.join(root, 'scripts/node/prepare-publish-packages.mjs'), versionTag, publishRoot],
      { cwd: root },
    )
    await executor(
      'node',
      [path.join(root, 'scripts/node/stage-binary-packages.mjs'), artifactDir, publishRoot],
      { cwd: root },
    )
    await writeFile(summaryFile, '', { mode: 0o600 })

    const packages = []
    for (const relativeDirectory of packageRelativeDirectories) {
      const packageDirectory = path.join(publishRoot, relativeDirectory)
      const prepared = await preflightPackage({
        packageDirectory,
        publishRoot,
        executor,
        summaryFile,
        stderr,
      })
      packages.push(prepared)
    }

    const smokeDirectory = await mkdtemp(path.join(publishRoot, 'smoke-'))
    await writeFile(path.join(smokeDirectory, 'package.json'), '{"private":true}\n', {
      mode: 0o600,
    })
    const smokeTarballs = selectSmokeTarballs(packages, platform, architecture)
    await executor(
      'npm',
      [
        'install',
        '--ignore-scripts',
        '--no-audit',
        '--no-fund',
        '--prefix',
        smokeDirectory,
        ...smokeTarballs,
      ],
      { cwd: root },
    )
    await executor(path.join(smokeDirectory, 'node_modules/.bin/gate'), ['--version'], {
      cwd: root,
    })

    for (const packageInfo of packages) {
      if (packageInfo.action === 'skip') {
        stdout.write(
          `npm package already exists with matching integrity; skipping ${packageInfo.spec}\n`,
        )
        await appendSummary(summaryFile, packageInfo.spec, 'already exists (verified)')
        continue
      }
      try {
        await executor(
          'npm',
          ['publish', packageInfo.tarball, '--access', 'public', npmRegistryConfig],
          { cwd: root },
        )
        await appendSummary(summaryFile, packageInfo.spec, 'published')
      } catch (error) {
        await appendSummary(summaryFile, packageInfo.spec, 'failed')
        throw error
      }
    }
  } finally {
    await rm(publishRoot, { recursive: true, force: true })
  }
}

async function preparePublishTree(root, publishRoot) {
  await mkdir(path.join(publishRoot, 'packages/binaries'), { recursive: true, mode: 0o750 })
  await mkdir(path.join(publishRoot, 'scripts/node'), { recursive: true, mode: 0o750 })
  for (const relativeDirectory of packageRelativeDirectories) {
    await cp(path.join(root, relativeDirectory), path.join(publishRoot, relativeDirectory), {
      recursive: true,
    })
  }
  await cp(
    path.join(root, 'scripts/node/verify-package-binary.mjs'),
    path.join(publishRoot, 'scripts/node/verify-package-binary.mjs'),
  )
}

async function preflightPackage({ packageDirectory, publishRoot, executor, summaryFile, stderr }) {
  const manifest = JSON.parse(await readFile(path.join(packageDirectory, 'package.json'), 'utf8'))
  const spec = `${manifest.name}@${manifest.version}`
  const packDirectory = await mkdtemp(path.join(publishRoot, 'pack-'))
  const packResult = await executor(
    'npm',
    ['pack', packageDirectory, '--json', '--pack-destination', packDirectory],
    { captureStdout: true },
  )
  const packed = JSON.parse(packResult.stdout)[0]
  if (
    !packed ||
    typeof packed.filename !== 'string' ||
    typeof packed.integrity !== 'string' ||
    !Array.isArray(packed.files)
  ) {
    throw new Error(`invalid npm pack response for ${spec}`)
  }
  validatePackedFiles(manifest.name, packed.files)
  const tarball = path.join(packDirectory, packed.filename)

  try {
    const remote = await executor(
      'npm',
      ['view', spec, 'dist.integrity', '--json', npmRegistryConfig],
      { captureStdout: true, captureStderr: true },
    )
    const remoteIntegrity = JSON.parse(remote.stdout)
    if (typeof remoteIntegrity !== 'string' || remoteIntegrity !== packed.integrity) {
      await appendSummary(summaryFile, spec, 'integrity mismatch')
      throw new Error(
        `npm package exists with different integrity; refusing immutable version mismatch: ${spec}`,
      )
    }
    return { spec, tarball, action: 'skip' }
  } catch (error) {
    if (!(error instanceof CommandFailure)) {
      throw error
    }
    if (!npmNotFoundPattern.test(error.stderr)) {
      stderr.write(error.stderr)
      error.reported = true
      await appendSummary(summaryFile, spec, 'lookup failed')
      throw error
    }
    return { spec, tarball, action: 'publish' }
  }
}

async function appendSummary(summaryFile, spec, status) {
  await appendFile(summaryFile, `${spec}\t${status}\n`)
}

async function main() {
  const versionTag = process.argv[2] ?? ''
  const artifactDir = process.argv[3] ?? '.'
  await publishPackages({
    versionTag,
    artifactDir,
    summaryFile: process.env.NPM_PUBLISH_SUMMARY ?? 'npm-publish-summary.tsv',
  })
}

const invokedPath = process.argv[1] ? pathToFileURL(path.resolve(process.argv[1])).href : ''
if (import.meta.url === invokedPath) {
  main().catch((error) => {
    if (error instanceof CommandFailure) {
      if (!error.reported && error.stderr) {
        process.stderr.write(error.stderr)
      }
    } else {
      process.stderr.write(`${error.message}\n`)
    }
    process.exitCode = 1
  })
}

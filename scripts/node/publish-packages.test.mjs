import assert from 'node:assert/strict'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  CommandFailure,
  currentPlatformPackage,
  expectedPackedFiles,
  packageRelativeDirectories,
  publishPackages,
  selectSmokeTarballs,
  validatePackageManifest,
  validatePackedFiles,
} from './publish-packages.mjs'

const temporaryRoots = []
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

afterEach(async () => {
  const { rm } = await import('node:fs/promises')
  await Promise.all(temporaryRoots.splice(0).map((root) => rm(root, { recursive: true })))
})

async function temporaryDirectory(prefix) {
  const { mkdtemp } = await import('node:fs/promises')
  const directory = await mkdtemp(path.join(os.tmpdir(), prefix))
  temporaryRoots.push(directory)
  return directory
}

async function createRepositoryFixture() {
  const root = await temporaryDirectory('gate-publish-repo-')
  for (const relativeDirectory of packageRelativeDirectories) {
    const directory = path.join(root, relativeDirectory)
    const isMain = relativeDirectory === 'packages/node'
    const suffix = relativeDirectory.split('/').at(-1)
    const name = isMain ? '@jinyongp/gate' : `@jinyongp/gate-${suffix}`
    const [platform, architecture] = suffix.split('-')
    const manifest = { name, version: '1.2.3' }
    if (!isMain) {
      manifest.os = [platform]
      manifest.cpu = [architecture]
    }
    await mkdir(path.join(directory, 'bin'), { recursive: true })
    await writeFile(path.join(directory, 'LICENSE'), 'license\n')
    if (isMain) {
      manifest.optionalDependencies = Object.fromEntries(
        [
          '@jinyongp/gate-darwin-arm64',
          '@jinyongp/gate-darwin-x64',
          '@jinyongp/gate-linux-arm64',
          '@jinyongp/gate-linux-x64',
        ].map((packageName) => [packageName, '1.2.3']),
      )
      await mkdir(path.join(directory, 'dist'), { recursive: true })
      await writeFile(path.join(directory, 'README.md'), 'readme\n')
      await writeFile(path.join(directory, 'bin/gate.mjs'), 'main\n')
      await writeFile(path.join(directory, 'dist/index.d.mts'), 'types\n')
      await writeFile(path.join(directory, 'dist/index.mjs'), 'module\n')
    } else {
      await writeFile(path.join(directory, 'bin/gate'), 'binary\n')
    }
    await writeFile(path.join(directory, 'package.json'), `${JSON.stringify(manifest)}\n`)
  }
  await mkdir(path.join(root, 'scripts/node'), { recursive: true })
  await writeFile(path.join(root, 'scripts/node/verify-package-binary.mjs'), 'fixture\n')
  return root
}

function packageNameForDirectory(directory) {
  if (directory.endsWith('/packages/node')) {
    return '@jinyongp/gate'
  }
  return `@jinyongp/gate-${path.basename(directory)}`
}

function createFakeExecutor(options = {}) {
  const calls = []
  const remote = options.remote ?? 'missing'
  const publishFailure = options.publishFailure ?? ''
  const executor = async (name, args) => {
    calls.push({ name, args: [...args] })
    if (name === 'npm' && args[0] === 'pack') {
      const packageDirectory = args[1]
      const packageName = packageNameForDirectory(packageDirectory)
      const filename = `${packageName.replaceAll(/[^a-z0-9]+/gi, '-')}.tgz`
      const destination = args.at(-1)
      await writeFile(path.join(destination, filename), packageName)
      return {
        stdout: JSON.stringify([
          {
            filename,
            integrity: `sha512-${packageName}`,
            files: expectedPackedFiles(packageName).map((file) => ({ path: file })),
          },
        ]),
        stderr: '',
      }
    }
    if (name === 'npm' && args[0] === 'view') {
      const spec = args[1]
      const packageName = spec.slice(0, spec.lastIndexOf('@'))
      if (remote === 'matching') {
        return { stdout: JSON.stringify(`sha512-${packageName}`), stderr: '' }
      }
      if (remote === 'mismatch') {
        return { stdout: JSON.stringify('sha512-different'), stderr: '' }
      }
      if (remote === 'failure') {
        throw new CommandFailure('npm view', 1, '', 'E500 registry unavailable\n')
      }
      throw new CommandFailure('npm view', 1, '', 'npm error E404 not found\n')
    }
    if (name === 'npm' && args[0] === 'publish') {
      if (publishFailure && args[1].includes(publishFailure)) {
        throw new CommandFailure('npm publish', 1, '', 'publish failed\n')
      }
    }
    return { stdout: '', stderr: '' }
  }
  return { calls, executor }
}

function stringWriter() {
  let value = ''
  return {
    writer: {
      write(chunk) {
        value += chunk
      },
    },
    value: () => value,
  }
}

async function runFixture(options = {}) {
  const root = await createRepositoryFixture()
  const temporary = await temporaryDirectory('gate-publish-temp-')
  const summaryFile = path.join(temporary, 'summary.tsv')
  const output = stringWriter()
  const errorOutput = stringWriter()
  const fake = createFakeExecutor(options)
  await publishPackages({
    versionTag: 'v1.2.3',
    artifactDir: path.join(root, 'artifacts'),
    summaryFile,
    root,
    executor: fake.executor,
    platform: options.platform ?? 'darwin',
    architecture: options.architecture ?? 'arm64',
    temporaryDirectory: temporary,
    stdout: output.writer,
    stderr: errorOutput.writer,
  })
  return {
    ...fake,
    summary: await readFile(summaryFile, 'utf8'),
    stdout: output.value(),
    stderr: errorOutput.value(),
    temporary,
  }
}

describe('packed file contracts', () => {
  test('accepts only the exact main and binary package allowlists', () => {
    assert.deepEqual(expectedPackedFiles('@jinyongp/gate'), [
      'LICENSE',
      'README.md',
      'bin/gate.mjs',
      'dist/index.d.mts',
      'dist/index.mjs',
      'package.json',
    ])
    assert.doesNotThrow(() =>
      validatePackedFiles(
        '@jinyongp/gate-linux-x64',
        expectedPackedFiles('@jinyongp/gate-linux-x64').map((file) => ({ path: file })),
      ),
    )
    assert.throws(
      () =>
        validatePackedFiles('@jinyongp/gate', [
          ...expectedPackedFiles('@jinyongp/gate').map((file) => ({ path: file })),
          { path: 'secret.env' },
        ]),
      /unexpected packed files/,
    )
  })

  test('validates every package name and platform mapping before packing', () => {
    assert.doesNotThrow(() =>
      validatePackageManifest('packages/binaries/linux-x64', {
        name: '@jinyongp/gate-linux-x64',
        os: ['linux'],
        cpu: ['x64'],
      }),
    )
    assert.throws(
      () =>
        validatePackageManifest('packages/binaries/darwin-arm64', {
          name: '@jinyongp/gate-darwin-arm64',
          os: ['linux'],
          cpu: ['arm64'],
        }),
      /package metadata mismatch/,
    )
    assert.throws(
      () =>
        validatePackageManifest('packages/node', {
          name: '@jinyongp/gate-linux-x64',
        }),
      /package metadata mismatch/,
    )
  })
})

describe('smoke package selection', () => {
  test('selects the main package and current platform only', () => {
    assert.equal(currentPlatformPackage('linux', 'x64'), '@jinyongp/gate-linux-x64@')
    const packages = [
      { spec: '@jinyongp/gate@1.2.3', tarball: 'main.tgz' },
      { spec: '@jinyongp/gate-linux-x64@1.2.3', tarball: 'linux.tgz' },
      { spec: '@jinyongp/gate-darwin-arm64@1.2.3', tarball: 'darwin.tgz' },
    ]
    assert.deepEqual(selectSmokeTarballs(packages, 'linux', 'x64'), ['main.tgz', 'linux.tgz'])
    assert.throws(() => currentPlatformPackage('win32', 'x64'), /unsupported/)
  })
})

describe('publish orchestration', () => {
  test('skips byte-identical published packages after smoke verification', async () => {
    const result = await runFixture({ remote: 'matching' })
    assert.equal(result.calls.filter((call) => call.args[0] === 'publish').length, 0)
    assert.equal(
      result.summary
        .trim()
        .split('\n')
        .every((line) => line.endsWith('\talready exists (verified)')),
      true,
    )
    assert.match(result.stdout, /already exists with matching integrity/)
    const smoke = result.calls.find((call) => call.name === 'npm' && call.args[0] === 'install')
    assert.ok(smoke.args.some((argument) => argument.includes('gate-publish')))
    assert.equal((await readdir(result.temporary)).toSorted().join(','), 'summary.tsv')
  })

  test('publishes every package missing from npm', async () => {
    const result = await runFixture({ remote: 'missing', platform: 'linux', architecture: 'x64' })
    assert.equal(result.calls.filter((call) => call.args[0] === 'publish').length, 5)
    assert.equal(
      result.summary
        .trim()
        .split('\n')
        .every((line) => line.endsWith('\tpublished')),
      true,
    )
  })

  test('fails before smoke or publish on immutable integrity mismatch', async () => {
    const root = await createRepositoryFixture()
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const summaryFile = path.join(temporary, 'summary.tsv')
    const fake = createFakeExecutor({ remote: 'mismatch' })
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile,
        temporaryDirectory: temporary,
        executor: fake.executor,
      }),
      /immutable version mismatch/,
    )
    assert.match(await readFile(summaryFile, 'utf8'), /\tintegrity mismatch\n$/)
    assert.equal(
      fake.calls.some((call) => call.args[0] === 'install'),
      false,
    )
    assert.equal(
      fake.calls.some((call) => call.args[0] === 'publish'),
      false,
    )
  })

  test('fails before pack, smoke, or publish on non-current package metadata mismatch', async () => {
    const root = await createRepositoryFixture()
    const manifestPath = path.join(root, 'packages/binaries/linux-x64/package.json')
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
    manifest.cpu = ['arm64']
    await writeFile(manifestPath, `${JSON.stringify(manifest)}\n`)
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const fake = createFakeExecutor({ remote: 'missing' })
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile: path.join(temporary, 'summary.tsv'),
        temporaryDirectory: temporary,
        executor: fake.executor,
        platform: 'darwin',
        architecture: 'arm64',
      }),
      /package metadata mismatch/,
    )
    assert.equal(
      fake.calls.some(
        (call) =>
          call.name === 'npm' &&
          call.args[0] === 'pack' &&
          call.args[1].endsWith('/packages/binaries/linux-x64'),
      ),
      false,
    )
    assert.equal(
      fake.calls.some(
        (call) => call.name === 'npm' && (call.args[0] === 'install' || call.args[0] === 'publish'),
      ),
      false,
    )
  })

  test('fails before every pack when a prepared package version does not match the tag', async () => {
    const root = await createRepositoryFixture()
    const manifestPath = path.join(root, 'packages/binaries/linux-x64/package.json')
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
    manifest.version = '9.9.9'
    await writeFile(manifestPath, `${JSON.stringify(manifest)}\n`)
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const fake = createFakeExecutor({ remote: 'missing' })
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile: path.join(temporary, 'summary.tsv'),
        temporaryDirectory: temporary,
        executor: fake.executor,
      }),
      /package metadata mismatch/,
    )
    assert.equal(
      fake.calls.some((call) => call.name === 'npm' && call.args[0] === 'pack'),
      false,
    )
    assert.equal(
      fake.calls.some((call) => call.name === 'npm' && call.args[0] === 'publish'),
      false,
    )
  })

  test('fails before every pack when main optional dependency versions drift', async () => {
    const root = await createRepositoryFixture()
    const manifestPath = path.join(root, 'packages/node/package.json')
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
    manifest.optionalDependencies['@jinyongp/gate-linux-x64'] = '9.9.9'
    await writeFile(manifestPath, `${JSON.stringify(manifest)}\n`)
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const fake = createFakeExecutor({ remote: 'missing' })
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile: path.join(temporary, 'summary.tsv'),
        temporaryDirectory: temporary,
        executor: fake.executor,
      }),
      /optional dependency mismatch/,
    )
    assert.equal(
      fake.calls.some((call) => call.name === 'npm' && call.args[0] === 'pack'),
      false,
    )
  })

  test('distinguishes lookup failure from npm 404 and records the summary', async () => {
    const root = await createRepositoryFixture()
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const summaryFile = path.join(temporary, 'summary.tsv')
    const fake = createFakeExecutor({ remote: 'failure' })
    const errorOutput = stringWriter()
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile,
        temporaryDirectory: temporary,
        executor: fake.executor,
        stderr: errorOutput.writer,
      }),
      CommandFailure,
    )
    assert.match(await readFile(summaryFile, 'utf8'), /\tlookup failed\n$/)
    assert.match(errorOutput.value(), /E500 registry unavailable/)
    assert.equal(
      fake.calls.some((call) => call.args[0] === 'publish'),
      false,
    )
  })

  test('records a publish failure after earlier successful packages', async () => {
    const root = await createRepositoryFixture()
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const summaryFile = path.join(temporary, 'summary.tsv')
    const fake = createFakeExecutor({
      remote: 'missing',
      publishFailure: 'gate-linux-arm64',
    })
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile,
        temporaryDirectory: temporary,
        executor: fake.executor,
      }),
      CommandFailure,
    )
    const summary = await readFile(summaryFile, 'utf8')
    assert.match(summary, /gate-darwin-arm64@1\.2\.3\tpublished/)
    assert.match(summary, /gate-linux-arm64@1\.2\.3\tfailed/)
  })

  test('re-verifies the immutable release tag before every npm publish', async () => {
    const root = await createRepositoryFixture()
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const base = createFakeExecutor({ remote: 'missing' })
    const expected = '1'.repeat(40)
    const moved = '2'.repeat(40)
    let verifications = 0
    const executor = async (name, args, options) => {
      if (name === 'git' && args[0] === 'ls-remote') {
        verifications += 1
        return {
          stdout: `${verifications === 1 ? expected : moved}\trefs/tags/v1.2.3^{}\n`,
          stderr: '',
        }
      }
      return base.executor(name, args, options)
    }
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile: path.join(temporary, 'summary.tsv'),
        temporaryDirectory: temporary,
        executor,
        expectedReleaseSHA: expected,
      }),
      /release tag target moved before npm publish/,
    )
    assert.equal(verifications, 2)
    assert.equal(
      base.calls.filter((call) => call.name === 'npm' && call.args[0] === 'publish').length,
      1,
    )
  })

  test('rejects a moved annotated tag object even when its target is unchanged', async () => {
    const root = await createRepositoryFixture()
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const base = createFakeExecutor({ remote: 'missing' })
    const expectedTarget = '1'.repeat(40)
    const expectedObject = '2'.repeat(40)
    const movedObject = '3'.repeat(40)
    let verifications = 0
    const executor = async (name, args, options) => {
      if (name === 'git' && args[0] === 'ls-remote') {
        verifications += 1
        return {
          stdout:
            `${verifications === 1 ? expectedObject : movedObject}\trefs/tags/v1.2.3\n` +
            `${expectedTarget}\trefs/tags/v1.2.3^{}\n`,
          stderr: '',
        }
      }
      return base.executor(name, args, options)
    }
    await assert.rejects(
      publishPackages({
        versionTag: 'v1.2.3',
        root,
        summaryFile: path.join(temporary, 'summary.tsv'),
        temporaryDirectory: temporary,
        executor,
        expectedReleaseSHA: expectedTarget,
        expectedTagObject: expectedObject,
      }),
      /release tag object moved before npm publish/,
    )
    assert.equal(verifications, 2)
    assert.equal(
      base.calls.filter((call) => call.name === 'npm' && call.args[0] === 'publish').length,
      1,
    )
  })

  test('accepts a matching lightweight release tag during npm verification', async () => {
    const root = await createRepositoryFixture()
    const temporary = await temporaryDirectory('gate-publish-temp-')
    const base = createFakeExecutor({ remote: 'missing' })
    const expected = '1'.repeat(40)
    const executor = async (name, args, options) => {
      if (name === 'git' && args[0] === 'ls-remote') {
        if (args[2].endsWith('^{}')) {
          return { stdout: '', stderr: '' }
        }
        return { stdout: `${expected}\trefs/tags/v1.2.3\n`, stderr: '' }
      }
      return base.executor(name, args, options)
    }
    await publishPackages({
      versionTag: 'v1.2.3',
      root,
      summaryFile: path.join(temporary, 'summary.tsv'),
      temporaryDirectory: temporary,
      executor,
      expectedReleaseSHA: expected,
    })
    assert.equal(
      base.calls.filter((call) => call.name === 'npm' && call.args[0] === 'publish').length,
      5,
    )
  })
})

test('release workflow uses the Node entrypoint and the shell orchestrator stays deleted', async () => {
  const workflow = await readFile(
    path.join(repositoryRoot, '.github/workflows/release.yml'),
    'utf8',
  )
  assert.match(
    workflow,
    /node \.\.\/tooling\/scripts\/node\/publish-packages\.mjs "\$\{VERSION_TAG\}" bin/,
  )
  assert.doesNotMatch(workflow, /publish-npm\.sh/)
  await assert.rejects(
    readFile(path.join(repositoryRoot, '.github/scripts/publish-npm.sh')),
    (error) => error.code === 'ENOENT',
  )
})

import { spawn } from 'node:child_process'
import { chmod, cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)))
const tempRoot = await mkdtemp(join(tmpdir(), 'gate-node-smoke-'))
const packDir = join(tempRoot, 'packs')
const examplesDir = join(tempRoot, 'examples')
const gateHome = join(tempRoot, 'gate-home')
const gateBin = join(repoRoot, 'bin', 'gate')
const binaryTargets = [
  ['darwin', 'arm64'],
  ['darwin', 'x64'],
  ['linux', 'arm64'],
  ['linux', 'x64'],
]

const examples = ['service-basic', 'custom-domain-error']
const { GATE_BIN: _gateBin, ...baseEnv } = process.env
const packageTarballs = await resolvePackageTarballs()

try {
  await mkdir(packDir, { recursive: true })
  await mkdir(examplesDir, { recursive: true })
  await mkdir(gateHome, { recursive: true })
  await run('pnpm', ['node:build'], { cwd: repoRoot })
  await run('pnpm', ['--filter', '@jinyongp/gate', 'pack', '--pack-destination', packDir], {
    cwd: repoRoot,
  })
  for (const [platform, arch] of binaryTargets) {
    await packBinaryPackage(
      platform,
      arch,
      await readPackageVersion(binaryPackagePath(platform, arch)),
    )
  }

  for (const example of examples) {
    const source = join(repoRoot, 'examples', 'node', example)
    const target = join(examplesDir, example)
    await cp(source, target, {
      recursive: true,
      filter: (path) => !path.split('/').includes('node_modules'),
    })
    await rewriteGateDependencies(target)
    await writeOverrides(target)
    await run(
      'pnpm',
      ['install', '--offline', '--no-lockfile', '--config.dangerouslyAllowAllBuilds=true'],
      { cwd: target },
    )
    await run('pnpm', ['exec', 'gate', '--version'], { cwd: target, env: baseEnv })
    await run('pnpm', ['smoke'], {
      cwd: target,
      env: {
        ...baseEnv,
        XDG_CONFIG_HOME: join(gateHome, 'config'),
        XDG_STATE_HOME: join(gateHome, 'state'),
      },
    })
    console.log(`smoke passed: ${example}`)
  }
} finally {
  if (process.env.GATE_KEEP_SMOKE_TEMP !== '1') {
    await rm(tempRoot, { recursive: true, force: true })
  } else {
    console.log(`kept smoke temp: ${tempRoot}`)
  }
}

async function rewriteGateDependencies(exampleDir) {
  const packagePath = join(exampleDir, 'package.json')
  const pkg = JSON.parse(await readFile(packagePath, 'utf8'))
  for (const section of ['dependencies', 'devDependencies', 'optionalDependencies']) {
    if (!pkg[section]) {
      continue
    }
    for (const [name, tarball] of Object.entries(packageTarballs)) {
      if (pkg[section][name]) {
        pkg[section][name] = `file:${tarball}`
      }
    }
    if (Object.keys(pkg[section]).length === 0) {
      delete pkg[section]
    }
  }
  await writeFile(packagePath, `${JSON.stringify(pkg, null, 2)}\n`)
}

async function writeOverrides(exampleDir) {
  const lines = ['overrides:']
  for (const [name, tarball] of Object.entries(packageTarballs)) {
    lines.push(`  "${name}": "file:${tarball}"`)
  }
  await writeFile(join(exampleDir, 'pnpm-workspace.yaml'), `${lines.join('\n')}\n`)
}

async function packBinaryPackage(platform, arch, version) {
  const packageName = `@jinyongp/gate-${platform}-${arch}`
  const packageDir = join(tempRoot, 'binary-packages', `${platform}-${arch}`)
  const packageBinDir = join(packageDir, 'bin')
  await mkdir(packageBinDir, { recursive: true })
  if (platform === process.platform && arch === process.arch) {
    await cp(gateBin, join(packageBinDir, 'gate'))
  } else {
    await writeFile(join(packageBinDir, 'gate'), '#!/usr/bin/env sh\nexit 1\n')
  }
  await chmod(join(packageBinDir, 'gate'), 0o755)
  await writeFile(
    join(packageDir, 'package.json'),
    `${JSON.stringify(
      {
        name: packageName,
        version,
        os: [platform],
        cpu: [arch],
        files: ['bin/gate'],
        license: 'MIT',
      },
      null,
      2,
    )}\n`,
  )
  await run('pnpm', ['pack', '--pack-destination', packDir], { cwd: packageDir })
}

async function resolvePackageTarballs() {
  const tarballs = {
    '@jinyongp/gate': join(
      packDir,
      `jinyongp-gate-${await readPackageVersion(join(repoRoot, 'packages', 'node', 'package.json'))}.tgz`,
    ),
  }
  for (const [platform, arch] of binaryTargets) {
    const packageName = `@jinyongp/gate-${platform}-${arch}`
    const version = await readPackageVersion(binaryPackagePath(platform, arch))
    tarballs[packageName] = join(packDir, `jinyongp-gate-${platform}-${arch}-${version}.tgz`)
  }
  return tarballs
}

async function readPackageVersion(packagePath) {
  const pkg = JSON.parse(await readFile(packagePath, 'utf8'))
  return pkg.version
}

function binaryPackagePath(platform, arch) {
  return join(repoRoot, 'packages', 'binaries', `${platform}-${arch}`, 'package.json')
}

async function run(command, args, options = {}) {
  console.log(`$ ${command} ${args.join(' ')}`)
  await new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env ?? process.env,
      stdio: 'inherit',
    })
    child.on('error', reject)
    child.on('exit', (code, signal) => {
      if (code === 0) {
        resolvePromise()
        return
      }
      const label = [command, ...args].join(' ')
      reject(new Error(`${label} failed with ${signal ? `signal ${signal}` : `exit code ${code}`}`))
    })
  })
}

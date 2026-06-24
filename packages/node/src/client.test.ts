import { chmod, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { expect, test } from 'vitest'
import { createGateClient, GateError, isGateError, resolveGateBinary } from './index.js'
import { parseProjectConfig, resolveServiceDomain } from './preflight.js'

test('resolveGateBinary prefers explicit bin, env, then package', () => {
  expect(resolveGateBinary({ bin: '/tmp/gate' })).toBe('/tmp/gate')
  expect(resolveGateBinary({ env: { GATE_BIN: '/env/gate' } })).toBe('/env/gate')
  expect(
    resolveGateBinary({
      platform: 'darwin',
      arch: 'arm64',
      resolvePackage: (specifier) => `/resolved/${specifier}`,
    }),
  ).toBe('/resolved/@jinyongp/gate-darwin-arm64/bin/gate')
})

test('resolveGateBinary reports missing platform package instead of falling back to PATH', () => {
  expect(() =>
    resolveGateBinary({
      platform: 'darwin',
      arch: 'arm64',
      resolvePackage: () => {
        throw new Error('missing optional dep')
      },
    }),
  ).toThrowError(expect.objectContaining({ code: 'GATE_BINARY_NOT_FOUND' }))
})

test('resolveGateBinary reports unsupported platform', () => {
  expect(() => resolveGateBinary({ platform: 'win32', arch: 'x64' })).toThrowError(
    expect.objectContaining({ code: 'GATE_UNSUPPORTED_PLATFORM' }),
  )
})

test('client service runs up then reads service metadata', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.localhost"\n\n[services.web]\n`,
  )

  const client = createGateClient({ bin: gate, cwd: dir })
  const service = await client.service('web')

  expect(service.service).toBe('web')
  expect(service.port).toBe(4312)
  expect(service.url).toBe('https://web.demo.localhost')
  expect(service.loopbackUrl).toBe('http://127.0.0.1:4312')

  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/up --json --dns localhost/)
  expect(calls).toMatch(/ls --json/)
})

test('client service method is safe to destructure', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'))
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.localhost"\n\n[services.web]\n`,
  )

  const { service } = createGateClient({ bin: gate, cwd: dir })
  const web = await service('web')

  expect(web.port).toBe(4312)
})

test('client maps permission failures', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'permission' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.up()).rejects.toMatchObject({
    code: 'GATE_PERMISSION_REQUIRED',
    exitCode: 3,
    gateCode: 'permission',
  })
})

test('client maps invalid JSON', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'invalid-json' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.ls()).rejects.toMatchObject({ code: 'GATE_JSON_PARSE_FAILED' })
})

test('default DNS preflight rejects custom domains before mutating up', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.service('web')).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('default DNS preflight rejects single-quoted custom domains before mutating up', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.localhost"\n\n[services.web]\ndomain = 'web.demo.test'\n`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.service('web')).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('default DNS preflight discovers parent gate config', async () => {
  const dir = await tempDir()
  const nested = join(dir, 'app', 'web')
  await mkdir(nested, { recursive: true })
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`,
  )
  const client = createGateClient({ bin: gate, cwd: nested })

  await expect(client.service('web')).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('isGateError narrows by code', () => {
  const error = new GateError({ code: 'GATE_DNS_REQUIRED', message: 'dns required' })

  expect(isGateError(error)).toBe(true)
  expect(isGateError(error, 'GATE_DNS_REQUIRED')).toBe(true)
  expect(isGateError(error, 'GATE_PERMISSION_REQUIRED')).toBe(false)
  expect(isGateError({ name: 'GateError', code: 'GATE_DNS_REQUIRED' }, 'GATE_DNS_REQUIRED')).toBe(
    true,
  )
  expect(isGateError({ name: 'GateError', code: 'NOPE' })).toBe(false)
})

test('preconfigured DNS bypasses custom-domain guard', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  await client.service('web', { dns: 'preconfigured' })

  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/up --json --dns localhost/)
})

test('timeout still aborts when caller passes a signal', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'hang' })
  const signal = new AbortController().signal
  const client = createGateClient({ bin: gate, cwd: dir, signal, timeoutMs: 10 })

  await expect(client.ls()).rejects.toMatchObject({ code: 'GATE_COMMAND_FAILED' })
})

test('project config parser resolves service domains', () => {
  const config = parseProjectConfig(`[project]
name = "demo"
base = "demo.localhost"

[services.web]

[services.root]
host = "."

[services.api]
domain = "api.example.test"
`)
  expect(resolveServiceDomain(config, 'web')).toBe('web.demo.localhost')
  expect(resolveServiceDomain(config, 'root')).toBe('demo.localhost')
  expect(resolveServiceDomain(config, 'api')).toBe('api.example.test')
})

async function tempDir(): Promise<string> {
  return await mkdtemp(join(tmpdir(), 'gate-node-'))
}

async function fakeGate(
  dir: string,
  log: string,
  options: { mode?: 'permission' | 'invalid-json' | 'hang' } = {},
): Promise<string> {
  const path = join(dir, 'gate-fake.mjs')
  const body = `#!/usr/bin/env node
import { appendFileSync } from "node:fs";
const args = process.argv.slice(2);
appendFileSync(${JSON.stringify(log)}, args.join(" ") + "\\n");
if (${JSON.stringify(options.mode)} === "permission") {
  console.error(JSON.stringify({ error: { code: "permission", message: "permission required" } }));
  process.exit(3);
}
if (${JSON.stringify(options.mode)} === "invalid-json") {
  console.log("not json");
  process.exit(0);
}
if (${JSON.stringify(options.mode)} === "hang") {
  await new Promise(() => {});
}
const cmd = args[0];
if (cmd === "up") {
  console.log(JSON.stringify({ project: "demo", reloaded: false, services: [{ service: "web", domain: "web.demo.localhost", port: 4312, allocated: true }] }));
  process.exit(0);
}
if (cmd === "ls") {
  console.log(JSON.stringify({ services: [{ project: "demo", service: "web", domain: "web.demo.localhost", port: 4312, route: "active", upstream: "down" }] }));
  process.exit(0);
}
if (cmd === "port") {
  console.log(JSON.stringify({ service: args.at(-1), port: 4312 }));
  process.exit(0);
}
if (cmd === "down") {
  console.log(JSON.stringify({ ok: true }));
  process.exit(0);
}
console.error("unknown command");
process.exit(2);
`
  await writeFile(path, body)
  await chmod(path, 0o755)
  return path
}

import { chmod, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { expect, test } from 'vitest'
import { createGateClient, GateError, isGateError, resolveGateBinary } from './index.js'

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
  expect(calls).toMatch(/^up --json$/m)
  expect(calls).toMatch(/ls --json/)
})

test('client preserves listener-aware URLs from ls', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost:9443',
      },
    ],
  })
  const client = createGateClient({ bin: gate, cwd: dir })

  const [listed] = await client.ls()
  const selected = await client.service('web', { up: false })
  expect(listed?.url).toBe('https://web.demo.localhost:9443')
  expect(selected.url).toBe('https://web.demo.localhost:9443')
})

test('client preserves listener-aware URLs from up', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost:9443',
      },
    ],
  })
  const client = createGateClient({ bin: gate, cwd: dir })

  const result = await client.up()

  expect(result.services[0]?.url).toBe('https://web.demo.localhost:9443')
  expect(result.services[0]?.loopbackUrl).toBe('http://127.0.0.1:4312')
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

test('client service accepts inline project config without gate.toml', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })

  const service = await client.service('web', {
    scope: {
      config: {
        name: 'demo',
        base: 'demo.localhost',
        services: { web: {} },
      },
    },
  })

  expect(service.url).toBe('https://web.demo.localhost')

  const calls = await readFile(log, 'utf8')
  const paths = configPaths(calls)
  expect(paths).toHaveLength(3)
  expect(new Set(paths).size).toBe(1)
  expect(calls).toMatch(/up --json --config \S+/)
  expect(calls).not.toMatch(/up [^\n]*--dns/)
  expect(calls).toMatch(/ls --json --config \S+/)
  expect(await readFile(paths[0] ?? '', 'utf8')).toBe(
    `[project]
name = "demo"
base = "demo.localhost"

[services.web]
`,
  )
})

test('client commands pass stable generated config path for inline project config', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })
  const scope = {
    config: {
      name: 'demo',
      base: 'demo.localhost',
      services: {
        api: {
          domain: 'api.demo.localhost',
          env: ['API_URL'],
          routeEnv: ['PUBLIC_API_URL'],
          port: 4313,
        },
        web: { host: '.', port: '${WEB_PORT:-3000}' },
      },
    },
  }

  await client.up({ scope })
  await client.ls({ scope })
  await client.port('web', { scope })
  await client.down({ scope })

  const calls = await readFile(log, 'utf8')
  const paths = configPaths(calls)
  expect(paths).toHaveLength(5)
  expect(new Set(paths).size).toBe(1)
  expect(calls).toMatch(/up --json --config \S+/)
  expect(calls).toMatch(/ls --json --config \S+/)
  expect(calls).toMatch(/port --json --config \S+ web/)
  expect(calls).toMatch(/down --json --config \S+/)
  expect(await readFile(paths[0] ?? '', 'utf8')).toBe(
    `[project]
name = "demo"
base = "demo.localhost"

[services.api]
domain = "api.demo.localhost"
port = 4313
env = ["API_URL"]
route_env = ["PUBLIC_API_URL"]

[services.web]
host = "."
port = "\${WEB_PORT:-3000}"
`,
  )
})

test('client isolatedRoot relocates gate subprocess state and inline config cache', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const envLog = join(dir, 'env.log')
  const gate = await fakeGate(dir, log, { envLog })
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    isolatedRoot: '.gate-agent',
  })

  await client.up({
    scope: {
      config: {
        name: 'demo',
        base: 'demo.localhost',
        services: { web: {} },
      },
    },
  })

  const root = join(dir, '.gate-agent')
  const env = JSON.parse(await readFile(envLog, 'utf8')) as Record<string, string>
  expect(env.GATE_ISOLATED_ROOT).toBe(root)
  expect(env.GATE_NODE_CACHE_DIR).toBe(join(root, 'cache', 'node'))
  expect(env.XDG_CONFIG_HOME).toBe(join(root, 'xdg', 'config'))
  expect(env.XDG_STATE_HOME).toBe(join(root, 'xdg', 'state'))
  expect(env.XDG_DATA_HOME).toBe(join(root, 'xdg', 'data'))

  const calls = await readFile(log, 'utf8')
  const paths = configPaths(calls)
  expect(paths[0]).toContain(join(root, 'cache', 'node', 'inline-config'))
})

test('client rejects isolatedRoot with daemon true before invoking gate', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  await expect(client.up({ daemon: true } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
    message: expect.stringContaining('cannot be combined with effective isolated gate state'),
  })

  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects options env isolated root with daemon true', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { GATE_ISOLATED_ROOT: '.gate-agent' },
  })

  await expect(client.up({ daemon: true })).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
  })
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects ambient isolated root with daemon true', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const previous = process.env.GATE_ISOLATED_ROOT
  process.env.GATE_ISOLATED_ROOT = join(dir, '.gate-agent')
  try {
    const client = createGateClient({ bin: gate, cwd: dir })
    await expect(client.up({ daemon: true })).rejects.toMatchObject({
      code: 'GATE_INVALID_OPTIONS',
    })
  } finally {
    if (previous === undefined) {
      delete process.env.GATE_ISOLATED_ROOT
    } else {
      process.env.GATE_ISOLATED_ROOT = previous
    }
  }
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client allows isolatedRoot with daemon false', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  await client.up({ daemon: false })

  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/up --json/)
  expect(calls).not.toMatch(/--daemon/)
})

test('per-call undefined isolatedRoot preserves client default', async () => {
  const dir = await tempDir()
  const envLog = join(dir, 'env.json')
  const gate = await fakeGate(dir, join(dir, 'args.log'), { envLog })
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  await client.ls({ isolatedRoot: undefined } as any)
  const env = JSON.parse(await readFile(envLog, 'utf8')) as Record<string, string>
  expect(env.GATE_ISOLATED_ROOT).toBe(join(dir, '.gate-agent'))
})

test('client rejects empty isolatedRoot before invoking gate', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.ls({ isolatedRoot: '' } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
    message: expect.stringContaining('non-empty path'),
  })
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects per-call isolatedRoot with daemon true before invoking gate', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(
    client.up({ isolatedRoot: '.gate-agent', daemon: true } as any),
  ).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
    message: expect.stringContaining('cannot be combined with effective isolated gate state'),
  })

  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects isolatedRoot daemon true even when route activation is skipped', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  await expect(client.service('web', { up: false, daemon: true } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
  })
  await expect(client.ready('web', { up: false, daemon: true } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
  })

  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects isolatedRoot daemon true on inspection-only methods', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  await expect(client.ls({ daemon: true } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
  })
  await expect(client.port('web', { daemon: true } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
  })
  await expect(client.down({ daemon: true } as any)).rejects.toMatchObject({
    code: 'GATE_INVALID_OPTIONS',
  })

  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects isolatedRoot daemon true before running from a ready descriptor', async () => {
  const dir = await tempDir()
  const child = await fakeChild(dir, 'process.exit(0)')
  const client = createGateClient({ cwd: dir, isolatedRoot: '.gate-agent' })

  await expect(
    client.run(
      {
        service: {
          project: 'demo',
          service: 'web',
          domain: 'web.demo.localhost',
          port: 4312,
          url: 'https://web.demo.localhost',
          loopbackUrl: 'http://127.0.0.1:4312',
          route: 'active',
          upstream: 'down',
        },
        env: { PORT: '4312' },
        diagnostics: [],
      },
      [child],
      { daemon: true } as any,
    ),
  ).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
})

test('client env returns inline declared loopback and route env values', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      { service: 'api', domain: 'api.demo.localhost', port: 4313 },
      { service: 'web', domain: 'web.demo.localhost', port: 4312 },
    ],
  })
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  const env = await client.env('web', {
    scope: {
      config: {
        name: 'demo',
        base: 'demo.localhost',
        services: {
          web: {},
          api: {
            env: 'API_URL',
            routeEnv: 'PUBLIC_API_URL',
          },
        },
      },
    },
  })

  expect(env).toMatchObject({
    PORT: '4312',
    GATE_API_PORT: '4313',
    GATE_API_URL: 'http://127.0.0.1:4313',
    GATE_API_ROUTE_URL: 'https://api.demo.localhost',
    GATE_WEB_PORT: '4312',
    GATE_WEB_URL: 'http://127.0.0.1:4312',
    GATE_WEB_ROUTE_URL: 'https://web.demo.localhost',
    API_URL: 'http://127.0.0.1:4313',
    PUBLIC_API_URL: 'https://api.demo.localhost',
  })
})

test('client env reads file-backed env and route_env declarations', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      { service: 'api', domain: 'api.demo.localhost', port: 4313 },
      { service: 'web', domain: 'web.demo.localhost', port: 4312 },
    ],
  })
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]
name = "demo"
base = "demo.localhost"

[services.web]

[services.api]
env = ["API_URL"]
route_env = "PUBLIC_API_URL"
`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  const env = await client.env('web')

  expect(env.API_URL).toBe('http://127.0.0.1:4313')
  expect(env.PUBLIC_API_URL).toBe('https://api.demo.localhost')
})

test('client env project-only scope without config returns registry-derived values', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      { service: 'api', domain: 'api.demo.localhost', port: 4313 },
      { service: 'web', domain: 'web.demo.localhost', port: 4312 },
    ],
  })
  const client = createGateClient({ bin: gate, cwd: dir })

  const env = await client.env('web', {
    up: false,
    scope: { kind: 'project', project: 'demo' },
  })

  expect(env.PORT).toBe('4312')
  expect(env.GATE_API_URL).toBe('http://127.0.0.1:4313')
  expect(env.API_URL).toBeUndefined()
  expect(env.PUBLIC_API_URL).toBeUndefined()
})

test('client env project-only scope ignores cwd config for a different project', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      { service: 'api', domain: 'api.demo.localhost', port: 4313 },
      { service: 'web', domain: 'web.demo.localhost', port: 4312 },
    ],
  })
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]
name = "other"
base = "other.localhost"

[services.api]
env = "API_URL"
route_env = "PUBLIC_API_URL"
`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  const env = await client.env('web', {
    up: false,
    scope: { kind: 'project', project: 'demo' },
  })

  expect(env.GATE_API_URL).toBe('http://127.0.0.1:4313')
  expect(env.API_URL).toBeUndefined()
  expect(env.PUBLIC_API_URL).toBeUndefined()
})

test('client run injects generated env into child process', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      { service: 'api', domain: 'api.demo.localhost', port: 4313 },
      { service: 'web', domain: 'web.demo.localhost', port: 4312 },
    ],
  })
  const child = await fakeChild(
    dir,
    `console.log(JSON.stringify({
      PORT: process.env.PORT,
      API_URL: process.env.API_URL,
      PUBLIC_API_URL: process.env.PUBLIC_API_URL,
      GATE_API_ROUTE_URL: process.env.GATE_API_ROUTE_URL,
    }));`,
  )
  const client = createGateClient({ bin: gate, cwd: dir, isolatedRoot: '.gate-agent' })

  const result = await client.run('web', [process.execPath, child], {
    stdio: 'pipe',
    scope: {
      config: {
        name: 'demo',
        base: 'demo.localhost',
        services: {
          web: {},
          api: {
            env: 'API_URL',
            routeEnv: 'PUBLIC_API_URL',
          },
        },
      },
    },
  })

  expect(result.exitCode).toBe(0)
  expect(result.service?.url).toBe('https://web.demo.localhost')
  expect(result.env?.PORT).toBe('4312')
  expect(JSON.parse(result.stdout ?? '{}')).toEqual({
    PORT: '4312',
    API_URL: 'http://127.0.0.1:4313',
    PUBLIC_API_URL: 'https://api.demo.localhost',
    GATE_API_ROUTE_URL: 'https://api.demo.localhost',
  })
})

test('client run uses gate env descriptor route URLs with listener ports', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), {
    services: [
      {
        service: 'api',
        domain: 'api.demo.localhost',
        port: 4313,
        url: 'https://api.demo.localhost:3443',
      },
      {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost:3443',
      },
    ],
  })
  const child = await fakeChild(
    dir,
    `console.log(JSON.stringify({
      route: process.env.GATE_WEB_ROUTE_URL,
      apiRoute: process.env.GATE_API_ROUTE_URL,
    }));`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })
  const readyUrls: string[] = []

  const result = await client.run('web', [process.execPath, child], {
    stdio: 'pipe',
    up: false,
    onReady({ service, env }) {
      readyUrls.push(service.url, env.GATE_API_ROUTE_URL)
    },
  })

  expect(readyUrls).toEqual(['https://web.demo.localhost:3443', 'https://api.demo.localhost:3443'])
  expect(result.service?.url).toBe('https://web.demo.localhost:3443')
  expect(result.env?.GATE_WEB_ROUTE_URL).toBe('https://web.demo.localhost:3443')
  expect(JSON.parse(result.stdout ?? '{}')).toEqual({
    route: 'https://web.demo.localhost:3443',
    apiRoute: 'https://api.demo.localhost:3443',
  })
})

test('client ready returns canonical env descriptor diagnostics', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'))
  const client = createGateClient({ bin: gate, cwd: dir })

  const ready = await client.ready('web', { up: false })

  expect(ready.service.url).toBe('https://web.demo.localhost')
  expect(ready.env.PORT).toBe('4312')
  expect(ready.envKeys).toEqual(['GATE_WEB_PORT', 'GATE_WEB_ROUTE_URL', 'GATE_WEB_URL', 'PORT'])
  expect(ready.daemon).toMatchObject({
    required: true,
    running: false,
    listener: 'listener:https-443-http-80',
  })
  expect(ready.diagnostics).toEqual([
    {
      code: 'daemon_not_running',
      severity: 'fixable',
      message: 'listener daemon is not running',
      suggestedCommand: 'gate up --daemon',
      actions: [{ label: 'Start listener daemon', command: 'gate up --daemon' }],
    },
  ])
})

test('client run accepts ready descriptor without resolving it again', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const child = await fakeChild(dir, `console.log(process.env.PORT);`)
  const client = createGateClient({ bin: gate, cwd: dir })

  const ready = await client.ready('web', { up: false })
  const result = await client.run(ready, [process.execPath, child], { stdio: 'pipe' })

  expect(result.stdout?.trim()).toBe('4312')
  expect(await readFile(log, 'utf8')).toBe('env --json web\n')
})

test('client run accepts legacy ready descriptor without diagnostics', async () => {
  const dir = await tempDir()
  const child = await fakeChild(dir, `console.log(process.env.PORT);`)
  const client = createGateClient({ bin: join(dir, 'missing-gate'), cwd: dir })

  const result = await client.run(
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: '4312' },
    },
    [process.execPath, child],
    { stdio: 'pipe' },
  )

  expect(result.stdout?.trim()).toBe('4312')
  expect(result.service?.service).toBe('web')
  expect(result.env?.PORT).toBe('4312')
  expect(result.envKeys).toEqual(['PORT'])
})

test.each([
  ['missing service fields', { service: { service: 'web', port: 4312 }, env: {} }],
  [
    'array env',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: [],
      diagnostics: [],
    },
  ],
  [
    'non-string env value',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: 4312 },
      diagnostics: [],
    },
  ],
  [
    'bad envKeys',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: '4312' },
      envKeys: ['PORT', 4312],
      diagnostics: [],
    },
  ],
  [
    'mismatched envKeys',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: '4312' },
      envKeys: ['GATE_WEB_PORT'],
      diagnostics: [],
    },
  ],
  [
    'bad daemon optional field',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: '4312' },
      daemon: {
        required: true,
        running: false,
        listener: 'listener:https-443-http-80',
        httpsAddr: 443,
      },
      diagnostics: [],
    },
  ],
  [
    'bad diagnostic optional field',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: '4312' },
      diagnostics: [
        {
          code: 'daemon_not_running',
          severity: 'surprising',
          message: 'listener daemon is not running',
          suggestedCommand: 123,
        },
      ],
    },
  ],
  [
    'bad diagnostic action',
    {
      service: {
        service: 'web',
        domain: 'web.demo.localhost',
        port: 4312,
        url: 'https://web.demo.localhost',
        loopbackUrl: 'http://127.0.0.1:4312',
        route: 'active',
        upstream: 'down',
      },
      env: { PORT: '4312' },
      diagnostics: [
        {
          code: 'daemon_not_running',
          severity: 'fixable',
          message: 'listener daemon is not running',
          actions: [{ label: 123 }],
        },
      ],
    },
  ],
])(
  'client run rejects malformed ready descriptors before spawning child: %s',
  async (_name, ready) => {
    const dir = await tempDir()
    const gate = await fakeGate(dir, join(dir, 'args.log'))
    const child = await fakeChild(dir, `console.log("ran");`)
    const client = createGateClient({ bin: gate, cwd: dir })

    await expect(client.run(ready as never, [process.execPath, child])).rejects.toMatchObject({
      code: 'GATE_INVALID_OPTIONS',
      message: 'ready descriptor must come from gate.ready()',
    })
  },
)

test('client run calls onReady with service and env before spawning child', async () => {
  const dir = await tempDir()
  const marker = join(dir, 'ready.txt')
  const gate = await fakeGate(dir, join(dir, 'args.log'))
  const child = await fakeChild(
    dir,
    `import { existsSync, readFileSync } from 'node:fs';
console.log(JSON.stringify({
  ready: existsSync(${JSON.stringify(marker)}),
  marker: readFileSync(${JSON.stringify(marker)}, 'utf8'),
  port: process.env.PORT,
}));`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })
  const readyCalls: string[] = []

  const result = await client.run('web', [process.execPath, child], {
    stdio: 'pipe',
    up: false,
    async onReady({ service, env }) {
      readyCalls.push(service.url)
      await writeFile(marker, env.PORT)
    },
  })

  expect(readyCalls).toEqual(['https://web.demo.localhost'])
  expect(result.service?.service).toBe('web')
  expect(result.env?.PORT).toBe('4312')
  expect(JSON.parse(result.stdout ?? '{}')).toEqual({
    ready: true,
    marker: '4312',
    port: '4312',
  })
})

test('client run does not spawn child when onReady fails', async () => {
  const dir = await tempDir()
  const marker = join(dir, 'child-ran.txt')
  const gate = await fakeGate(dir, join(dir, 'args.log'))
  const child = await fakeChild(
    dir,
    `import { writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(marker)}, 'ran');`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(
    client.run('web', [process.execPath, child], {
      stdio: 'pipe',
      up: false,
      onReady() {
        throw new Error('ready failed')
      },
    }),
  ).rejects.toMatchObject({
    code: 'GATE_COMMAND_FAILED',
    message: 'run onReady failed',
  })
  await expect(readFile(marker, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client run reports non-zero child exits with captured output', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'))
  const child = await fakeChild(
    dir,
    `process.stdout.write("child out");
process.stderr.write("child err");
process.exit(7);`,
  )
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(
    client.run('web', [process.execPath, child], { stdio: 'pipe', up: false }),
  ).rejects.toMatchObject({
    code: 'GATE_COMMAND_FAILED',
    exitCode: 7,
    stdout: 'child out',
    stderr: 'child err',
    command: [process.execPath, child],
  })
})

test('client maps permission failures', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'permission' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.up()).rejects.toMatchObject({
    code: 'GATE_PERMISSION_REQUIRED',
    exitCode: 3,
    gateCode: 'permission',
    severity: 'permission',
    retryable: false,
    hint: 'Run outside the sandbox.',
    nextActions: [{ label: 'Check setup', command: 'gate doctor --json' }],
  })
})

test('client maps invalid JSON', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'invalid-json' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.ls()).rejects.toMatchObject({
    code: 'GATE_JSON_PARSE_FAILED',
    command: [gate, 'ls', '--json'],
  })
})

test('client rejects valid JSON with the wrong response shape', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'wrong-shape' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.ls()).rejects.toMatchObject({ code: 'GATE_JSON_PARSE_FAILED' })
  await expect(client.up()).rejects.toMatchObject({ code: 'GATE_JSON_PARSE_FAILED' })
})

test('client maps gate not_allocated failures', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'not-allocated' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.port('missing')).rejects.toMatchObject({
    code: 'GATE_SERVICE_NOT_FOUND',
    gateCode: 'not_allocated',
  })
})

test('client maps gate no_service failures', async () => {
  const dir = await tempDir()
  const gate = await fakeGate(dir, join(dir, 'args.log'), { mode: 'no-service' })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.port('missing')).rejects.toMatchObject({
    code: 'GATE_SERVICE_NOT_FOUND',
    gateCode: 'no_service',
  })
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
  await expectResolverWithoutUp(log)
})

test('default DNS preflight preserves implicit global custom-domain scope', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log, {
    services: [{ service: 'web', domain: 'web.example.test', port: 4312 }],
  })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.service('web')).resolves.toMatchObject({ domain: 'web.example.test' })
  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/^__resolve-project(?: |$)/m)
  expect(calls).toMatch(/^up(?: |$)/m)
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
  await expectResolverWithoutUp(log)
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
  await expectResolverWithoutUp(log)
})

test('default DNS preflight skips non-file gate config while walking upward', async () => {
  const dir = await tempDir()
  const nested = join(dir, 'app')
  await mkdir(join(nested, 'gate.toml'), { recursive: true })
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`,
  )
  const client = createGateClient({ bin: gate, cwd: nested })

  await expect(client.service('web')).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expectResolverWithoutUp(log)
})

test('default DNS preflight stops at git boundary like gate config discovery', async () => {
  const dir = await tempDir()
  const nested = join(dir, 'repo', 'app')
  await mkdir(join(dir, 'repo', '.git'), { recursive: true })
  await mkdir(nested, { recursive: true })
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  await writeFile(
    join(dir, 'gate.toml'),
    `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`,
  )
  const client = createGateClient({ bin: gate, cwd: nested })

  await expect(client.service('web')).resolves.toMatchObject({ domain: 'web.demo.localhost' })
  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/^__resolve-project(?: |$)/m)
  expect(calls).toMatch(/^up(?: |$)/m)
})

test('default DNS preflight rejects custom inline config domains before mutating up', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })

  await expect(
    client.service('web', {
      scope: {
        config: {
          name: 'demo',
          base: 'demo.test',
          services: { web: {} },
        },
      },
    }),
  ).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expectResolverWithoutUp(log)
})

test('up rejects custom inline config domains before mutating routes', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })

  await expect(
    client.up({
      scope: {
        config: {
          name: 'demo',
          base: 'demo.test',
          services: { web: {}, api: { domain: 'api.demo.localhost' } },
        },
      },
    }),
  ).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expectResolverWithoutUp(log)
})

test('up allows custom inline config domains with explicit DNS mode', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })

  await client.up({
    dns: 'preconfigured',
    scope: {
      config: {
        name: 'demo',
        base: 'demo.test',
        services: { web: {} },
      },
    },
  })

  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/up --json --config \S+/)
})

test('implicit global DNS preserves stored mode without forcing localhost', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log, {
    services: [{ service: 'web', domain: 'web.example.test', port: 4312 }],
  })
  const client = createGateClient({ bin: gate, cwd: dir })

  await client.up({ scope: { kind: 'global' } })

  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/^__resolve-project --global$/m)
  expect(calls).toMatch(/^up --json --global$/m)
  expect(calls).not.toMatch(/^up [^\n]*--dns/m)
})

test('explicit localhost DNS rejects custom global reservations before up', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log, {
    services: [{ service: 'web', domain: 'web.example.test', port: 4312 }],
  })
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(client.up({ scope: { kind: 'global' }, dns: 'localhost' })).rejects.toMatchObject({
    code: 'GATE_DNS_REQUIRED',
  })
  await expectResolverWithoutUp(log)
})

test('inline DNS preflight expands env references before checking localhost', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { BASE_DOMAIN: 'demo.localhost', GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })

  await client.service('web', {
    scope: {
      config: {
        name: 'demo',
        base: '${BASE_DOMAIN}',
        services: { web: {}, api: { domain: '${API_DOMAIN:-api.demo.localhost}' } },
      },
    },
  })

  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/up --json --config \S+/)
})

test('inline DNS preflight rejects env references that resolve to custom domains', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { BASE_DOMAIN: 'demo.test', GATE_NODE_CACHE_DIR: join(dir, 'cache') },
  })

  await expect(
    client.service('web', {
      scope: {
        config: {
          name: 'demo',
          base: '${BASE_DOMAIN}',
          services: { web: {} },
        },
      },
    }),
  ).rejects.toMatchObject({ code: 'GATE_DNS_REQUIRED' })
  await expectResolverWithoutUp(log)
})

test('inline project config rejects invalid scope combinations before running gate', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })
  const config = { name: 'demo', services: { web: {} } }

  await expect(
    client.ls({ scope: { kind: 'project', project: 'other', config } }),
  ).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
  await expect(
    client.up({
      scope: { kind: 'global', config } as never,
    }),
  ).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
  for (const scope of [
    { kind: 'typo' },
    { kind: 'project', project: '' },
    { kind: 'project', config: '  ' },
    { kind: 'project', typo: true },
  ]) {
    await expect(client.ls({ scope: scope as never })).rejects.toMatchObject({
      code: 'GATE_INVALID_OPTIONS',
    })
  }
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('client rejects non-positive and non-finite timeoutMs before spawning gate', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  for (const timeoutMs of [0, -1, Number.POSITIVE_INFINITY, Number.NaN]) {
    const client = createGateClient({ bin: gate, cwd: dir, timeoutMs })
    await expect(client.ls()).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
  }
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('inline project config rejects unsupported fields before running gate', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })

  await expect(
    client.up({
      scope: {
        config: {
          name: 'demo',
          envFiles: ['.env'],
          services: { web: {} },
        } as never,
      },
    }),
  ).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
  await expect(
    client.up({
      scope: {
        config: {
          name: 'demo',
          services: { web: { envFiles: ['.env'] } },
        } as never,
      },
    }),
  ).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
  await expect(
    client.up({
      scope: {
        config: {
          name: 'demo',
          services: { web: { routeEnv: [123] } },
        } as never,
      },
    }),
  ).rejects.toMatchObject({ code: 'GATE_INVALID_OPTIONS' })
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('inline project config rejects project and service names outside CLI grammar', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })

  for (const config of [
    { name: ' demo', services: { web: {} } },
    { name: 'demo', services: { 'web.app': {} } },
    { name: 'demo', services: { stop: {} } },
    { name: 'demo', services: { web: { env: 'PORT' } } },
    { name: 'demo', services: { web: { routeEnv: 'GATE_WEB_URL' } } },
    { name: 'demo', services: { web: { env: 'SHARED' }, api: { routeEnv: 'SHARED' } } },
    { name: 'demo', services: { web: { env: ' SHARED ' }, api: { routeEnv: 'SHARED' } } },
  ]) {
    await expect(client.up({ scope: { config } as never })).rejects.toMatchObject({
      code: 'GATE_INVALID_OPTIONS',
    })
  }
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('isGateError narrows by code', () => {
  const error = new GateError({ code: 'GATE_DNS_REQUIRED', message: 'dns required' })

  expect(isGateError(error)).toBe(true)
  expect(isGateError(error, 'GATE_DNS_REQUIRED')).toBe(true)
  expect(isGateError(error, 'GATE_PERMISSION_REQUIRED')).toBe(false)
  expect(isGateError({ name: 'GateError', code: 'GATE_DNS_REQUIRED' })).toBe(false)
  expect(
    isGateError({
      name: 'GateError',
      code: 'GATE_DNS_REQUIRED',
      message: 'dns required',
      command: ['gate', 'up'],
      nextActions: [],
    }),
  ).toBe(true)
  expect(
    isGateError({
      name: 'GateError',
      code: 'GATE_DNS_REQUIRED',
      message: 'dns required',
      command: ['gate'],
      nextActions: [{ label: 123 }],
    }),
  ).toBe(false)
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
  const client = createGateClient({ bin: gate, cwd: dir, signal, timeoutMs: 100 })

  await expect(client.ls()).rejects.toMatchObject({
    code: 'GATE_COMMAND_FAILED',
    cause: expect.objectContaining({ name: 'AbortError' }),
  })
})

async function tempDir(): Promise<string> {
  return await mkdtemp(join(tmpdir(), 'gate-node-'))
}

async function fakeGate(
  dir: string,
  log: string,
  options: {
    mode?: 'permission' | 'invalid-json' | 'wrong-shape' | 'no-service' | 'not-allocated' | 'hang'
    envLog?: string
    services?: Array<{ service: string; domain: string; port: number; url?: string }>
  } = {},
): Promise<string> {
  const path = join(dir, 'gate-fake.mjs')
  const services = options.services ?? [
    { service: 'web', domain: 'web.demo.localhost', port: 4312 },
  ]
  const body = `#!/usr/bin/env node
import { appendFileSync, existsSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
const args = process.argv.slice(2);
appendFileSync(${JSON.stringify(log)}, args.join(" ") + "\\n");
if (${JSON.stringify(options.envLog)}) {
  writeFileSync(${JSON.stringify(options.envLog)}, JSON.stringify({
    GATE_ISOLATED_ROOT: process.env.GATE_ISOLATED_ROOT,
    GATE_NODE_CACHE_DIR: process.env.GATE_NODE_CACHE_DIR,
    XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME,
    XDG_STATE_HOME: process.env.XDG_STATE_HOME,
    XDG_DATA_HOME: process.env.XDG_DATA_HOME,
  }));
}
if (${JSON.stringify(options.mode)} === "permission") {
  console.error(JSON.stringify({
    error: {
      code: "permission",
      message: "permission required",
      severity: "permission",
      retryable: false,
      hint: "Run outside the sandbox.",
      nextActions: [{ label: "Check setup", command: "gate doctor --json" }],
    },
  }));
  process.exit(3);
}
if (${JSON.stringify(options.mode)} === "invalid-json") {
  console.log("not json");
  process.exit(0);
}
if (${JSON.stringify(options.mode)} === "wrong-shape") {
  console.log(JSON.stringify({ services: null }));
  process.exit(0);
}
if (${JSON.stringify(options.mode)} === "no-service") {
  console.error(JSON.stringify({ error: { code: "no_service", message: "service not found" } }));
  process.exit(2);
}
if (${JSON.stringify(options.mode)} === "not-allocated") {
  console.error(JSON.stringify({ error: { code: "not_allocated", message: "service not allocated" } }));
  process.exit(2);
}
if (${JSON.stringify(options.mode)} === "hang") {
	setInterval(() => {}, 1000);
	await new Promise(() => {});
}
const cmd = args[0];
const services = ${JSON.stringify(services)};
const loopbackUrl = (service) => "http://127.0.0.1:" + service.port;
const routeUrl = (service) => service.url ?? ("https://" + service.domain);
const isFile = (path) => {
	try { return statSync(path).isFile(); } catch { return false; }
};
const discoverConfig = () => {
	let current = resolve(process.cwd());
	while (true) {
		const candidate = join(current, "gate.toml");
		if (isFile(candidate)) return candidate;
		if (existsSync(join(current, ".git"))) return undefined;
		const parent = dirname(current);
		if (parent === current) return undefined;
		current = parent;
	}
};
const configPath = (() => {
  const index = args.indexOf("--config");
  if (index !== -1) return args[index + 1];
	return discoverConfig();
})();
const configBody = configPath && existsSync(configPath) ? readFileSync(configPath, "utf8") : "";
const projectName = (() => {
  const match = configBody.match(/\\[project\\][\\s\\S]*?name\\s*=\\s*"([^"]+)"/);
  return match?.[1];
})();
const includeDeclarations = Boolean(configBody.includes("[services.api]") && configBody.includes("env") && projectName === "demo");
const scalar = (raw) => {
	const value = raw.trim();
	const unquoted = (value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))
		? value.slice(1, -1)
		: value;
	return unquoted.replace(/[$][{]([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?[}]/g, (_, key, fallback) => process.env[key] || fallback || "");
};
const resolvedConfigServices = () => {
	let base;
	let section;
	const found = new Map();
	for (const raw of configBody.split(/\\r?\\n/)) {
		const line = raw.replace(/#.*$/, "").trim();
		if (!line) continue;
		if (line === "[project]") { section = "project"; continue; }
		const header = line.match(/^\\[services\\.(?:"([^"]+)"|'([^']+)'|([A-Za-z0-9_-]+))\\]$/);
		if (header) {
			section = header[1] || header[2] || header[3];
			found.set(section, {});
			continue;
		}
		if (line.startsWith("[")) { section = undefined; continue; }
		const assignment = line.match(/^([A-Za-z_][A-Za-z0-9_]*)\\s*=\\s*(.+)$/);
		if (!assignment) continue;
		const key = assignment[1];
		const value = scalar(assignment[2]);
		if (section === "project" && key === "base") base = value;
		if (section && section !== "project" && (key === "domain" || key === "host")) {
			found.get(section)[key] = value;
		}
	}
	return [...found.entries()].map(([service, config]) => ({
		service,
		domain: config.domain || ((config.host === "." ? "" : (config.host || service) + ".") + base),
	}));
};
const envFor = (selected) => {
  const env = { PORT: String(selected.port) };
  for (const service of services) {
    const key = service.service.trim().toUpperCase().replace(/[^A-Z0-9]/g, "_");
    env["GATE_" + key + "_PORT"] = String(service.port);
    env["GATE_" + key + "_URL"] = loopbackUrl(service);
    env["GATE_" + key + "_ROUTE_URL"] = routeUrl(service);
  }
  const api = services.find((service) => service.service === "api");
  if (includeDeclarations && api) {
    env.API_URL = loopbackUrl(api);
    env.PUBLIC_API_URL = routeUrl(api);
  }
  return env;
};
if (cmd === "__resolve-project") {
	console.log(JSON.stringify({
		scope: args.includes("--global") || !configBody ? "global" : "project",
		project: projectName || "demo",
		services: configBody ? resolvedConfigServices() : services,
	}));
	process.exit(0);
}
if (cmd === "up") {
  console.log(JSON.stringify({ project: "demo", reloaded: false, services: services.map((service) => ({ ...service, allocated: true })) }));
  process.exit(0);
}
if (cmd === "ls") {
  console.log(JSON.stringify({ services: services.map((service) => ({ project: "demo", ...service, route: "active", upstream: "down" })) }));
  process.exit(0);
}
if (cmd === "env") {
  const service = services.find((candidate) => candidate.service === args.at(-1));
  if (!service) {
    console.error(JSON.stringify({ error: { code: "service_not_found", message: "service not found" } }));
    process.exit(2);
  }
  console.log(JSON.stringify({
    project: "demo",
    service: service.service,
    domain: service.domain,
    port: service.port,
    url: routeUrl(service),
    loopbackUrl: loopbackUrl(service),
    route: "active",
    upstream: "down",
    env: envFor(service),
    envKeys: Object.keys(envFor(service)).sort(),
    daemon: {
      required: true,
      running: false,
      listener: "listener:https-443-http-80",
      httpsAddr: ":443",
      httpAddr: ":80",
    },
    diagnostics: [{
      code: "daemon_not_running",
      severity: "fixable",
      message: "listener daemon is not running",
      suggestedCommand: "gate up --daemon",
      actions: [{ label: "Start listener daemon", command: "gate up --daemon" }],
    }],
  }));
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

async function fakeChild(dir: string, body: string): Promise<string> {
  const path = join(dir, `child-${Date.now()}-${Math.random().toString(16).slice(2)}.mjs`)
  await writeFile(path, body)
  await chmod(path, 0o755)
  return path
}

async function expectResolverWithoutUp(log: string): Promise<void> {
  const calls = await readFile(log, 'utf8')
  expect(calls).toMatch(/^__resolve-project(?: |$)/m)
  expect(calls).not.toMatch(/^up(?: |$)/m)
}

function configPaths(calls: string): string[] {
  return calls
    .trim()
    .split('\n')
    .flatMap((line) => {
      const args = line.split(' ')
      const index = args.indexOf('--config')
      return index === -1 ? [] : [args[index + 1] ?? '']
    })
}

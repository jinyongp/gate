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
  expect(paths).toHaveLength(2)
  expect(new Set(paths).size).toBe(1)
  expect(calls).toMatch(/up --json --config \S+ --dns localhost/)
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
  expect(paths).toHaveLength(4)
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
  expect(env.GATE_NODE_CACHE_DIR).toBe(join(root, 'cache', 'node'))
  expect(env.XDG_CONFIG_HOME).toBe(join(root, 'xdg', 'config'))
  expect(env.XDG_STATE_HOME).toBe(join(root, 'xdg', 'state'))
  expect(env.XDG_DATA_HOME).toBe(join(root, 'xdg', 'data'))

  const calls = await readFile(log, 'utf8')
  const paths = configPaths(calls)
  expect(paths[0]).toContain(join(root, 'cache', 'node', 'inline-config'))
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
  expect(JSON.parse(result.stdout ?? '{}')).toEqual({
    PORT: '4312',
    API_URL: 'http://127.0.0.1:4313',
    PUBLIC_API_URL: 'https://api.demo.localhost',
    GATE_API_ROUTE_URL: 'https://api.demo.localhost',
  })
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

test('default DNS preflight rejects custom inline config domains before mutating up', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })

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
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
})

test('up rejects custom inline config domains before mutating routes', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({ bin: gate, cwd: dir })

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
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
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
  expect(calls).toMatch(/up --json --config \S+ --dns localhost/)
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
  expect(calls).toMatch(/up --json --config \S+ --dns localhost/)
})

test('inline DNS preflight rejects env references that resolve to custom domains', async () => {
  const dir = await tempDir()
  const log = join(dir, 'args.log')
  const gate = await fakeGate(dir, log)
  const client = createGateClient({
    bin: gate,
    cwd: dir,
    env: { BASE_DOMAIN: 'demo.test' },
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
  await expect(readFile(log, 'utf8')).rejects.toThrow(/ENOENT/)
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
  options: {
    mode?: 'permission' | 'invalid-json' | 'hang'
    envLog?: string
    services?: Array<{ service: string; domain: string; port: number }>
  } = {},
): Promise<string> {
  const path = join(dir, 'gate-fake.mjs')
  const services = options.services ?? [
    { service: 'web', domain: 'web.demo.localhost', port: 4312 },
  ]
  const body = `#!/usr/bin/env node
import { appendFileSync, writeFileSync } from "node:fs";
const args = process.argv.slice(2);
appendFileSync(${JSON.stringify(log)}, args.join(" ") + "\\n");
if (${JSON.stringify(options.envLog)}) {
  writeFileSync(${JSON.stringify(options.envLog)}, JSON.stringify({
    GATE_NODE_CACHE_DIR: process.env.GATE_NODE_CACHE_DIR,
    XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME,
    XDG_STATE_HOME: process.env.XDG_STATE_HOME,
    XDG_DATA_HOME: process.env.XDG_DATA_HOME,
  }));
}
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
const services = ${JSON.stringify(services)};
if (cmd === "up") {
  console.log(JSON.stringify({ project: "demo", reloaded: false, services: services.map((service) => ({ ...service, allocated: true })) }));
  process.exit(0);
}
if (cmd === "ls") {
  console.log(JSON.stringify({ services: services.map((service) => ({ project: "demo", ...service, route: "active", upstream: "down" })) }));
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

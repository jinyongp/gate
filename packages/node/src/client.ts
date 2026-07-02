import { spawn } from 'node:child_process'
import { dnsArgs, parseJSON, runGate } from './command.js'
import { composeSignals } from './command.js'
import { prepareScope, serviceEnvDeclarations } from './config.js'
import { gateProcessEnv } from './environment.js'
import { GateError } from './errors.js'
import { assertDNSAllowed, assertScopeDNSAllowed } from './preflight.js'
import type {
  GateClient,
  GateClientOptions,
  GateCommandOptions,
  GateRunEnv,
  GateRunOptions,
  GateRunResult,
  GateService,
  GateServiceOptions,
  GateUpOptions,
  GateUpResult,
} from './types.js'

interface GatePortJSON {
  service: string
  port: number
}

interface GateLsJSON {
  services: Array<{
    project?: string
    service: string
    domain: string
    port: number
    route: 'active' | 'inactive'
    upstream: 'live' | 'down'
    standalone?: boolean
  }>
}

interface GateUpJSON {
  project?: string
  global?: boolean
  reloaded: boolean
  services: Array<{
    service: string
    domain: string
    port: number
    allocated?: boolean
  }>
}

export function createGateClient(defaults: GateClientOptions = {}): GateClient {
  const withDefaults = <T extends GateCommandOptions>(options?: T): T & GateCommandOptions =>
    ({
      ...defaults,
      ...options,
      env: { ...defaults.env, ...options?.env },
    }) as T & GateCommandOptions

  const up = async (options?: GateUpOptions): Promise<GateUpResult> => {
    const merged = withDefaults(options)
    await assertScopeDNSAllowed(merged)
    const scope = await prepareScope(merged.scope, merged)
    const args = ['up', '--json', ...scope.args, ...dnsArgs(merged.dns)]
    if (merged.daemon) {
      args.push('--daemon')
    }
    const result = await runGate(args, merged)
    const parsed = parseJSON<GateUpJSON>([resultCommand(merged), ...args], result.stdout)
    return {
      ...parsed,
      services: parsed.services.map((service) => enrichUpService(service)),
    }
  }

  const ls = async (options?: GateCommandOptions): Promise<GateService[]> => {
    const merged = withDefaults(options)
    const scope = await prepareScope(merged.scope, merged)
    const args = ['ls', '--json', ...scope.args]
    const result = await runGate(args, merged)
    const parsed = parseJSON<GateLsJSON>([resultCommand(merged), ...args], result.stdout)
    return parsed.services.map((service) => ({
      ...service,
      url: `https://${service.domain}`,
      loopbackUrl: `http://127.0.0.1:${service.port}`,
    }))
  }

  const envForService = async (name: string, options?: GateServiceOptions): Promise<GateRunEnv> => {
    const merged = withDefaults(options)
    const { selected, services } = await resolveServiceSet(name, merged, up, ls)
    const declarations = await serviceEnvDeclarations(merged.scope, merged)
    return buildRunEnv(selected, services, declarations)
  }

  return {
    up,

    async service(name: string, options?: GateServiceOptions): Promise<GateService> {
      const merged = withDefaults(options)
      return (await resolveServiceSet(name, merged, up, ls)).selected
    },

    env: envForService,

    async run(
      name: string,
      command: readonly string[],
      options?: GateRunOptions,
    ): Promise<GateRunResult> {
      validateCommand(command)
      const merged = withDefaults(options)
      const generatedEnv = await envForService(name, merged)
      return await runChild(command, generatedEnv, merged)
    },

    async port(service: string, options?: GateCommandOptions): Promise<number> {
      const merged = withDefaults(options)
      const scope = await prepareScope(merged.scope, merged)
      const args = ['port', '--json', ...scope.args, service]
      const result = await runGate(args, merged)
      return parseJSON<GatePortJSON>([resultCommand(merged), ...args], result.stdout).port
    },

    ls,

    async down(options?: GateCommandOptions): Promise<void> {
      const merged = withDefaults(options)
      const scope = await prepareScope(merged.scope, merged)
      const args = ['down', '--json', ...scope.args]
      await runGate(args, merged)
    },
  }
}

function enrichUpService(
  service: GateUpJSON['services'][number],
): GateUpResult['services'][number] {
  return {
    ...service,
    url: `https://${service.domain}`,
    loopbackUrl: `http://127.0.0.1:${service.port}`,
  }
}

function resultCommand(options: GateCommandOptions): string {
  return options.bin ?? options.env?.GATE_BIN ?? 'gate'
}

async function resolveServiceSet(
  name: string,
  options: GateServiceOptions,
  up: (options?: GateUpOptions) => Promise<GateUpResult>,
  ls: (options?: GateCommandOptions) => Promise<GateService[]>,
): Promise<{ selected: GateService; services: GateService[] }> {
  const serviceOptions = { ...options, dns: options.dns ?? 'localhost' }
  if (options.up ?? true) {
    await assertDNSAllowed(name, serviceOptions)
    await up(serviceOptions)
  }
  const services = await ls(serviceOptions)
  const selected = services.find((candidate) => candidate.service === name)
  if (!selected) {
    throw new GateError({
      code: 'GATE_SERVICE_NOT_FOUND',
      message: `gate service not found: ${name}`,
    })
  }
  return { selected, services }
}

function buildRunEnv(
  selected: GateService,
  services: GateService[],
  declarations: Map<string, { env: string[]; routeEnv: string[] }>,
): GateRunEnv {
  const out: GateRunEnv = { PORT: String(selected.port) }
  const byService = new Map(services.map((service) => [service.service, service]))

  for (const service of services) {
    const key = serviceEnvKey(service.service)
    const portName = `GATE_${key}_PORT`
    if (out[portName] !== undefined) {
      throw new GateError({
        code: 'GATE_COMMAND_FAILED',
        message: `duplicate gate env service key "${key}" for service "${service.service}"`,
      })
    }
    out[portName] = String(service.port)
    out[`GATE_${key}_URL`] = service.loopbackUrl
    out[`GATE_${key}_ROUTE_URL`] = service.url
  }

  for (const [serviceName, declaration] of declarations) {
    if (declaration.env.length === 0 && declaration.routeEnv.length === 0) {
      continue
    }
    const service = byService.get(serviceName)
    if (!service) {
      throw new GateError({
        code: 'GATE_COMMAND_FAILED',
        message: `service "${serviceName}" publishes env names but has no reserved port`,
      })
    }
    for (const envName of declaration.env) {
      out[envName] = service.loopbackUrl
    }
    for (const envName of declaration.routeEnv) {
      out[envName] = service.url
    }
  }

  return out
}

function serviceEnvKey(name: string): string {
  return name
    .trim()
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '_')
}

function validateCommand(command: readonly string[]): void {
  if (!Array.isArray(command) || command.length === 0) {
    throw new GateError({
      code: 'GATE_INVALID_OPTIONS',
      message: 'run command must be a non-empty argv array',
    })
  }
  if (command.some((part) => typeof part !== 'string' || part.length === 0)) {
    throw new GateError({
      code: 'GATE_INVALID_OPTIONS',
      message: 'run command argv entries must be non-empty strings',
    })
  }
}

async function runChild(
  command: readonly string[],
  generatedEnv: GateRunEnv,
  options: GateRunOptions,
): Promise<GateRunResult> {
  const stdio = options.stdio ?? 'inherit'
  const env = { ...gateProcessEnv(options), ...generatedEnv }

  return await new Promise<GateRunResult>((resolve, reject) => {
    const timeoutController = options.timeoutMs ? new AbortController() : undefined
    const signal = composeSignals(options.signal, timeoutController?.signal)
    let timedOut = false
    const timeout = options.timeoutMs
      ? setTimeout(() => {
          timedOut = true
          timeoutController?.abort()
        }, options.timeoutMs)
      : undefined
    let settled = false
    let stdout = ''
    let stderr = ''

    const finish = (callback: () => void) => {
      if (settled) {
        return
      }
      settled = true
      if (timeout) {
        clearTimeout(timeout)
      }
      callback()
    }

    const child = spawn(command[0] ?? '', command.slice(1), {
      cwd: options.cwd,
      env,
      signal,
      stdio: stdio === 'pipe' ? ['ignore', 'pipe', 'pipe'] : 'inherit',
    })

    if (stdio === 'pipe') {
      child.stdout?.setEncoding('utf8')
      child.stderr?.setEncoding('utf8')
      child.stdout?.on('data', (chunk: string) => {
        stdout += chunk
      })
      child.stderr?.on('data', (chunk: string) => {
        stderr += chunk
      })
    }

    child.on('error', (error: NodeJS.ErrnoException) => {
      finish(() => {
        reject(
          new GateError({
            code: 'GATE_COMMAND_FAILED',
            message: timedOut ? `child timed out after ${options.timeoutMs}ms` : error.message,
            command: [...command],
            stdout: stdio === 'pipe' ? stdout : undefined,
            stderr: stdio === 'pipe' ? stderr : undefined,
            cause: error,
          }),
        )
      })
    })

    child.on('close', (exitCode, signalName) => {
      finish(() => {
        if (exitCode === 0 && !signalName) {
          resolve({
            exitCode: 0,
            stdout: stdio === 'pipe' ? stdout : undefined,
            stderr: stdio === 'pipe' ? stderr : undefined,
          })
          return
        }
        reject(
          new GateError({
            code: 'GATE_COMMAND_FAILED',
            message:
              signalName === null
                ? `child exited with code ${exitCode ?? 1}`
                : `child terminated with signal ${signalName}`,
            command: [...command],
            exitCode: exitCode ?? undefined,
            signal: signalName ?? undefined,
            stdout: stdio === 'pipe' ? stdout : undefined,
            stderr: stdio === 'pipe' ? stderr : undefined,
          }),
        )
      })
    })
  })
}

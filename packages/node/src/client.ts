import { spawn } from 'node:child_process'
import { dnsArgs, parseJSON, runGate } from './command.js'
import { composeSignals } from './command.js'
import { prepareScope } from './config.js'
import { gateProcessEnv } from './environment.js'
import { GateError } from './errors.js'
import { assertDNSAllowed, assertScopeDNSAllowed } from './preflight.js'
import type {
  GateClient,
  GateClientOptions,
  GateCommandOptions,
  GateClientCallOptions,
  GateRunEnv,
  GateRunOptions,
  GateRunReady,
  GateReadyResult,
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

interface GateEnvJSON extends GateService {
  env: GateRunEnv
  envKeys?: string[]
  daemon?: GateReadyResult['daemon']
  diagnostics?: GateReadyResult['diagnostics']
}

/**
 * Create a high-level Node client backed by the gate CLI JSON contracts.
 *
 * @remarks
 * Defaults are merged into every method call. Per-call options override client
 * defaults, and per-call `env` values are merged over default `env` values.
 *
 * The client does not reimplement gate routing logic. It invokes the selected
 * gate binary, requests JSON output, and maps command failures to
 * {@link GateError}.
 *
 * @param defaults - Options applied to all client methods.
 * @returns A {@link GateClient} instance.
 *
 * @example
 * ```ts
 * import { createGateClient } from '@jinyongp/gate'
 *
 * const gate = createGateClient({ cwd: process.cwd() })
 * const web = await gate.service('web', { up: true })
 * console.log(web.url)
 * ```
 *
 * @public
 */
export function createGateClient(): GateClient<false>
export function createGateClient<const Defaults extends GateClientOptions>(
  defaults: Defaults,
): GateClient<Defaults extends { isolatedRoot: string } ? true : false>
export function createGateClient(defaults: GateClientOptions = {}): GateClient<boolean> {
  const withDefaults = <T extends GateCommandOptions>(options?: T): T & GateCommandOptions =>
    ({
      ...defaults,
      ...options,
      env: { ...defaults.env, ...options?.env },
    }) as T & GateCommandOptions

  const up = async (options?: GateUpOptions): Promise<GateUpResult> => {
    const merged = withDefaults(options)
    assertDaemonAllowed(merged, [resultCommand(merged), 'up'])
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
    assertDaemonAllowed(merged, [resultCommand(merged), 'ls'])
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

  const readyForService = async (
    name: string,
    options?: GateServiceOptions,
  ): Promise<GateReadyResult> => {
    const merged = withDefaults(options)
    assertDaemonAllowed(merged, [resultCommand(merged), 'env', name])
    const serviceOptions = { ...merged, dns: merged.dns ?? 'localhost' }
    if (merged.up ?? true) {
      await assertDNSAllowed(name, serviceOptions)
      await up(serviceOptions)
    }
    const scope = await prepareScope(serviceOptions.scope, serviceOptions)
    const args = ['env', '--json', ...scope.args, name]
    const result = await runGate(args, serviceOptions)
    const parsed = parseJSON<GateEnvJSON>([resultCommand(serviceOptions), ...args], result.stdout)
    const { env, envKeys, daemon, diagnostics, ...service } = parsed
    return {
      service,
      env,
      envKeys: envKeys ?? sortedKeys(env),
      daemon,
      diagnostics: diagnostics ?? [],
    }
  }

  const envForService = async (name: string, options?: GateServiceOptions): Promise<GateRunEnv> => {
    return (await readyForService(name, options)).env
  }

  return {
    up,

    async service(
      name: string,
      options?: GateClientCallOptions<GateServiceOptions, boolean>,
    ): Promise<GateService> {
      const merged = withDefaults(options)
      assertDaemonAllowed(merged, [resultCommand(merged), 'service', name])
      return (await resolveServiceSet(name, merged, up, ls)).selected
    },

    env: envForService,

    ready: readyForService,

    async run(
      serviceOrReady: string | GateRunReady,
      command: readonly string[],
      options?: GateClientCallOptions<GateRunOptions, boolean>,
    ): Promise<GateRunResult> {
      validateCommand(command)
      const merged = withDefaults(options)
      assertDaemonAllowed(merged, [resultCommand(merged), 'run'])
      const ready =
        typeof serviceOrReady === 'string'
          ? await readyForService(serviceOrReady, merged)
          : validateReadyResult(serviceOrReady)
      return await runChild(command, ready, merged)
    },

    async port(service: string, options?: GateCommandOptions): Promise<number> {
      const merged = withDefaults(options)
      assertDaemonAllowed(merged, [resultCommand(merged), 'port', service])
      const scope = await prepareScope(merged.scope, merged)
      const args = ['port', '--json', ...scope.args, service]
      const result = await runGate(args, merged)
      return parseJSON<GatePortJSON>([resultCommand(merged), ...args], result.stdout).port
    },

    ls,

    async down(options?: GateCommandOptions): Promise<void> {
      const merged = withDefaults(options)
      assertDaemonAllowed(merged, [resultCommand(merged), 'down'])
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

function assertDaemonAllowed(
  options: GateCommandOptions & { daemon?: boolean },
  command: string[],
): void {
  if (options.isolatedRoot && options.daemon === true) {
    throw new GateError({
      code: 'GATE_INVALID_OPTIONS',
      message:
        'isolatedRoot cannot be combined with daemon: true; use normal gate state for real dev launches, or use the CLI with explicit non-default listener addresses for isolated daemon tests',
      command,
    })
  }
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

function validateReadyResult(ready: GateRunReady): GateReadyResult {
  if (
    !ready ||
    typeof ready !== 'object' ||
    !isGateService(ready.service) ||
    !isStringRecord(ready.env) ||
    (ready.envKeys !== undefined &&
      (!isStringArray(ready.envKeys) || !sameStrings(ready.envKeys, sortedKeys(ready.env)))) ||
    (ready.daemon !== undefined && !isGateDaemonReadiness(ready.daemon)) ||
    (ready.diagnostics !== undefined && !isGateDiagnostics(ready.diagnostics))
  ) {
    throw new GateError({
      code: 'GATE_INVALID_OPTIONS',
      message: 'ready descriptor must come from gate.ready()',
    })
  }
  return {
    ...ready,
    envKeys: ready.envKeys ?? sortedKeys(ready.env),
    diagnostics: ready.diagnostics ?? [],
  }
}

function isGateService(value: unknown): value is GateService {
  if (!value || typeof value !== 'object') {
    return false
  }
  const service = value as Partial<GateService>
  return (
    typeof service.service === 'string' &&
    typeof service.domain === 'string' &&
    typeof service.port === 'number' &&
    typeof service.url === 'string' &&
    typeof service.loopbackUrl === 'string' &&
    (service.route === 'active' || service.route === 'inactive') &&
    (service.upstream === 'live' || service.upstream === 'down')
  )
}

function isStringRecord(value: unknown): value is Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false
  }
  return Object.values(value).every((entry) => typeof entry === 'string')
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((entry) => typeof entry === 'string')
}

function sortedKeys(value: Record<string, string>): string[] {
  return Object.keys(value).toSorted()
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

function isGateDaemonReadiness(value: unknown): boolean {
  if (!value || typeof value !== 'object') {
    return false
  }
  const daemon = value as {
    required?: unknown
    running?: unknown
    listener?: unknown
    httpsAddr?: unknown
    httpAddr?: unknown
  }
  return (
    typeof daemon.required === 'boolean' &&
    typeof daemon.running === 'boolean' &&
    typeof daemon.listener === 'string' &&
    (daemon.httpsAddr === undefined || typeof daemon.httpsAddr === 'string') &&
    (daemon.httpAddr === undefined || typeof daemon.httpAddr === 'string')
  )
}

function isGateDiagnostics(value: unknown): boolean {
  if (!Array.isArray(value)) {
    return false
  }
  return value.every((item) => {
    if (!item || typeof item !== 'object') {
      return false
    }
    const diagnostic = item as {
      code?: unknown
      severity?: unknown
      message?: unknown
      suggestedCommand?: unknown
      actions?: unknown
    }
    return (
      typeof diagnostic.code === 'string' &&
      isGateDiagnosticSeverity(diagnostic.severity) &&
      typeof diagnostic.message === 'string' &&
      (diagnostic.suggestedCommand === undefined ||
        typeof diagnostic.suggestedCommand === 'string') &&
      (diagnostic.actions === undefined || isGateDiagnosticActions(diagnostic.actions))
    )
  })
}

function isGateDiagnosticActions(value: unknown): boolean {
  if (!Array.isArray(value)) {
    return false
  }
  return value.every((item) => {
    if (!item || typeof item !== 'object') {
      return false
    }
    const action = item as { label?: unknown; command?: unknown }
    return (
      typeof action.label === 'string' &&
      (action.command === undefined || typeof action.command === 'string')
    )
  })
}

function isGateDiagnosticSeverity(
  value: unknown,
): value is GateReadyResult['diagnostics'][number]['severity'] {
  return (
    value === 'fatal' ||
    value === 'fixable' ||
    value === 'permission' ||
    value === 'warning' ||
    value === 'info'
  )
}

async function runChild(
  command: readonly string[],
  ready: GateReadyResult,
  options: GateRunOptions,
): Promise<GateRunResult> {
  const stdio = options.stdio ?? 'inherit'
  const env = { ...gateProcessEnv(options), ...ready.env }

  try {
    await options.onReady?.(ready)
  } catch (cause) {
    throw new GateError({
      code: 'GATE_COMMAND_FAILED',
      message: 'run onReady failed',
      command: [...command],
      cause,
    })
  }

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
            service: ready.service,
            env: ready.env,
            envKeys: ready.envKeys,
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

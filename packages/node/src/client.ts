import { dnsArgs, parseJSON, runGate } from './command.js'
import { prepareScope } from './config.js'
import { GateError } from './errors.js'
import { assertDNSAllowed, assertScopeDNSAllowed } from './preflight.js'
import type {
  GateClient,
  GateClientOptions,
  GateCommandOptions,
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

  return {
    up,

    async service(name: string, options?: GateServiceOptions): Promise<GateService> {
      const merged = withDefaults(options)
      const serviceOptions = { ...merged, dns: merged.dns ?? 'localhost' }
      if (merged.up ?? true) {
        await assertDNSAllowed(name, serviceOptions)
        await up(serviceOptions)
      }
      const services = await ls(serviceOptions)
      const service = services.find((candidate) => candidate.service === name)
      if (!service) {
        throw new GateError({
          code: 'GATE_SERVICE_NOT_FOUND',
          message: `gate service not found: ${name}`,
        })
      }
      return service
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

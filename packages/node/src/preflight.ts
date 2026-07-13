import { parseJSON, runGate } from './command.js'
import type { PreparedScope } from './config.js'
import { prepareScope } from './config.js'
import { GateError } from './errors.js'
import type { GateCommandOptions, GateDNSMode } from './types.js'

interface ResolvedScopeJSON {
  scope: 'project' | 'global'
  project?: string
  services: Array<{
    service: string
    domain: string
  }>
}

export async function assertScopeDNSAllowed(
  options: GateCommandOptions & { dns?: GateDNSMode },
  preparedScope?: PreparedScope,
): Promise<void> {
  const dns = options.dns ?? 'localhost'
  if (dns === 'hosts' || dns === 'preconfigured') {
    return
  }

  const scope = preparedScope ?? (await prepareScope(options.scope, options))
  const args = ['__resolve-project', ...scope.args]
  const result = await runGate(args, options)
  const resolved = parseJSON<ResolvedScopeJSON>(result.command, result.stdout, isResolvedScopeJSON)
  if (resolved.scope === 'global' && options.dns === undefined) {
    return
  }
  for (const service of resolved.services) {
    if (!isLocalhostDomain(service.domain)) {
      throw new GateError({
        code: 'GATE_DNS_REQUIRED',
        message: `service "${service.service}" resolves to "${service.domain}", which requires explicit dns: "hosts" or dns: "preconfigured"`,
        command: result.command,
      })
    }
  }
}

function isResolvedScopeJSON(value: unknown): value is ResolvedScopeJSON {
  if (!value || typeof value !== 'object') {
    return false
  }
  const resolved = value as { scope?: unknown; project?: unknown; services?: unknown }
  return (
    (resolved.scope === 'project' || resolved.scope === 'global') &&
    (resolved.project === undefined || typeof resolved.project === 'string') &&
    Array.isArray(resolved.services) &&
    resolved.services.every((service) => {
      if (!service || typeof service !== 'object') {
        return false
      }
      const item = service as { service?: unknown; domain?: unknown }
      return typeof item.service === 'string' && typeof item.domain === 'string'
    })
  )
}

function isLocalhostDomain(domain: string): boolean {
  return domain.trim().toLowerCase().endsWith('.localhost')
}

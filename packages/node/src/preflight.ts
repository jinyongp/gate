import { access, readFile } from 'node:fs/promises'
import { dirname, parse, resolve } from 'node:path'
import type { GateCommandOptions, GateDNSMode } from './types.js'
import { GateError } from './errors.js'

interface ProjectConfig {
  base?: string
  services: Map<string, { domain?: string; host?: string }>
}

export async function assertDNSAllowed(
  service: string,
  options: GateCommandOptions & { dns?: GateDNSMode },
): Promise<void> {
  const dns = options.dns ?? 'localhost'
  if (dns === 'hosts' || dns === 'preconfigured') {
    return
  }
  if (options.scope?.kind === 'global') {
    return
  }

  const configPath = await resolveConfigPath(options)
  const config = await loadProjectConfig(configPath)
  const domain = resolveServiceDomain(config, service)
  if (domain && !domain.endsWith('.localhost')) {
    throw new GateError({
      code: 'GATE_DNS_REQUIRED',
      message: `service "${service}" resolves to "${domain}", which requires explicit dns: "hosts" or dns: "preconfigured"`,
    })
  }
}

async function resolveConfigPath(options: GateCommandOptions): Promise<string> {
  const cwd = options.cwd ?? process.cwd()
  if (options.scope?.kind === 'project' && options.scope.config) {
    return resolve(cwd, options.scope.config)
  }

  const found = await findUp('gate.toml', cwd)
  if (found) {
    return found
  }

  throw new GateError({
    code: 'GATE_COMMAND_FAILED',
    message: `gate.toml not found from ${cwd}`,
  })
}

async function findUp(fileName: string, start: string): Promise<string | undefined> {
  let current = resolve(start)
  const { root } = parse(current)

  while (true) {
    const candidate = resolve(current, fileName)
    try {
      await access(candidate)
      return candidate
    } catch {
      // Keep walking upward.
    }
    if (current === root) {
      return undefined
    }
    current = dirname(current)
  }
}

async function loadProjectConfig(path: string): Promise<ProjectConfig> {
  const body = await readFile(path, 'utf8')
  return parseProjectConfig(body)
}

export function parseProjectConfig(body: string): ProjectConfig {
  const services = new Map<string, { domain?: string; host?: string }>()
  const config: ProjectConfig = { services }
  let section: { kind: 'project' } | { kind: 'service'; name: string } | undefined

  for (const rawLine of body.split(/\r?\n/)) {
    const line = stripComment(rawLine).trim()
    if (!line) {
      continue
    }
    const sectionMatch = line.match(/^\[([^\]]+)\]$/)
    if (sectionMatch) {
      const name = sectionMatch[1] ?? ''
      if (name === 'project') {
        section = { kind: 'project' }
      } else if (name.startsWith('services.')) {
        const serviceName = name.slice('services.'.length).replace(/^['"]|['"]$/g, '')
        section = { kind: 'service', name: serviceName }
        if (!services.has(serviceName)) {
          services.set(serviceName, {})
        }
      } else {
        section = undefined
      }
      continue
    }

    const keyValue = line.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(['"])(.*)\2$/)
    if (!keyValue || !section) {
      continue
    }
    const key = keyValue[1]
    const quote = keyValue[2]
    const value = keyValue[3]
    if (!key || value === undefined) {
      continue
    }
    const parsedValue = quote === "'" ? value : unescapeBasicString(value)
    if (section.kind === 'project' && key === 'base') {
      config.base = parsedValue
      continue
    }
    if (section.kind === 'service') {
      const service = services.get(section.name) ?? {}
      if (key === 'domain') {
        service.domain = parsedValue
      }
      if (key === 'host') {
        service.host = parsedValue
      }
      services.set(section.name, service)
    }
  }

  return config
}

export function resolveServiceDomain(
  config: ProjectConfig,
  serviceName: string,
): string | undefined {
  const service = config.services.get(serviceName)
  if (!service) {
    return undefined
  }
  if (service.domain) {
    return service.domain.toLowerCase()
  }
  if (!config.base) {
    return undefined
  }
  if (service.host === '.') {
    return config.base.toLowerCase()
  }
  return `${service.host || serviceName}.${config.base}`.toLowerCase()
}

function stripComment(line: string): string {
  let quote: "'" | '"' | undefined
  for (let index = 0; index < line.length; index += 1) {
    const char = line[index]
    if ((char === "'" || char === '"') && line[index - 1] !== '\\') {
      quote = quote === char ? undefined : (quote ?? char)
    }
    if (char === '#' && !quote) {
      return line.slice(0, index)
    }
  }
  return line
}

function unescapeBasicString(value: string): string {
  return value.replace(/\\(["\\])/g, '$1')
}

export function configDirectory(configPath: string): string {
  return dirname(resolve(configPath))
}

import { access, readFile } from 'node:fs/promises'
import { dirname, parse, resolve } from 'node:path'
import type { GateCommandOptions, GateDNSMode } from './types.js'
import {
  inlineConfigToPreflightProject,
  isInlineProjectConfig,
  validateInlineProjectScope,
} from './config.js'
import { GateError, isGateError } from './errors.js'

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

  const config = await resolveProjectConfig(options)
  const domain = resolveServiceDomain(config, service)
  if (domain && !isLocalhostDomain(domain)) {
    throw new GateError({
      code: 'GATE_DNS_REQUIRED',
      message: `service "${service}" resolves to "${domain}", which requires explicit dns: "hosts" or dns: "preconfigured"`,
    })
  }
}

export async function assertScopeDNSAllowed(
  options: GateCommandOptions & { dns?: GateDNSMode },
): Promise<void> {
  const dns = options.dns ?? 'localhost'
  if (dns === 'hosts' || dns === 'preconfigured') {
    return
  }
  if (options.scope?.kind === 'global') {
    return
  }

  const config = await resolveProjectConfigForScopePreflight(options)
  for (const [service] of config.services) {
    const domain = resolveServiceDomain(config, service)
    if (domain && !isLocalhostDomain(domain)) {
      throw new GateError({
        code: 'GATE_DNS_REQUIRED',
        message: `service "${service}" resolves to "${domain}", which requires explicit dns: "hosts" or dns: "preconfigured"`,
      })
    }
  }
}

async function resolveProjectConfigForScopePreflight(
  options: GateCommandOptions,
): Promise<ProjectConfig> {
  try {
    return await resolveProjectConfig(options)
  } catch (error) {
    if (isGateError(error, 'GATE_COMMAND_FAILED') && !hasExplicitConfig(options)) {
      return { services: new Map() }
    }
    throw error
  }
}

function hasExplicitConfig(options: GateCommandOptions): boolean {
  return Boolean(options.scope && 'config' in options.scope && options.scope.config)
}

async function resolveProjectConfig(options: GateCommandOptions): Promise<ProjectConfig> {
  if (options.scope?.kind !== 'global' && isInlineProjectConfig(options.scope?.config)) {
    validateInlineProjectScope(options.scope.project, options.scope.config)
    return expandProjectConfig(inlineConfigToPreflightProject(options.scope.config), options)
  }

  const configPath = await resolveConfigPath(options)
  return await loadProjectConfig(configPath)
}

async function resolveConfigPath(options: GateCommandOptions): Promise<string> {
  const cwd = options.cwd ?? process.cwd()
  if (options.scope?.kind !== 'global' && typeof options.scope?.config === 'string') {
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

function expandProjectConfig(config: ProjectConfig, options: GateCommandOptions): ProjectConfig {
  const env = { ...process.env, ...options.env }
  const services = new Map<string, { domain?: string; host?: string }>()
  for (const [name, service] of config.services) {
    services.set(name, {
      domain:
        service.domain === undefined
          ? undefined
          : expandEnvRefs(service.domain, env, `service "${name}" domain`),
      host:
        service.host === undefined
          ? undefined
          : expandEnvRefs(service.host, env, `service "${name}" host`),
    })
  }
  return {
    base: config.base === undefined ? undefined : expandEnvRefs(config.base, env, 'project base'),
    services,
  }
}

function expandEnvRefs(value: string, env: NodeJS.ProcessEnv, context: string): string {
  let out = ''
  let rest = value
  while (true) {
    const start = rest.indexOf('${')
    if (start === -1) {
      return out + rest
    }
    out += rest.slice(0, start)
    const afterStart = rest.slice(start + 2)
    const end = afterStart.indexOf('}')
    if (end === -1) {
      throw invalidOptions(`${context}: unterminated env reference`)
    }
    out += expandEnvRef(afterStart.slice(0, end), env, context)
    rest = afterStart.slice(end + 1)
  }
}

function expandEnvRef(expr: string, env: NodeJS.ProcessEnv, context: string): string {
  const [key, ...fallbackParts] = expr.split(':-')
  if (!key || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
    throw invalidOptions(`${context}: invalid env reference "${expr}"`)
  }
  const value = env[key]
  if (fallbackParts.length > 0 && !value) {
    return fallbackParts.join(':-')
  }
  if (value === undefined) {
    throw invalidOptions(`${context}: env ${key} is not set`)
  }
  return value
}

function isLocalhostDomain(domain: string): boolean {
  return domain.trim().toLowerCase().endsWith('.localhost')
}

function invalidOptions(message: string): GateError {
  return new GateError({ code: 'GATE_INVALID_OPTIONS', message })
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

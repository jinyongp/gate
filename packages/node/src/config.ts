import { createHash, randomUUID } from 'node:crypto'
import { mkdir, rename, writeFile } from 'node:fs/promises'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { GateError } from './errors.js'
import type {
  GateCommandOptions,
  GateInlineProjectConfig,
  GateInlineServiceConfig,
  GateScope,
} from './types.js'

export interface PreparedScope {
  args: string[]
  inlineConfigPath?: string
}

export async function prepareScope(
  scope: GateScope | undefined,
  options: GateCommandOptions = {},
): Promise<PreparedScope> {
  if (!scope) {
    return { args: [] }
  }
  if (scope.kind === 'global') {
    validateGlobalScope(scope)
    return { args: ['--global'] }
  }

  const args: string[] = []
  const config = scope.config
  if (isInlineProjectConfig(config)) {
    validateInlineProjectScope(scope.project, config)
    if (scope.project) {
      args.push('--project', scope.project)
    }
    const inlineConfigPath = await materializeInlineProjectConfig(config, options)
    args.push('--config', inlineConfigPath)
    return { args, inlineConfigPath }
  }

  if (scope.project) {
    args.push('--project', scope.project)
  }
  if (typeof config === 'string') {
    args.push('--config', config)
  }
  return { args }
}

export async function materializeInlineProjectConfig(
  config: GateInlineProjectConfig,
  options: GateCommandOptions = {},
): Promise<string> {
  validateInlineProjectConfig(config)
  const cwd = resolve(options.cwd ?? process.cwd())
  const body = inlineProjectConfigToToml(config)
  const cacheRoot = resolveInlineConfigCacheRoot(options)
  const digest = createHash('sha256')
    .update(cwd)
    .update('\0')
    .update(stableStringify(config))
    .digest('hex')
    .slice(0, 32)
  const path = join(cacheRoot, `${digest}.toml`)
  await mkdir(dirname(path), { recursive: true })
  const tempPath = join(cacheRoot, `.${digest}.${process.pid}.${randomUUID()}.tmp`)
  await writeFile(tempPath, body, { mode: 0o600 })
  await rename(tempPath, path)
  return path
}

export function inlineProjectConfigToToml(config: GateInlineProjectConfig): string {
  validateInlineProjectConfig(config)

  const lines = ['[project]', `name = ${tomlString(config.name)}`]
  if (config.base !== undefined) {
    lines.push(`base = ${tomlString(config.base)}`)
  }

  for (const serviceName of Object.keys(config.services).toSorted()) {
    const service = config.services[serviceName]
    if (!service) {
      continue
    }
    lines.push('', `[services.${tomlKey(serviceName)}]`)
    if (service.domain !== undefined) {
      lines.push(`domain = ${tomlString(service.domain)}`)
    }
    if (service.host !== undefined) {
      lines.push(`host = ${tomlString(service.host)}`)
    }
    if (service.port !== undefined) {
      lines.push(`port = ${tomlPort(service.port)}`)
    }
    if (service.env !== undefined) {
      lines.push(`env = ${tomlEnv(service.env)}`)
    }
    if (service.routeEnv !== undefined) {
      lines.push(`route_env = ${tomlEnv(service.routeEnv)}`)
    }
  }

  return `${lines.join('\n')}\n`
}

export function validateInlineProjectScope(
  project: string | undefined,
  config: GateInlineProjectConfig,
): void {
  validateInlineProjectConfig(config)
  if (project !== undefined && project !== config.name) {
    throw invalidOptions(
      `scope.project "${project}" must match inline project config name "${config.name}"`,
    )
  }
}

export function isInlineProjectConfig(value: unknown): value is GateInlineProjectConfig {
  return isRecord(value)
}

export function inlineConfigToPreflightProject(config: GateInlineProjectConfig): {
  base?: string
  services: Map<string, { domain?: string; host?: string }>
} {
  validateInlineProjectConfig(config)
  const services = new Map<string, { domain?: string; host?: string }>()
  for (const [name, service] of Object.entries(config.services)) {
    services.set(name, { domain: service.domain, host: service.host })
  }
  return { base: config.base, services }
}

function validateGlobalScope(scope: GateScope): void {
  const candidate = scope as { config?: unknown; project?: unknown }
  if (candidate.config !== undefined) {
    throw invalidOptions('scope.config cannot be combined with global scope')
  }
  if (candidate.project !== undefined) {
    throw invalidOptions('scope.project cannot be combined with global scope')
  }
}

function validateInlineProjectConfig(config: GateInlineProjectConfig): void {
  if (!isRecord(config)) {
    throw invalidOptions('inline scope.config must be an object')
  }
  assertKnownKeys(config, ['name', 'base', 'services'], 'inline scope.config')
  if (!isNonEmptyString(config.name)) {
    throw invalidOptions('inline scope.config.name must be a non-empty string')
  }
  if (config.base !== undefined && typeof config.base !== 'string') {
    throw invalidOptions('inline scope.config.base must be a string')
  }
  if (!isRecord(config.services)) {
    throw invalidOptions('inline scope.config.services must be an object')
  }

  for (const [serviceName, service] of Object.entries(config.services)) {
    if (!isNonEmptyString(serviceName)) {
      throw invalidOptions('inline scope.config service names must be non-empty strings')
    }
    validateInlineServiceConfig(serviceName, service)
  }
}

function validateInlineServiceConfig(serviceName: string, service: unknown): void {
  if (!isRecord(service)) {
    throw invalidOptions(`inline scope.config.services.${serviceName} must be an object`)
  }
  assertKnownKeys(
    service,
    ['domain', 'host', 'port', 'env', 'routeEnv'],
    `inline scope.config.services.${serviceName}`,
  )
  const config = service as GateInlineServiceConfig
  if (config.domain !== undefined && typeof config.domain !== 'string') {
    throw invalidOptions(`inline scope.config.services.${serviceName}.domain must be a string`)
  }
  if (config.host !== undefined && typeof config.host !== 'string') {
    throw invalidOptions(`inline scope.config.services.${serviceName}.host must be a string`)
  }
  if (
    config.port !== undefined &&
    typeof config.port !== 'number' &&
    typeof config.port !== 'string'
  ) {
    throw invalidOptions(
      `inline scope.config.services.${serviceName}.port must be a number or string`,
    )
  }
  if (typeof config.port === 'number' && !Number.isInteger(config.port)) {
    throw invalidOptions(`inline scope.config.services.${serviceName}.port must be an integer`)
  }
  if (config.env !== undefined) {
    validateServiceEnv(serviceName, config.env)
  }
  if (config.routeEnv !== undefined) {
    validateServiceEnv(serviceName, config.routeEnv, 'routeEnv')
  }
}

function validateServiceEnv(serviceName: string, env: string | string[], field = 'env'): void {
  if (typeof env === 'string') {
    return
  }
  if (!Array.isArray(env) || env.some((value) => typeof value !== 'string')) {
    throw invalidOptions(
      `inline scope.config.services.${serviceName}.${field} must be a string array`,
    )
  }
}

function tomlKey(value: string): string {
  return /^[A-Za-z0-9_-]+$/.test(value) ? value : tomlString(value)
}

function tomlString(value: string): string {
  return JSON.stringify(value)
}

function tomlPort(value: number | string): string {
  return typeof value === 'number' ? String(value) : tomlString(value)
}

function tomlEnv(value: string | string[]): string {
  if (typeof value === 'string') {
    return tomlString(value)
  }
  return `[${value.map((item) => tomlString(item)).join(', ')}]`
}

function stableStringify(value: unknown): string {
  if (!isRecord(value)) {
    return JSON.stringify(value)
  }
  const entries = Object.entries(value).toSorted(([left], [right]) => left.localeCompare(right))
  return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${stableStringify(item)}`).join(',')}}`
}

function resolveInlineConfigCacheRoot(options: GateCommandOptions): string {
  const env = { ...process.env, ...options.env }
  if (env.GATE_NODE_CACHE_DIR) {
    return resolve(env.GATE_NODE_CACHE_DIR, 'inline-config')
  }
  if (process.platform === 'darwin') {
    return join(homedir(), 'Library', 'Caches', 'gate', 'node', 'inline-config')
  }
  if (env.XDG_CACHE_HOME) {
    return resolve(env.XDG_CACHE_HOME, 'gate', 'node', 'inline-config')
  }
  const home = homedir()
  if (home) {
    return join(home, '.cache', 'gate', 'node', 'inline-config')
  }
  return join(tmpdir(), 'gate', 'node', 'inline-config')
}

function invalidOptions(message: string): GateError {
  return new GateError({ code: 'GATE_INVALID_OPTIONS', message })
}

function assertKnownKeys(
  value: Record<string, unknown>,
  allowedKeys: string[],
  label: string,
): void {
  const allowed = new Set(allowedKeys)
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw invalidOptions(`${label}.${key} is not supported`)
    }
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0
}

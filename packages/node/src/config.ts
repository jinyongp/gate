import { createHash, randomUUID } from 'node:crypto'
import { access, mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { GateError } from './errors.js'
import { gateOptionEnv } from './environment.js'
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

export interface ServiceEnvDeclaration {
  env: string[]
  routeEnv: string[]
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

export async function serviceEnvDeclarations(
  scope: GateScope | undefined,
  options: GateCommandOptions = {},
): Promise<Map<string, ServiceEnvDeclaration>> {
  if (scope?.kind === 'global') {
    return new Map()
  }
  if (isInlineProjectConfig(scope?.config)) {
    validateInlineProjectScope(scope.project, scope.config)
    return inlineServiceEnvDeclarations(scope.config)
  }

  const path = await resolveDeclarationConfigPath(scope, options)
  if (!path) {
    return new Map()
  }
  const body = await readFile(path, 'utf8')
  if (scope?.project && parseProjectName(body) !== scope.project) {
    return new Map()
  }
  return parseServiceEnvDeclarations(body)
}

function parseProjectName(body: string): string | undefined {
  let inProjectSection = false
  for (const rawLine of body.split(/\r?\n/)) {
    const line = stripTomlComment(rawLine).trim()
    if (!line) {
      continue
    }
    const sectionMatch = line.match(/^\[([^\]]+)\]$/)
    if (sectionMatch) {
      inProjectSection = sectionMatch[1] === 'project'
      continue
    }
    if (!inProjectSection) {
      continue
    }
    const keyValue = line.match(/^name\s*=\s*(.+)$/)
    const value = keyValue?.[1]?.trim()
    if (value) {
      return parseTomlStringList(value)[0]
    }
  }
  return undefined
}

export function parseServiceEnvDeclarations(body: string): Map<string, ServiceEnvDeclaration> {
  const declarations = new Map<string, ServiceEnvDeclaration>()
  let serviceName: string | undefined

  for (const rawLine of body.split(/\r?\n/)) {
    const line = stripTomlComment(rawLine).trim()
    if (!line) {
      continue
    }
    const sectionMatch = line.match(/^\[([^\]]+)\]$/)
    if (sectionMatch) {
      const sectionName = sectionMatch[1] ?? ''
      if (sectionName.startsWith('services.')) {
        serviceName = sectionName.slice('services.'.length).replace(/^['"]|['"]$/g, '')
        ensureServiceEnvDeclaration(declarations, serviceName)
      } else {
        serviceName = undefined
      }
      continue
    }
    if (!serviceName) {
      continue
    }

    const keyValue = line.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$/)
    if (!keyValue) {
      continue
    }
    const key = keyValue[1]
    const value = keyValue[2]?.trim()
    if (!value) {
      continue
    }
    const declaration = ensureServiceEnvDeclaration(declarations, serviceName)
    if (key === 'env') {
      declaration.env = parseTomlStringList(value)
    }
    if (key === 'route_env') {
      declaration.routeEnv = parseTomlStringList(value)
    }
  }

  return declarations
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

function inlineServiceEnvDeclarations(
  config: GateInlineProjectConfig,
): Map<string, ServiceEnvDeclaration> {
  validateInlineProjectConfig(config)
  const declarations = new Map<string, ServiceEnvDeclaration>()
  for (const [serviceName, service] of Object.entries(config.services)) {
    declarations.set(serviceName, {
      env: normalizeEnvList(service.env),
      routeEnv: normalizeEnvList(service.routeEnv),
    })
  }
  return declarations
}

async function resolveDeclarationConfigPath(
  scope: GateScope | undefined,
  options: GateCommandOptions,
): Promise<string | undefined> {
  const cwd = resolve(options.cwd ?? process.cwd())
  if (scope?.kind !== 'global' && typeof scope?.config === 'string') {
    return resolve(cwd, scope.config)
  }
  return await findUpFile('gate.toml', cwd)
}

async function findUpFile(fileName: string, start: string): Promise<string | undefined> {
  let current = resolve(start)

  while (true) {
    const candidate = resolve(current, fileName)
    try {
      await access(candidate)
      return candidate
    } catch {
      // Keep walking upward.
    }
    const parent = dirname(current)
    if (parent === current) {
      return undefined
    }
    current = parent
  }
}

function ensureServiceEnvDeclaration(
  declarations: Map<string, ServiceEnvDeclaration>,
  serviceName: string,
): ServiceEnvDeclaration {
  const existing = declarations.get(serviceName)
  if (existing) {
    return existing
  }
  const declaration = { env: [], routeEnv: [] }
  declarations.set(serviceName, declaration)
  return declaration
}

function normalizeEnvList(value: string | string[] | undefined): string[] {
  if (value === undefined) {
    return []
  }
  return typeof value === 'string' ? [value] : value
}

function parseTomlStringList(value: string): string[] {
  if (value.startsWith('[')) {
    const out: string[] = []
    const pattern = /'([^']*)'|"((?:\\.|[^"\\])*)"/g
    for (const match of value.matchAll(pattern)) {
      out.push(match[1] ?? unescapeTomlBasicString(match[2] ?? ''))
    }
    return out
  }
  if (value.startsWith("'") && value.endsWith("'")) {
    return [value.slice(1, -1)]
  }
  if (value.startsWith('"') && value.endsWith('"')) {
    return [unescapeTomlBasicString(value.slice(1, -1))]
  }
  return []
}

function stripTomlComment(line: string): string {
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

function unescapeTomlBasicString(value: string): string {
  return value.replace(/\\(["\\])/g, '$1')
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
  const env = { ...process.env, ...gateOptionEnv(options) }
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

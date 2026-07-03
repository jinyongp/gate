export interface GateInlineServiceConfig {
  domain?: string
  host?: string
  port?: number | string
  env?: string | string[]
  routeEnv?: string | string[]
}

export interface GateInlineProjectConfig {
  name: string
  base?: string
  services: Record<string, GateInlineServiceConfig>
}

export type GateScope =
  | { kind?: 'project'; project?: string; config?: string | GateInlineProjectConfig }
  | { kind: 'global' }

export type GateDNSMode = 'localhost' | 'hosts' | 'preconfigured'

export interface GateClientOptions {
  bin?: string
  cwd?: string
  env?: NodeJS.ProcessEnv
  isolatedRoot?: string
  signal?: AbortSignal
  timeoutMs?: number
}

export interface GateCommandOptions extends GateClientOptions {
  scope?: GateScope
}

export interface GateService {
  service: string
  project?: string
  standalone?: boolean
  domain: string
  port: number
  url: string
  loopbackUrl: string
  route: 'active' | 'inactive'
  upstream: 'live' | 'down'
}

export interface GateUpOptions extends GateCommandOptions {
  daemon?: boolean
  dns?: GateDNSMode
}

export interface GateUpResult {
  project?: string
  global?: boolean
  reloaded: boolean
  services: Array<{
    service: string
    domain: string
    port: number
    url: string
    loopbackUrl: string
    allocated?: boolean
  }>
}

export interface GateServiceOptions extends GateUpOptions {
  up?: boolean
}

export interface GateClient {
  up(options?: GateUpOptions): Promise<GateUpResult>
  service(name: string, options?: GateServiceOptions): Promise<GateService>
  env(service: string, options?: GateServiceOptions): Promise<GateRunEnv>
  run(service: string, command: readonly string[], options?: GateRunOptions): Promise<GateRunResult>
  port(service: string, options?: GateCommandOptions): Promise<number>
  ls(options?: GateCommandOptions): Promise<GateService[]>
  down(options?: GateCommandOptions): Promise<void>
}

export type GateRunEnv = Record<string, string>

export interface GateRunReady {
  service: GateService
  env: GateRunEnv
}

export interface GateRunOptions extends GateServiceOptions {
  stdio?: 'inherit' | 'pipe'
  onReady?: (ready: GateRunReady) => void | Promise<void>
}

export interface GateRunResult {
  exitCode: number
  signal?: NodeJS.Signals
  stdout?: string
  stderr?: string
  service?: GateService
  env?: GateRunEnv
}

export type GateErrorCode =
  | 'GATE_BINARY_NOT_FOUND'
  | 'GATE_COMMAND_FAILED'
  | 'GATE_DNS_REQUIRED'
  | 'GATE_INVALID_OPTIONS'
  | 'GATE_JSON_PARSE_FAILED'
  | 'GATE_PERMISSION_REQUIRED'
  | 'GATE_SERVICE_NOT_FOUND'
  | 'GATE_UNSUPPORTED_PLATFORM'

export interface GateErrorDetails {
  code: GateErrorCode
  message: string
  command?: string[]
  gateCode?: string
  exitCode?: number
  signal?: NodeJS.Signals
  stdout?: string
  stderr?: string
  cause?: unknown
}

export type GateScope = { kind?: 'project'; project?: string; config?: string } | { kind: 'global' }

export type GateDNSMode = 'localhost' | 'hosts' | 'preconfigured'

export interface GateClientOptions {
  bin?: string
  cwd?: string
  env?: NodeJS.ProcessEnv
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
  port(service: string, options?: GateCommandOptions): Promise<number>
  ls(options?: GateCommandOptions): Promise<GateService[]>
  down(options?: GateCommandOptions): Promise<void>
}

export type GateErrorCode =
  | 'GATE_BINARY_NOT_FOUND'
  | 'GATE_COMMAND_FAILED'
  | 'GATE_DNS_REQUIRED'
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
  stdout?: string
  stderr?: string
  cause?: unknown
}

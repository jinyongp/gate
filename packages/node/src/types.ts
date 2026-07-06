/**
 * Inline declaration for one `[services.<name>]` entry in a generated
 * `gate.toml`.
 *
 * @remarks
 * Use inline service config when automation should call gate with a
 * project-scoped config without writing `gate.toml` into the repository.
 *
 * `env` receives loopback URLs such as `http://127.0.0.1:<port>`.
 * `routeEnv` receives browser-visible gate route URLs such as
 * `https://api.myapp.localhost`.
 *
 * @public
 */
export interface GateInlineServiceConfig {
  /**
   * Fully qualified route domain for the service.
   *
   * @remarks
   * When set, this value is used directly instead of deriving the domain from
   * `host` and the project `base`.
   *
   * @example
   * `"api.myapp.localhost"`
   */
  domain?: string

  /**
   * Host label used with the project `base` to derive the route domain.
   *
   * @remarks
   * For a project `base` of `myapp.localhost`, `host: "api"` resolves to
   * `api.myapp.localhost`.
   */
  host?: string

  /**
   * Fixed service port, or an environment reference accepted by `gate.toml`.
   *
   * @remarks
   * Omit this field to let gate allocate a stable free port for the service.
   *
   * @example
   * `3001`
   *
   * @example
   * `"${API_PORT}"`
   */
  port?: number | string

  /**
   * Environment variable name or names that receive the loopback URL.
   *
   * @remarks
   * Values injected by {@link GateClient.env}, {@link GateClient.ready}, and
   * {@link GateClient.run} use `http://127.0.0.1:<port>`.
   */
  env?: string | string[]

  /**
   * Environment variable name or names that receive the gate route URL.
   *
   * @remarks
   * Use framework-public names here when browser code needs the URL, for
   * example `VITE_API_BASE_URL`, `NEXT_PUBLIC_API_BASE_URL`, or
   * `NUXT_PUBLIC_API_BASE_URL`.
   */
  routeEnv?: string | string[]
}

/**
 * Inline project config materialized to a temporary TOML file before invoking
 * the gate binary.
 *
 * @remarks
 * This mirrors the public `gate.toml` shape for the Node API. It is useful for
 * agents, tests, and tooling that need deterministic project-scoped behavior
 * without editing the user's checkout.
 *
 * @public
 */
export interface GateInlineProjectConfig {
  /** Project name used for project-scoped reservations. */
  name: string

  /**
   * Default domain suffix used to derive service domains.
   *
   * @example
   * `"myapp.localhost"`
   */
  base?: string

  /** Service declarations keyed by service name. */
  services: Record<string, GateInlineServiceConfig>
}

/**
 * Scope selector for commands that can target project or global gate state.
 *
 * @remarks
 * The default scope follows normal gate CLI behavior: discover `gate.toml`
 * from `cwd` and operate on that project. Use `{ kind: "global" }` for
 * standalone machine-wide reservations. Use `config` to point at a specific
 * TOML file or pass a {@link GateInlineProjectConfig}.
 *
 * When `config` is inline and `project` is also set, `project` must match
 * `config.name`.
 *
 * @public
 */
export type GateScope =
  | {
      /**
       * Project scope marker.
       *
       * @defaultValue `"project"`
       */
      kind?: 'project'

      /** Explicit project name. Defaults to the discovered or inline project. */
      project?: string

      /** Config file path or inline project config for this scope. */
      config?: string | GateInlineProjectConfig
    }
  | {
      /** Machine-wide standalone reservation scope. */
      kind: 'global'
    }

/**
 * DNS handling mode for commands that may activate routes.
 *
 * @remarks
 * `localhost` only allows domains that already resolve through the special
 * `.localhost` behavior. `hosts` allows gate to edit the system hosts file.
 * `preconfigured` tells gate DNS is already handled elsewhere.
 *
 * @public
 */
export type GateDNSMode = 'localhost' | 'hosts' | 'preconfigured'

/**
 * Shared options for all Node API calls.
 *
 * @public
 */
export interface GateClientOptions {
  /**
   * Explicit path to the gate binary.
   *
   * @remarks
   * Takes precedence over `GATE_BIN` and the package-provided platform binary.
   */
  bin?: string

  /**
   * Working directory for gate subprocesses and project discovery.
   *
   * @defaultValue `process.cwd()`
   */
  cwd?: string

  /**
   * Extra environment for gate subprocesses.
   *
   * @remarks
   * Values are merged over `process.env`. Per-call `env` values override
   * defaults passed to {@link createGateClient}.
   */
  env?: NodeJS.ProcessEnv

  /**
   * Workspace-local root for isolated gate state.
   *
   * @remarks
   * Sets the gate isolation environment used for generated inline config,
   * registry locks, daemon state, trust material, and cache files. Prefer this
   * for agent inspection, tests, and sandboxed tooling. Omit it for normal dev
   * launches that should share the user's gate registry and trusted CA.
   */
  isolatedRoot?: string

  /** Abort signal applied to gate subprocesses. */
  signal?: AbortSignal

  /** Maximum runtime for each gate subprocess, in milliseconds. */
  timeoutMs?: number
}

/**
 * Options for commands that accept a scope.
 *
 * @public
 */
export interface GateCommandOptions extends GateClientOptions {
  /** Project or global scope used for the command. */
  scope?: GateScope
}

/**
 * Selected service descriptor returned by gate.
 *
 * @remarks
 * This is the stable automation shape for a service route: service identity,
 * assigned port, route URL, loopback URL, and live status.
 *
 * @public
 */
export interface GateService {
  /** Service name from `gate.toml`, inline config, or global reservation. */
  service: string

  /** Project name when the service belongs to a project scope. */
  project?: string

  /** Whether this service is a standalone global reservation. */
  standalone?: boolean

  /** Route domain handled by the gate proxy. */
  domain: string

  /** Local port reserved for the service. */
  port: number

  /** HTTPS gate route URL for browser/client traffic. */
  url: string

  /** Direct loopback URL for local process-to-process traffic. */
  loopbackUrl: string

  /** Whether the route is currently active in the daemon route table. */
  route: 'active' | 'inactive'

  /** Whether gate can connect to the upstream service port. */
  upstream: 'live' | 'down'
}

/**
 * Options for {@link GateClient.up}.
 *
 * @public
 */
export interface GateUpOptions extends GateCommandOptions {
  /**
   * Start or reload the listener daemon after reserving routes.
   *
   * @defaultValue `false`
   */
  daemon?: boolean

  /**
   * DNS mode used when route activation requires DNS changes.
   *
   * @defaultValue `"localhost"` for APIs that activate a single service.
   */
  dns?: GateDNSMode
}

/**
 * Result from activating routes with {@link GateClient.up}.
 *
 * @public
 */
export interface GateUpResult {
  /** Project name for project-scoped activation. */
  project?: string

  /** True when the global scope was targeted. */
  global?: boolean

  /** Whether a running daemon accepted a route-table reload. */
  reloaded: boolean

  /** Services reserved or refreshed by the operation. */
  services: Array<{
    /** Service name. */
    service: string

    /** Route domain handled by gate. */
    domain: string

    /** Local reserved port. */
    port: number

    /** HTTPS gate route URL. */
    url: string

    /** Direct loopback URL. */
    loopbackUrl: string

    /** True when gate allocated the port during this call. */
    allocated?: boolean
  }>
}

/**
 * Options for APIs that resolve one service.
 *
 * @public
 */
export interface GateServiceOptions extends GateUpOptions {
  /**
   * Reserve and activate routes before resolving the service.
   *
   * @defaultValue `true`
   */
  up?: boolean
}

/**
 * High-level Node client for the gate CLI JSON contracts.
 *
 * @remarks
 * Methods call the gate binary and parse stable JSON output. Data goes through
 * stdout; diagnostics and structured errors are surfaced through
 * {@link GateError}.
 *
 * @public
 */
export interface GateClient {
  /**
   * Reserve project or global routes, optionally starting/reloading the daemon.
   */
  up(options?: GateUpOptions): Promise<GateUpResult>

  /**
   * Resolve one service descriptor.
   *
   * @remarks
   * By default this may call `gate up --json` first, because
   * {@link GateServiceOptions.up} defaults to `true`.
   */
  service(name: string, options?: GateServiceOptions): Promise<GateService>

  /**
   * Return the environment variables gate would inject for a service process.
   *
   * @remarks
   * This includes `PORT`, peer `GATE_<SERVICE>_*` values, and service-declared
   * `env` / `routeEnv` values from the selected config.
   */
  env(service: string, options?: GateServiceOptions): Promise<GateRunEnv>

  /**
   * Resolve service, environment, daemon readiness, and diagnostics as one
   * descriptor.
   *
   * @remarks
   * Use this when another runner needs to inspect route/env readiness before it
   * starts a child process.
   */
  ready(service: string, options?: GateServiceOptions): Promise<GateReadyResult>

  /**
   * Resolve readiness, then spawn a child command with gate environment
   * injected.
   *
   * @throws
   * Throws a {@link GateError} when options are invalid, gate fails, or the
   * child exits non-zero or terminates by signal. Captured stdout/stderr are
   * attached only when `stdio: "pipe"` is used.
   */
  run(service: string, command: readonly string[], options?: GateRunOptions): Promise<GateRunResult>

  /**
   * Spawn a child command from an existing readiness snapshot.
   *
   * @remarks
   * Passing a snapshot from {@link GateClient.ready} avoids resolving the
   * descriptor a second time.
   */
  run(
    ready: GateRunReady,
    command: readonly string[],
    options?: GateRunOptions,
  ): Promise<GateRunResult>

  /** Return the reserved port for a service. */
  port(service: string, options?: GateCommandOptions): Promise<number>

  /** List service descriptors visible in the selected scope. */
  ls(options?: GateCommandOptions): Promise<GateService[]>

  /** Deactivate routes in the selected scope while preserving reservations. */
  down(options?: GateCommandOptions): Promise<void>
}

/**
 * Environment variables injected into a service process.
 *
 * @public
 */
export type GateRunEnv = Record<string, string>

/**
 * Daemon readiness data included in {@link GateReadyResult}.
 *
 * @public
 */
export interface GateDaemonReadiness {
  /** Whether the selected route requires the daemon for browser-visible access. */
  required: boolean

  /** Whether the matching listener daemon is currently responding. */
  running: boolean

  /** Stable listener key for the daemon that owns the route table. */
  listener: string

  /** HTTPS bind address reported by the daemon. */
  httpsAddr?: string

  /** HTTP redirect bind address reported by the daemon. */
  httpAddr?: string
}

/**
 * Machine-readable diagnostic produced while resolving service readiness.
 *
 * @remarks
 * Diagnostic schemas are stable and additive, but callers should not depend on
 * any specific diagnostic always being present.
 *
 * @public
 */
export interface GateDiagnostic {
  /** Stable diagnostic code from the gate binary. */
  code: string

  /** Severity bucket for automation decisions. */
  severity: 'fatal' | 'fixable' | 'permission' | 'warning' | 'info'

  /** Human-readable diagnostic message. */
  message: string

  /** Legacy single suggested command, when one can be encoded safely. */
  suggestedCommand?: string

  /** Structured remediation actions. */
  actions?: GateDiagnosticAction[]
}

/**
 * Remediation action attached to a {@link GateDiagnostic}.
 *
 * @public
 */
export interface GateDiagnosticAction {
  /** Human-readable action label. */
  label: string

  /** Optional command to run. Omitted when scope cannot be encoded safely. */
  command?: string
}

/**
 * Full route/env readiness descriptor for one service.
 *
 * @public
 */
export interface GateReadyResult {
  /** Selected service descriptor. */
  service: GateService

  /** Environment variables to merge into the service process. */
  env: GateRunEnv

  /** Stable sorted keys present in {@link GateReadyResult.env}. */
  envKeys?: string[]

  /** Daemon readiness, when the gate binary reports it. */
  daemon?: GateDaemonReadiness

  /** Readiness diagnostics. Empty when no issues were reported. */
  diagnostics: GateDiagnostic[]
}

/**
 * Readiness snapshot accepted by {@link GateClient.run}.
 *
 * @remarks
 * This is intentionally slightly looser than {@link GateReadyResult}; callers
 * may pass a saved descriptor and omit diagnostics.
 *
 * @public
 */
export interface GateRunReady {
  /** Selected service descriptor. */
  service: GateService

  /** Environment variables to inject into the child process. */
  env: GateRunEnv

  /** Stable sorted keys present in {@link GateRunReady.env}. */
  envKeys?: string[]

  /** Daemon readiness, when available. */
  daemon?: GateDaemonReadiness

  /** Optional readiness diagnostics preserved from {@link GateReadyResult}. */
  diagnostics?: GateDiagnostic[]
}

/**
 * Options for {@link GateClient.run}.
 *
 * @public
 */
export interface GateRunOptions extends GateServiceOptions {
  /**
   * Child stdio mode.
   *
   * @remarks
   * `inherit` streams child output to the parent process. `pipe` captures
   * stdout/stderr and includes them in {@link GateRunResult} or {@link GateError}.
   *
   * @defaultValue `"inherit"`
   */
  stdio?: 'inherit' | 'pipe'

  /**
   * Hook called after route/env readiness resolves and before the child starts.
   *
   * @remarks
   * If the hook throws or rejects, the child command is not spawned.
   */
  onReady?: (ready: GateReadyResult) => void | Promise<void>
}

/**
 * Result from a successful {@link GateClient.run} child process.
 *
 * @public
 */
export interface GateRunResult {
  /** Child process exit code. */
  exitCode: number

  /** Signal that terminated the child, when applicable. */
  signal?: NodeJS.Signals

  /** Captured stdout when `stdio: "pipe"` is used. */
  stdout?: string

  /** Captured stderr when `stdio: "pipe"` is used. */
  stderr?: string

  /** Service descriptor used for the run. */
  service?: GateService

  /** Environment injected into the child. */
  env?: GateRunEnv

  /** Stable sorted keys present in {@link GateRunResult.env}. */
  envKeys?: string[]
}

/**
 * Stable Node API error code.
 *
 * @public
 */
export type GateErrorCode =
  | 'GATE_BINARY_NOT_FOUND'
  | 'GATE_COMMAND_FAILED'
  | 'GATE_DNS_REQUIRED'
  | 'GATE_INVALID_OPTIONS'
  | 'GATE_JSON_PARSE_FAILED'
  | 'GATE_PERMISSION_REQUIRED'
  | 'GATE_SERVICE_NOT_FOUND'
  | 'GATE_UNSUPPORTED_PLATFORM'

/**
 * Constructor details for {@link GateError}.
 *
 * @public
 */
export interface GateErrorDetails {
  /** Stable Node API error code. */
  code: GateErrorCode

  /** Human-readable error message. */
  message: string

  /** Command argv that failed, including the resolved gate binary name. */
  command?: string[]

  /** Gate CLI JSON error code, when the binary emitted one. */
  gateCode?: string

  /** Gate CLI JSON severity, when available. */
  severity?: string

  /** Whether the gate CLI marked the operation retryable. */
  retryable?: boolean

  /** Human-readable recovery hint from the gate CLI. */
  hint?: string

  /** Structured recovery actions from the gate CLI. */
  nextActions?: GateErrorNextAction[]

  /** Process exit code, when a subprocess exited normally. */
  exitCode?: number

  /** Process signal, when a subprocess was terminated by signal. */
  signal?: NodeJS.Signals

  /** Captured stdout, usually only when output was piped. */
  stdout?: string

  /** Captured stderr, usually only when output was piped or JSON error parsing ran. */
  stderr?: string

  /** Original cause for error chaining. */
  cause?: unknown
}

/**
 * Recovery action attached to a {@link GateError}.
 *
 * @public
 */
export interface GateErrorNextAction {
  /** Human-readable action label. */
  label: string

  /** Optional shell command. Omitted when gate cannot encode it safely. */
  command?: string
}

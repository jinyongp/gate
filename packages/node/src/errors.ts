import type { GateErrorCode, GateErrorDetails } from './types.js'

const gateErrorCodes = new Set<GateErrorCode>([
  'GATE_BINARY_NOT_FOUND',
  'GATE_COMMAND_FAILED',
  'GATE_DNS_REQUIRED',
  'GATE_INVALID_OPTIONS',
  'GATE_JSON_PARSE_FAILED',
  'GATE_PERMISSION_REQUIRED',
  'GATE_SERVICE_NOT_FOUND',
  'GATE_UNSUPPORTED_PLATFORM',
])

/**
 * Error type thrown by the Node API for gate command and option failures.
 *
 * @remarks
 * `code` is the stable Node API category. When the gate binary emits a JSON
 * error envelope, fields such as `gateCode`, `severity`, `retryable`, `hint`,
 * and `nextActions` preserve that metadata for automation.
 *
 * @public
 */
export class GateError extends Error {
  /** Stable Node API error code. */
  readonly code: GateErrorCode

  /** Command argv that failed, including the resolved gate binary name. */
  readonly command: string[]

  /** Gate CLI JSON error code, when available. */
  readonly gateCode?: string

  /** Gate CLI JSON severity, when available. */
  readonly severity?: string

  /** Whether the gate CLI marked the operation retryable. */
  readonly retryable?: boolean

  /** Human-readable recovery hint from the gate CLI. */
  readonly hint?: string

  /** Structured recovery actions from the gate CLI. */
  readonly nextActions: GateErrorDetails['nextActions']

  /** Process exit code, when a subprocess exited normally. */
  readonly exitCode?: number

  /** Process signal, when a subprocess was terminated by signal. */
  readonly signal?: NodeJS.Signals

  /** Captured stdout, usually only when output was piped. */
  readonly stdout?: string

  /** Captured stderr, usually only when output was piped or JSON error parsing ran. */
  readonly stderr?: string

  /** Create a gate Node API error from structured details. */
  constructor(details: GateErrorDetails) {
    super(details.message, details.cause === undefined ? undefined : { cause: details.cause })
    this.name = 'GateError'
    this.code = details.code
    this.command = details.command ?? []
    this.gateCode = details.gateCode
    this.severity = details.severity
    this.retryable = details.retryable
    this.hint = details.hint
    this.nextActions = details.nextActions ?? []
    this.exitCode = details.exitCode
    this.signal = details.signal
    this.stdout = details.stdout
    this.stderr = details.stderr
  }
}

/**
 * {@link GateError} narrowed to a specific stable code.
 *
 * @public
 */
export type GateErrorWithCode<Code extends GateErrorCode> = GateError & {
  readonly code: Code
}

/**
 * Test whether an unknown value is a {@link GateError}.
 *
 * @remarks
 * Pass a code to narrow the result to {@link GateErrorWithCode}. The check also
 * accepts structurally complete GateError objects, which keeps narrowing useful
 * across package copies or process boundaries without trusting partial lookalikes.
 *
 * @param error - Value caught from `try`/`catch`.
 * @param code - Optional stable Node API error code to match.
 *
 * @example
 * ```ts
 * import { createGateClient, isGateError } from '@jinyongp/gate'
 *
 * const gate = createGateClient()
 *
 * try {
 *   await gate.service('web')
 * } catch (error) {
 *   if (isGateError(error, 'GATE_DNS_REQUIRED')) {
 *     console.error(error.hint)
 *   }
 *   throw error
 * }
 * ```
 *
 * @public
 */
export function isGateError(error: unknown): error is GateError
export function isGateError<Code extends GateErrorCode>(
  error: unknown,
  code: Code,
): error is GateErrorWithCode<Code>
export function isGateError<Code extends GateErrorCode>(
  error: unknown,
  code?: Code,
): error is GateError | GateErrorWithCode<Code> {
  if (!isGateErrorLike(error)) {
    return false
  }
  return code === undefined || error.code === code
}

function isGateErrorLike(error: unknown): error is GateError {
  if (error instanceof GateError) {
    return true
  }
  if (!error || typeof error !== 'object') {
    return false
  }
  const candidate = error as Record<string, unknown>
  return (
    candidate.name === 'GateError' &&
    typeof candidate.code === 'string' &&
    gateErrorCodes.has(candidate.code as GateErrorCode) &&
    typeof candidate.message === 'string' &&
    isStringArray(candidate.command) &&
    isNextActionArray(candidate.nextActions) &&
    optionalType(candidate.gateCode, 'string') &&
    optionalType(candidate.severity, 'string') &&
    optionalType(candidate.retryable, 'boolean') &&
    optionalType(candidate.hint, 'string') &&
    optionalType(candidate.exitCode, 'number') &&
    optionalType(candidate.signal, 'string') &&
    optionalType(candidate.stdout, 'string') &&
    optionalType(candidate.stderr, 'string')
  )
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
}

function isNextActionArray(value: unknown): value is GateErrorDetails['nextActions'] {
  return (
    Array.isArray(value) &&
    value.every(
      (item) =>
        item !== null &&
        typeof item === 'object' &&
        typeof (item as { label?: unknown }).label === 'string' &&
        optionalType((item as { command?: unknown }).command, 'string'),
    )
  )
}

function optionalType(value: unknown, type: 'boolean' | 'number' | 'string'): boolean {
  return value === undefined || typeof value === type
}

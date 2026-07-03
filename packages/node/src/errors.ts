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

export class GateError extends Error {
  readonly code: GateErrorCode
  readonly command: string[]
  readonly gateCode?: string
  readonly severity?: string
  readonly retryable?: boolean
  readonly hint?: string
  readonly nextActions: GateErrorDetails['nextActions']
  readonly exitCode?: number
  readonly signal?: NodeJS.Signals
  readonly stdout?: string
  readonly stderr?: string

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

export type GateErrorWithCode<Code extends GateErrorCode> = GateError & {
  readonly code: Code
}

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
  const candidate = error as { name?: unknown; code?: unknown }
  return (
    candidate.name === 'GateError' &&
    typeof candidate.code === 'string' &&
    gateErrorCodes.has(candidate.code as GateErrorCode)
  )
}

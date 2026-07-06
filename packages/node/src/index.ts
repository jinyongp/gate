export { createGateClient } from './client.js'
export { GateError, isGateError } from './errors.js'
export type { GateErrorWithCode } from './errors.js'
export { resolveGateBinary } from './binary.js'
export type { BinaryResolutionOptions } from './binary.js'
export type {
  GateClient,
  GateClientOptions,
  GateCommandOptions,
  GateDaemonReadiness,
  GateDiagnostic,
  GateDiagnosticAction,
  GateDNSMode,
  GateErrorCode,
  GateErrorDetails,
  GateErrorNextAction,
  GateInlineProjectConfig,
  GateInlineServiceConfig,
  GateRunEnv,
  GateRunOptions,
  GateRunReady,
  GateRunResult,
  GateScope,
  GateReadyResult,
  GateService,
  GateServiceOptions,
  GateUpOptions,
  GateUpResult,
} from './types.js'

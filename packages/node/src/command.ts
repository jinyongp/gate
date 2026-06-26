import { spawn } from 'node:child_process'
import { GateError } from './errors.js'
import type { GateCommandOptions, GateDNSMode } from './types.js'
import { resolveGateBinary } from './binary.js'

export interface CommandResult {
  stdout: string
  stderr: string
}

export function dnsArgs(dns?: GateDNSMode): string[] {
  if (!dns) {
    return []
  }
  return ['--dns', dns === 'preconfigured' ? 'localhost' : dns]
}

export async function runGate(
  args: string[],
  options: GateCommandOptions = {},
): Promise<CommandResult> {
  const bin = resolveGateBinary(options)
  const command = [bin, ...args]
  const env = { ...process.env, ...options.env }

  return await new Promise<CommandResult>((resolve, reject) => {
    const timeoutController = options.timeoutMs ? new AbortController() : undefined
    const signal = composeSignals(options.signal, timeoutController?.signal)
    const timeout = options.timeoutMs
      ? setTimeout(() => timeoutController?.abort(), options.timeoutMs)
      : undefined
    let settled = false

    const finish = (callback: () => void) => {
      if (settled) {
        return
      }
      settled = true
      if (timeout) {
        clearTimeout(timeout)
      }
      callback()
    }

    const child = spawn(bin, args, {
      cwd: options.cwd,
      env,
      signal,
      stdio: ['ignore', 'pipe', 'pipe'],
    })

    let stdout = ''
    let stderr = ''

    child.stdout.setEncoding('utf8')
    child.stderr.setEncoding('utf8')
    child.stdout.on('data', (chunk: string) => {
      stdout += chunk
    })
    child.stderr.on('data', (chunk: string) => {
      stderr += chunk
    })
    child.on('error', (error: NodeJS.ErrnoException) => {
      finish(() => {
        const code = error.code === 'ENOENT' ? 'GATE_BINARY_NOT_FOUND' : 'GATE_COMMAND_FAILED'
        reject(
          new GateError({ code, message: error.message, command, stdout, stderr, cause: error }),
        )
      })
    })
    child.on('close', (exitCode) => {
      finish(() => {
        if (exitCode === 0) {
          resolve({ stdout, stderr })
          return
        }
        reject(errorFromGateFailure(command, exitCode ?? 1, stdout, stderr))
      })
    })
  })
}

export function parseJSON<T>(command: string[], stdout: string): T {
  try {
    return JSON.parse(stdout) as T
  } catch (cause) {
    throw new GateError({
      code: 'GATE_JSON_PARSE_FAILED',
      message: 'gate returned invalid JSON',
      command,
      stdout,
      cause,
    })
  }
}

function errorFromGateFailure(
  command: string[],
  exitCode: number,
  stdout: string,
  stderr: string,
): GateError {
  const gateEnvelope = parseGateError(stderr)
  return new GateError({
    code: exitCode === 3 ? 'GATE_PERMISSION_REQUIRED' : 'GATE_COMMAND_FAILED',
    message: gateEnvelope?.message ?? (stderr.trim() || `gate exited with code ${exitCode}`),
    command,
    gateCode: gateEnvelope?.code,
    exitCode,
    stdout,
    stderr,
  })
}

function parseGateError(stderr: string): { code?: string; message?: string } | undefined {
  try {
    const parsed = JSON.parse(stderr) as { error?: { code?: string; message?: string } }
    return parsed.error
  } catch {
    return undefined
  }
}

function composeSignals(primary?: AbortSignal, secondary?: AbortSignal): AbortSignal | undefined {
  if (!primary) {
    return secondary
  }
  if (!secondary) {
    return primary
  }
  return AbortSignal.any([primary, secondary])
}

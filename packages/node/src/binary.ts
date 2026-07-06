import { createRequire } from 'node:module'
import type { GateClientOptions } from './types.js'
import { GateError } from './errors.js'

const require = createRequire(import.meta.url)

const binaryPackages: Record<string, string> = {
  'darwin:arm64': '@jinyongp/gate-darwin-arm64/bin/gate',
  'darwin:x64': '@jinyongp/gate-darwin-x64/bin/gate',
  'linux:arm64': '@jinyongp/gate-linux-arm64/bin/gate',
  'linux:x64': '@jinyongp/gate-linux-x64/bin/gate',
}

/**
 * Options for resolving the packaged gate binary.
 *
 * @remarks
 * `platform`, `arch`, and `resolvePackage` are primarily for tests and custom
 * launchers. Normal callers usually pass no options, an explicit `bin`, or an
 * `env` containing `GATE_BIN`.
 *
 * @public
 */
export interface BinaryResolutionOptions extends GateClientOptions {
  /** Platform key used to select the optional binary package. */
  platform?: NodeJS.Platform

  /** Architecture key used to select the optional binary package. */
  arch?: string

  /** Custom resolver for platform package specifiers. */
  resolvePackage?: (specifier: string) => string
}

/**
 * Resolve the gate executable path for the current process.
 *
 * @remarks
 * Resolution order:
 *
 * 1. `options.bin`
 * 2. `options.env.GATE_BIN`
 * 3. `process.env.GATE_BIN`
 * 4. package-provided optional binary for the current platform/architecture
 *
 * @throws
 * Throws a {@link GateError} with `GATE_BINARY_NOT_FOUND` when the expected
 * optional binary package is not installed.
 *
 * @throws
 * Throws a {@link GateError} with `GATE_UNSUPPORTED_PLATFORM` for unsupported
 * platforms or architectures.
 *
 * @example
 * ```ts
 * import { createGateClient, resolveGateBinary } from '@jinyongp/gate'
 *
 * const bin = resolveGateBinary()
 * const gate = createGateClient({ bin })
 * ```
 *
 * @public
 */
export function resolveGateBinary(options: BinaryResolutionOptions = {}): string {
  if (options.bin) {
    return options.bin
  }
  const envBin = options.env?.GATE_BIN ?? process.env.GATE_BIN
  if (envBin) {
    return envBin
  }

  const platform = options.platform ?? process.platform
  const arch = options.arch ?? process.arch
  const packagePath = binaryPackages[`${platform}:${arch}`]
  if (packagePath) {
    try {
      return (options.resolvePackage ?? require.resolve)(packagePath)
    } catch (cause) {
      throw new GateError({
        code: 'GATE_BINARY_NOT_FOUND',
        message: `gate binary package is not installed for ${platform}/${arch}`,
        cause,
      })
    }
  }

  if (platform === 'darwin' || platform === 'linux') {
    throw new GateError({
      code: 'GATE_UNSUPPORTED_PLATFORM',
      message: `unsupported gate binary architecture: ${platform}/${arch}`,
    })
  }

  throw new GateError({
    code: 'GATE_UNSUPPORTED_PLATFORM',
    message: `unsupported gate binary platform: ${platform}/${arch}`,
  })
}

import { createRequire } from "node:module";
import type { GateClientOptions } from "./types.js";
import { GateError } from "./errors.js";

const require = createRequire(import.meta.url);

const binaryPackages: Record<string, string> = {
  "darwin:arm64": "@gate/binary-darwin-arm64/bin/gate",
  "darwin:x64": "@gate/binary-darwin-x64/bin/gate",
  "linux:arm64": "@gate/binary-linux-arm64/bin/gate",
  "linux:x64": "@gate/binary-linux-x64/bin/gate"
};

export interface BinaryResolutionOptions extends GateClientOptions {
  platform?: NodeJS.Platform;
  arch?: string;
  resolvePackage?: (specifier: string) => string;
}

export function resolveGateBinary(options: BinaryResolutionOptions = {}): string {
  if (options.bin) {
    return options.bin;
  }
  const envBin = options.env?.GATE_BIN ?? process.env.GATE_BIN;
  if (envBin) {
    return envBin;
  }

  const platform = options.platform ?? process.platform;
  const arch = options.arch ?? process.arch;
  const packagePath = binaryPackages[`${platform}:${arch}`];
  if (packagePath) {
    try {
      return (options.resolvePackage ?? require.resolve)(packagePath);
    } catch (cause) {
      throw new GateError({
        code: "GATE_BINARY_NOT_FOUND",
        message: `gate binary package is not installed for ${platform}/${arch}`,
        cause
      });
    }
  }

  if (platform === "darwin" || platform === "linux") {
    throw new GateError({
      code: "GATE_UNSUPPORTED_PLATFORM",
      message: `unsupported gate binary architecture: ${platform}/${arch}`
    });
  }

  throw new GateError({
    code: "GATE_UNSUPPORTED_PLATFORM",
    message: `unsupported gate binary platform: ${platform}/${arch}`
  });
}

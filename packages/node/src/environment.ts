import { join, resolve } from 'node:path'
import type { GateClientOptions } from './types.js'

export function gateProcessEnv(options: GateClientOptions = {}): NodeJS.ProcessEnv {
  return {
    ...process.env,
    ...options.env,
    ...isolatedRootEnv(options),
  }
}

export function gateOptionEnv(options: GateClientOptions = {}): NodeJS.ProcessEnv {
  return {
    ...options.env,
    ...isolatedRootEnv(options),
  }
}

function isolatedRootEnv(options: GateClientOptions): NodeJS.ProcessEnv {
  const root = resolveIsolatedRoot(options)
  if (!root) {
    return {}
  }
  return {
    GATE_ISOLATED_ROOT: root,
    GATE_NODE_CACHE_DIR: join(root, 'cache', 'node'),
    XDG_CONFIG_HOME: join(root, 'xdg', 'config'),
    XDG_STATE_HOME: join(root, 'xdg', 'state'),
    XDG_DATA_HOME: join(root, 'xdg', 'data'),
  }
}

function resolveIsolatedRoot(options: GateClientOptions): string | undefined {
  if (!options.isolatedRoot) {
    return undefined
  }
  return resolve(options.cwd ?? process.cwd(), options.isolatedRoot)
}

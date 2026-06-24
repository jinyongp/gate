#!/usr/bin/env node
import { spawn } from 'node:child_process'
import { accessSync, chmodSync, constants } from 'node:fs'
import { resolveGateBinary } from '../dist/index.mjs'

const bin = resolveGateBinary()

if (bin.includes('/')) {
  try {
    accessSync(bin, constants.X_OK)
  } catch {
    chmodSync(bin, 0o755)
  }
}

const child = spawn(bin, process.argv.slice(2), {
  env: process.env,
  stdio: 'inherit',
})

child.on('error', (error) => {
  console.error(error.message)
  process.exit(error.code === 'ENOENT' ? 127 : 1)
})

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal)
    return
  }
  process.exit(code ?? 1)
})

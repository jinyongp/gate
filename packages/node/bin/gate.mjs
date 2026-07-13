#!/usr/bin/env node
import { spawn } from 'node:child_process'
import { resolveGateBinary } from '../dist/index.mjs'

const bin = resolveGateBinary()

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

import { createGateClient, type GateUpOptions } from './index.js'

const normal = createGateClient()

normal.up({ daemon: true })
normal.up({ isolatedRoot: '.gate-agent', daemon: false })

// @ts-expect-error isolated per-call options cannot start the daemon
normal.up({ isolatedRoot: '.gate-agent', daemon: true })

const isolated = createGateClient({ isolatedRoot: '.gate-agent' })

isolated.up()
isolated.up({ daemon: false })
isolated.service('web', { up: false, daemon: false })
isolated.ready('web', { up: false, daemon: false })
isolated.env('web', { up: false, daemon: false })

// @ts-expect-error isolated client options cannot start the daemon
isolated.up({ daemon: true })

// @ts-expect-error isolated service options cannot start the daemon
isolated.service('web', { up: false, daemon: true })

// Predeclared values can still use the broad public option type; runtime guards
// cover dynamically constructed or intentionally widened inputs.
const widened: GateUpOptions = { isolatedRoot: '.gate-agent', daemon: true }
void widened

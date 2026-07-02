import { createGateClient } from '@jinyongp/gate'

const gate = createGateClient({ cwd: import.meta.dirname, isolatedRoot: '.gate-agent' })
const web = await gate.service('web', { up: true })

if (!Number.isInteger(web.port) || web.port <= 0) {
  throw new Error(`invalid reserved port: ${web.port}`)
}

const env = await gate.env('web')
assertEqual(env.PORT, String(web.port), 'PORT')
assertMatch(env.API_URL, /^http:\/\/127\.0\.0\.1:\d+$/, 'API_URL')
assertMatch(env.PUBLIC_API_URL, /^https:\/\/api\.node-service-basic\.localhost$/, 'PUBLIC_API_URL')
assertEqual(env.GATE_API_ROUTE_URL, env.PUBLIC_API_URL, 'GATE_API_ROUTE_URL')

const runResult = await gate.run(
  'web',
  [
    process.execPath,
    '-e',
    `console.log(JSON.stringify({
      port: process.env.PORT,
      apiUrl: process.env.API_URL,
      publicApiUrl: process.env.PUBLIC_API_URL,
      gateApiRouteUrl: process.env.GATE_API_ROUTE_URL,
      xdgConfigHome: process.env.XDG_CONFIG_HOME
    }))`,
  ],
  { stdio: 'pipe' },
)
const childEnv = JSON.parse(runResult.stdout)
assertEqual(childEnv.port, env.PORT, 'child PORT')
assertEqual(childEnv.apiUrl, env.API_URL, 'child API_URL')
assertEqual(childEnv.publicApiUrl, env.PUBLIC_API_URL, 'child PUBLIC_API_URL')
assertEqual(childEnv.gateApiRouteUrl, env.GATE_API_ROUTE_URL, 'child GATE_API_ROUTE_URL')
assertMatch(childEnv.xdgConfigHome, /\/\.gate-agent\/xdg\/config$/, 'child XDG_CONFIG_HOME')

console.log(
  JSON.stringify({
    service: web.service,
    port: web.port,
    url: web.url,
    loopbackUrl: web.loopbackUrl,
    apiUrl: env.API_URL,
    publicApiUrl: env.PUBLIC_API_URL,
  }),
)

function assertEqual(actual, expected, label) {
  if (actual !== expected) {
    throw new Error(`${label}: got ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}`)
  }
}

function assertMatch(actual, pattern, label) {
  if (typeof actual !== 'string' || !pattern.test(actual)) {
    throw new Error(`${label}: got ${JSON.stringify(actual)}, want ${pattern}`)
  }
}

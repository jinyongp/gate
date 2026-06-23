import { chmod, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { test } from "node:test";
import assert from "node:assert/strict";
import { createGateClient, GateError, isGateError, resolveGateBinary } from "./index.js";
import { parseProjectConfig, resolveServiceDomain } from "./preflight.js";

test("resolveGateBinary prefers explicit bin, env, then package", () => {
  assert.equal(resolveGateBinary({ bin: "/tmp/gate" }), "/tmp/gate");
  assert.equal(resolveGateBinary({ env: { GATE_BIN: "/env/gate" } }), "/env/gate");
  assert.equal(
    resolveGateBinary({
      platform: "darwin",
      arch: "arm64",
      resolvePackage: (specifier) => `/resolved/${specifier}`
    }),
    "/resolved/@gate/binary-darwin-arm64/bin/gate"
  );
});

test("resolveGateBinary reports missing platform package instead of falling back to PATH", () => {
  assert.throws(
    () =>
      resolveGateBinary({
        platform: "darwin",
        arch: "arm64",
        resolvePackage: () => {
          throw new Error("missing optional dep");
        }
      }),
    (error) => isGateError(error, "GATE_BINARY_NOT_FOUND")
  );
});

test("resolveGateBinary reports unsupported platform", () => {
  assert.throws(
    () => resolveGateBinary({ platform: "win32", arch: "x64" }),
    (error) => isGateError(error, "GATE_UNSUPPORTED_PLATFORM")
  );
});

test("client service runs up then reads service metadata", async () => {
  const dir = await tempDir();
  const log = join(dir, "args.log");
  const gate = await fakeGate(dir, log);
  await writeFile(join(dir, "gate.toml"), `[project]\nname = "demo"\nbase = "demo.localhost"\n\n[services.web]\n`);

  const client = createGateClient({ bin: gate, cwd: dir });
  const service = await client.service("web");

  assert.equal(service.service, "web");
  assert.equal(service.port, 4312);
  assert.equal(service.url, "https://web.demo.localhost");
  assert.equal(service.loopbackUrl, "http://127.0.0.1:4312");

  const calls = await readFile(log, "utf8");
  assert.match(calls, /up --json --dns localhost/);
  assert.match(calls, /ls --json/);
});

test("client service method is safe to destructure", async () => {
  const dir = await tempDir();
  const gate = await fakeGate(dir, join(dir, "args.log"));
  await writeFile(join(dir, "gate.toml"), `[project]\nname = "demo"\nbase = "demo.localhost"\n\n[services.web]\n`);

  const { service } = createGateClient({ bin: gate, cwd: dir });
  const web = await service("web");

  assert.equal(web.port, 4312);
});

test("client maps permission failures", async () => {
  const dir = await tempDir();
  const gate = await fakeGate(dir, join(dir, "args.log"), { mode: "permission" });
  const client = createGateClient({ bin: gate, cwd: dir });

  await assert.rejects(
    () => client.up(),
    (error) =>
      isGateError(error, "GATE_PERMISSION_REQUIRED") &&
      error.gateCode === "permission" &&
      error.exitCode === 3
  );
});

test("client maps invalid JSON", async () => {
  const dir = await tempDir();
  const gate = await fakeGate(dir, join(dir, "args.log"), { mode: "invalid-json" });
  const client = createGateClient({ bin: gate, cwd: dir });

  await assert.rejects(
    () => client.ls(),
    (error) => isGateError(error, "GATE_JSON_PARSE_FAILED")
  );
});

test("default DNS preflight rejects custom domains before mutating up", async () => {
  const dir = await tempDir();
  const log = join(dir, "args.log");
  const gate = await fakeGate(dir, log);
  await writeFile(join(dir, "gate.toml"), `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`);
  const client = createGateClient({ bin: gate, cwd: dir });

  await assert.rejects(
    () => client.service("web"),
    (error) => isGateError(error, "GATE_DNS_REQUIRED")
  );
  await assert.rejects(() => readFile(log, "utf8"), /ENOENT/);
});

test("default DNS preflight rejects single-quoted custom domains before mutating up", async () => {
  const dir = await tempDir();
  const log = join(dir, "args.log");
  const gate = await fakeGate(dir, log);
  await writeFile(join(dir, "gate.toml"), `[project]\nname = "demo"\nbase = "demo.localhost"\n\n[services.web]\ndomain = 'web.demo.test'\n`);
  const client = createGateClient({ bin: gate, cwd: dir });

  await assert.rejects(
    () => client.service("web"),
    (error) => isGateError(error, "GATE_DNS_REQUIRED")
  );
  await assert.rejects(() => readFile(log, "utf8"), /ENOENT/);
});

test("default DNS preflight discovers parent gate config", async () => {
  const dir = await tempDir();
  const nested = join(dir, "app", "web");
  await mkdir(nested, { recursive: true });
  const log = join(dir, "args.log");
  const gate = await fakeGate(dir, log);
  await writeFile(join(dir, "gate.toml"), `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`);
  const client = createGateClient({ bin: gate, cwd: nested });

  await assert.rejects(
    () => client.service("web"),
    (error) => isGateError(error, "GATE_DNS_REQUIRED")
  );
  await assert.rejects(() => readFile(log, "utf8"), /ENOENT/);
});

test("isGateError narrows by code", () => {
  const error = new GateError({ code: "GATE_DNS_REQUIRED", message: "dns required" });

  assert.equal(isGateError(error), true);
  assert.equal(isGateError(error, "GATE_DNS_REQUIRED"), true);
  assert.equal(isGateError(error, "GATE_PERMISSION_REQUIRED"), false);
  assert.equal(isGateError({ name: "GateError", code: "GATE_DNS_REQUIRED" }, "GATE_DNS_REQUIRED"), true);
  assert.equal(isGateError({ name: "GateError", code: "NOPE" }), false);
});

test("preconfigured DNS bypasses custom-domain guard", async () => {
  const dir = await tempDir();
  const log = join(dir, "args.log");
  const gate = await fakeGate(dir, log);
  await writeFile(join(dir, "gate.toml"), `[project]\nname = "demo"\nbase = "demo.test"\n\n[services.web]\n`);
  const client = createGateClient({ bin: gate, cwd: dir });

  await client.service("web", { dns: "preconfigured" });

  const calls = await readFile(log, "utf8");
  assert.match(calls, /up --json --dns localhost/);
});

test("timeout still aborts when caller passes a signal", async () => {
  const dir = await tempDir();
  const gate = await fakeGate(dir, join(dir, "args.log"), { mode: "hang" });
  const signal = new AbortController().signal;
  const client = createGateClient({ bin: gate, cwd: dir, signal, timeoutMs: 10 });

  await assert.rejects(
    () => client.ls(),
    (error) => isGateError(error, "GATE_COMMAND_FAILED")
  );
});

test("project config parser resolves service domains", () => {
  const config = parseProjectConfig(`[project]
name = "demo"
base = "demo.localhost"

[services.web]

[services.root]
host = "."

[services.api]
domain = "api.example.test"
`);
  assert.equal(resolveServiceDomain(config, "web"), "web.demo.localhost");
  assert.equal(resolveServiceDomain(config, "root"), "demo.localhost");
  assert.equal(resolveServiceDomain(config, "api"), "api.example.test");
});

async function tempDir(): Promise<string> {
  return await mkdtemp(join(tmpdir(), "gate-node-"));
}

async function fakeGate(dir: string, log: string, options: { mode?: "permission" | "invalid-json" | "hang" } = {}): Promise<string> {
  const path = join(dir, "gate-fake.mjs");
  const body = `#!/usr/bin/env node
import { appendFileSync } from "node:fs";
const args = process.argv.slice(2);
appendFileSync(${JSON.stringify(log)}, args.join(" ") + "\\n");
if (${JSON.stringify(options.mode)} === "permission") {
  console.error(JSON.stringify({ error: { code: "permission", message: "permission required" } }));
  process.exit(3);
}
if (${JSON.stringify(options.mode)} === "invalid-json") {
  console.log("not json");
  process.exit(0);
}
if (${JSON.stringify(options.mode)} === "hang") {
  await new Promise(() => {});
}
const cmd = args[0];
if (cmd === "up") {
  console.log(JSON.stringify({ project: "demo", reloaded: false, services: [{ service: "web", domain: "web.demo.localhost", port: 4312, allocated: true }] }));
  process.exit(0);
}
if (cmd === "ls") {
  console.log(JSON.stringify({ services: [{ project: "demo", service: "web", domain: "web.demo.localhost", port: 4312, route: "active", upstream: "down" }] }));
  process.exit(0);
}
if (cmd === "port") {
  console.log(JSON.stringify({ service: args.at(-1), port: 4312 }));
  process.exit(0);
}
if (cmd === "down") {
  console.log(JSON.stringify({ ok: true }));
  process.exit(0);
}
console.error("unknown command");
process.exit(2);
`;
  await writeFile(path, body);
  await chmod(path, 0o755);
  return path;
}

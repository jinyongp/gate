import { createGateClient } from "@jinyongp/gate";

const gate = createGateClient({ cwd: import.meta.dirname });
const web = await gate.service("web", { up: true });

if (!Number.isInteger(web.port) || web.port <= 0) {
  throw new Error(`invalid reserved port: ${web.port}`);
}

console.log(JSON.stringify({
  service: web.service,
  port: web.port,
  url: web.url,
  loopbackUrl: web.loopbackUrl
}));

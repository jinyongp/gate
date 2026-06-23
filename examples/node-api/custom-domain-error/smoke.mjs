import { createGateClient, isGateError } from "@gate/node";

const gate = createGateClient({ cwd: import.meta.dirname });

try {
  await gate.service("web", { up: true });
} catch (error) {
  if (isGateError(error, "GATE_DNS_REQUIRED")) {
    console.log(JSON.stringify({ code: error.code }));
    process.exit(0);
  }
  throw error;
}

throw new Error("expected GATE_DNS_REQUIRED");

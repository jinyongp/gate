import http from "node:http";

const port = parsePort(process.env.PORT ?? "4301");
const host = "127.0.0.1";

const server = http.createServer((req, res) => {
  if (req.url === "/healthz") {
    res.writeHead(200, { "content-type": "text/plain; charset=utf-8" });
    res.end("ok\n");
    return;
  }

  res.writeHead(200, { "content-type": "text/plain; charset=utf-8" });
  res.end(
    `hello from prx js sample\nhost: ${req.headers.host ?? ""}\ntime: ${new Date().toISOString()}\n`,
  );
});

server.listen(port, host, () => {
  console.log("listening on sample HTTP server");
});

function parsePort(raw) {
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 1 || value > 65535) {
    console.error("invalid PORT");
    process.exit(1);
  }
  return value;
}

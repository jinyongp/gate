# hello-go

Tiny HTTP server for testing prx locally.

## Run Through Development prx

From the repository root:

```bash
just hello-go
```

Then open:

```text
https://hello-go.localhost
```

Use `Ctrl-C` to stop the sample server.

The first browser visit may show `ERR_CERT_AUTHORITY_INVALID`. That means the local prx CA is not trusted yet. For smoke testing, use the browser's advanced/proceed flow.

To remove the warning:

```bash
bin/prx trust
```

Run it from the repository root. It may ask for administrator approval.

## Direct Run

```bash
go run .
```

The server listens on `127.0.0.1:4300` by default.

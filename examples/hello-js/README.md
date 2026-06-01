# hello-js

Tiny Node.js HTTP server for testing prx locally.

## Run Through Development prx

From the repository root:

```bash
just hello-js
```

Then open:

```text
https://hello-js.localhost
```

Use `Ctrl-C` to stop the sample server.

The first browser visit may show `ERR_CERT_AUTHORITY_INVALID`. That means the local prx CA is not trusted yet. For smoke testing, use the browser's advanced/proceed flow.

To remove the warning:

```bash
bin/prx trust
```

Run it from the repository root. It may ask for administrator approval.

## Config Variants

`.localhost` config:

```toml
[project]
name = "hello-js"

[services.web]
domain = "hello-js.localhost"
```

File: `localhost/prx.toml`

Custom-domain config:

```toml
[project]
name = "hello-js-custom"

[services.web]
domain = "hello-js.test"
```

File: `custom-domain/prx.toml`

Run custom-domain smoke from the repository root:

```bash
just hello-js-custom
```

Then open:

```text
https://hello-js.test
```

Custom domains are not automatic like `.localhost`; they need `/etc/hosts` or local DNS setup. `just hello-js-custom` uses HTTPS `:443` and HTTP `:80`; if another process owns those ports, stop it first. The recipe adds `hello-js.test` to `/etc/hosts` inside a dedicated `<prx:hello-js-custom-hosts>` block, so sudo may ask for your password once. Remove it with `just hello-js-custom-clean`. TLS still needs `bin/prx trust` or browser advanced/proceed.

## Direct Run

```bash
node server.mjs
```

The server listens on `127.0.0.1:4301` by default.

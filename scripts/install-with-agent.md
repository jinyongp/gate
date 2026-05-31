# prx AI Agent bootstrap

Use this file when an AI Agent is setting up `prx` for a repository.

1) Install `prx` binary:

```bash
curl -fsSL https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install.sh | sh
```

2) Install the agent skill:

```bash
if command -v npx >/dev/null 2>&1; then
  npx skills add jinyongp/prx
else
  apm install jinyongp/prx
fi
```

3) Add `prx.toml` from project context:

- If `prx.toml` already exists, keep it and do not overwrite it.
- If missing, inspect the project and infer the dev port:
  - `package.json` scripts: `vite`/`next`/`nuxt` → `3000` (Vite is often `5173` when explicitly configured)
  - `go.mod` projects → `8080` default
  - `docker-compose.yml`/`docker-compose.yaml` with mapped host port → use that port
  - fallback: `3000`
- Create `prx.toml` using the detected service name and port:

```toml
[project]
name = "<repo-folder-name>"

[services.web]
domain = "<repo-folder-name>.example.localhost"
port = 3000
```

4) Suggest and run:

```bash
prx up
```

5) Report where the install landed and what was detected.

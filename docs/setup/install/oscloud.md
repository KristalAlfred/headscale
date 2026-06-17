# Running headscale on Eyevinn Open Source Cloud

!!! warning "Community documentation"

    This page is not actively maintained by the headscale authors and is
    written by community members. It is _not_ verified by headscale developers.

    **It might be outdated and it might miss necessary steps**.

[Eyevinn Open Source Cloud (OSC)](https://www.osaas.io) can run headscale via the
[`Eyevinn/golang-runner`](https://app.osaas.io) service. The runner clones a Git
repository, runs `go build`, and executes the resulting binary behind a single
TCP port with TLS terminated for you.

Headscale does not match the runner's contract out of the box, so this repository
ships a thin entrypoint at `cmd/server/main.go`. The runner auto-detects and
builds `cmd/server` first; the entrypoint binds `$PORT`, derives headscale's
configuration from environment variables and OSC secrets, materializes the noise
private key, and then delegates to the regular `headscale serve` path. A
`/healthz` alias is served alongside `/health` for the runner's health checks.

## How the entrypoint maps the environment

All headscale settings are read from the environment — no `config.yaml` is needed.
Operator-provided variables always take precedence; the entrypoint only fills in
defaults that would otherwise break a config-less boot:

| Variable | Default applied | Notes |
| --- | --- | --- |
| `HEADSCALE_LISTEN_ADDR` | `0.0.0.0:$PORT` (`PORT` defaults to `8080`) | Mandatory — headscale has no default listen address. |
| `HEADSCALE_DNS_OVERRIDE_LOCAL_DNS` | `false` | Avoids requiring global nameservers at boot. |
| `HEADSCALE_DNS_MAGIC_DNS` | `false` | MagicDNS requires a `base_domain`; disabled so a config-less boot succeeds. |
| `HEADSCALE_PREFIXES_V4` | `100.64.0.0/10` | Tailnet IPv4 range. |
| `HEADSCALE_PREFIXES_V6` | `fd7a:115c:a1e0::/48` | Tailnet IPv6 range. |
| `HEADSCALE_POLICY_MODE` | `database` | No policy file required. |
| `HEADSCALE_DATABASE_TYPE` | `postgres` | State lives in external PostgreSQL. |
| `HEADSCALE_UNIX_SOCKET` | `/tmp/headscale.sock` | Default path may be read-only. |
| `HEADSCALE_DERP_URLS` | `https://controlplane.tailscale.com/derpmap/default` | A DERP map is mandatory; defaults to Tailscale's public relays since the embedded relay cannot be exposed. |

The entrypoint also translates a single connection URL into headscale's discrete
PostgreSQL settings. If `DATABASE_URL` is set to
`postgres://user:pass@host:port/dbname?sslmode=require`, it populates:

- `HEADSCALE_DATABASE_POSTGRES_HOST`
- `HEADSCALE_DATABASE_POSTGRES_PORT`
- `HEADSCALE_DATABASE_POSTGRES_NAME`
- `HEADSCALE_DATABASE_POSTGRES_USER`
- `HEADSCALE_DATABASE_POSTGRES_PASS`
- `HEADSCALE_DATABASE_POSTGRES_SSL` (the `sslmode` query value, passed through
  verbatim, e.g. `require` or `disable`; omitted when absent)

You may also set those discrete `HEADSCALE_DATABASE_POSTGRES_*` variables directly
instead of `DATABASE_URL` — values already present in the environment are not
overwritten.

## Required environment and OSC secrets

| Variable | Where | Purpose |
| --- | --- | --- |
| `HEADSCALE_SERVER_URL` | env | The externally reachable HTTPS URL of the instance, for example `https://<assigned-host>`. Must match the URL clients use. |
| `DATABASE_URL` | secret | PostgreSQL connection URL (see below). |
| `HEADSCALE_NOISE_PRIVATE_KEY` | secret | The noise private **key value** (a `privkey:` string), seeded once (see below). |
| `PORT` | env | Injected by the runner; the entrypoint binds `0.0.0.0:$PORT`. |

Any other headscale setting can be overridden through its `HEADSCALE_*`
environment variable.

## Provision the PostgreSQL service

The PostgreSQL service slug is `birme-osc-postgresql`.

1. Store an admin password as a secret, then create the instance:

    ```shell
    npx -y @osaas/cli@latest secrets create birme-osc-postgresql pgpwd '<STRONG_PASSWORD>'
    npx -y @osaas/cli@latest create birme-osc-postgresql headscaledb \
      -o PostgresPassword='{{secrets.pgpwd}}' \
      -o PostgresUser=postgres \
      -o PostgresDb=headscale
    ```

2. Read back the host and external port (there is no single connection-URL
   field — you assemble it):

    ```shell
    npx -y @osaas/cli@latest --json describe birme-osc-postgresql headscaledb
    ```

3. Build the connection URL from that host/port and your password:

    ```text
    postgres://postgres:<password>@<host>:<port>/headscale?sslmode=disable
    ```

   Store it as the `dburl` secret on the golang-runner service (next section).

## Seed the noise private key (one-time)

The noise private key is headscale's server identity. Losing it forces every node
to re-register, so it is injected as a secret rather than regenerated on each boot.

1. Generate a key locally:

    ```shell
    go run ./cmd/headscale generate private-key
    ```

    This prints a `privkey:...` value.

2. Store that value as a secret on the golang-runner service and reference it
   from `HEADSCALE_NOISE_PRIVATE_KEY` at create time (see next section). The
   entrypoint writes it to `/tmp/noise_private.key` at startup and points
   headscale at that path.

If `HEADSCALE_NOISE_PRIVATE_KEY` is not set, the entrypoint falls back to
`/usercontent/noise_private.key`. The durability of `/usercontent` across restarts
is **not guaranteed**, so always seed the secret for a stable identity.

## Deploy with the OSC CLI

Authenticate first by exporting a personal access token (from the OSC web console
under **Settings → `{ }` API**):

```shell
export OSC_ACCESS_TOKEN=<your-token>
```

The golang-runner service slug is `eyevinn-golang-runner`. The `create` command
takes only `-o key=value` options — service options, plain environment variables,
and secret references are all passed this way. Secrets are created per service and
referenced as `{{secrets.<name>}}`:

```shell
# create the secrets on the golang-runner service
npx -y @osaas/cli@latest secrets create eyevinn-golang-runner dburl '<DATABASE_URL>'
npx -y @osaas/cli@latest secrets create eyevinn-golang-runner noisekey '<privkey:...>'

# first pass: create the instance (read its assigned url from the output)
npx -y @osaas/cli@latest --json create eyevinn-golang-runner headscale \
  -o SOURCE_URL='https://github.com/KristalAlfred/headscale.git#osc-golang-runner' \
  -o PORT=8080 \
  -o DATABASE_URL='{{secrets.dburl}}' \
  -o HEADSCALE_NOISE_PRIVATE_KEY='{{secrets.noisekey}}'
```

The instance's public URL is assigned at creation and returned as the `url` field;
it is not predictable beforehand. Because `HEADSCALE_SERVER_URL` must match that
URL and the CLI has no option-patch command, remove and recreate the instance once
the URL is known, adding it:

```shell
npx -y @osaas/cli@latest remove eyevinn-golang-runner headscale
npx -y @osaas/cli@latest --json create eyevinn-golang-runner headscale \
  -o SOURCE_URL='https://github.com/KristalAlfred/headscale.git#osc-golang-runner' \
  -o PORT=8080 \
  -o HEADSCALE_SERVER_URL='https://<assigned-url>' \
  -o DATABASE_URL='{{secrets.dburl}}' \
  -o HEADSCALE_NOISE_PRIVATE_KEY='{{secrets.noisekey}}'
```

Watch the build/boot logs, then verify:

```shell
npx -y @osaas/cli@latest logs eyevinn-golang-runner headscale
curl https://<assigned-url>/healthz
curl https://<assigned-url>/health
```

Both `curl`s should return `200` once the build finishes (the runner serves a
`503 Building` page on `/healthz` until then). The logs should show a successful
PostgreSQL connection, migrations applied, and the noise key loaded from the
seeded path.

## TLS and `server_url`

OSC terminates TLS at its edge and forwards plain HTTP to the container. Run
headscale over HTTP (do not enable headscale's own TLS) and set
`HEADSCALE_SERVER_URL` to the external `https://…` URL. If you need the real client
address from the proxy headers, set `HEADSCALE_TRUSTED_PROXIES`
(see [Configuration](../../ref/configuration.md)).

## Limitations

- **Single TCP port.** Only one port is exposed and UDP is not available, so the
  embedded DERP/STUN relay cannot be reached. The entrypoint defaults
  `HEADSCALE_DERP_URLS` to Tailscale's public DERP map; point it at your own
  external DERP map to avoid relying on Tailscale's relays.
- **No metrics endpoint.** The Prometheus metrics listener is a second port and is
  not exposed.
- **Admin gRPC API not reachable.** The gRPC API binds internally only and is not
  exposed over the single public port. Management tasks such as creating preauth
  keys require a container exec session into the running instance.

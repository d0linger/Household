# Deploy Household from Portainer

Use [compose.portainer.yaml](../compose.portainer.yaml) in Portainer on a
**Docker Standalone Linux/amd64** endpoint. The stack runs one Household
instance, PostgreSQL 18 and the encrypted backup worker. It expects an existing
HTTPS reverse proxy container on the same Docker endpoint. The proxy is the
public entry point. Only the app joins the proxy network.

## Prepare the variables

For a **new installation**, run this once in a private directory on a Docker
host. It writes randomly generated secrets to an owner-readable file.

```sh
(umask 077; set -C; docker run --rm ghcr.io/d0linger/household:production init > .env.portainer)
```

For an **existing installation**, start with the current production values. Keep
the existing database role passwords, encryption key or keyring, CSRF and rate
secrets, VAPID key, and restic passwords. [The switch procedure below](#switch-an-existing-installation)
also preserves the volumes.

| Portainer variable | Value |
| --- | --- |
| `HOUSEHOLD_ORIGIN` | The exact public HTTPS origin, such as `https://home.example.org`. Include a nonstandard port when used. Use the domain root. |
| `HOUSEHOLD_PROXY_NETWORK` | The existing Docker bridge network name used by your reverse proxy. |
| `HOUSEHOLD_TRUSTED_PROXIES` | The proxy's source CIDR as seen by Household, preferably its reserved Docker IP with `/32` for IPv4 or `/128` for IPv6. For more than one trusted proxy address, use a comma-separated list. |
| `HOUSEHOLD_IMAGE` | Optional image override. For controlled updates, set the [verified release digest](release-process.md#image-and-permission-contract) as `ghcr.io/d0linger/household@sha256:<digest>`. The default is `ghcr.io/d0linger/household:production`. |
| `HOUSEHOLD_NAME`, `HOUSEHOLD_ADMIN_USERNAME` | Initial household and username. Defaults are `Mein Haushalt` and `admin`. |

The generated file supplies `POSTGRES_PASSWORD`, `HOUSEHOLD_OWNER_PASSWORD`,
`HOUSEHOLD_APP_PASSWORD`, `HOUSEHOLD_CSRF_SECRET`, `HOUSEHOLD_RATE_SECRET`,
`HOUSEHOLD_ENCRYPTION_KEY`, `HOUSEHOLD_ADMIN_PASSWORD`, `RESTIC_PASSWORD` and
`HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY`. For an existing keyring installation,
enter `HOUSEHOLD_ENCRYPTION_KEYRING` and leave the single key blank. The bootstrap
password is needed on a fresh database and must be at least 14 characters and
at most 72 bytes. An initialized instance uses its stored accounts.

Keep an encrypted copy of the actual variables separately from the Docker host
and its backups. Portainer and Docker administrators can inspect container
environment variables, so restrict access to both. The
[variable reference](../.env.production.example) lists every value accepted by
this stack, including offsite backups, alerts, document import, meter adapters
and metrics. For scanner folders or an internal meter CA, add the corresponding
host mount and app variable using the examples in [the installation guide](installation.md#was-läuft-da).

## Create the stack

1. Attach the proxy to the bridge network you entered as `HOUSEHOLD_PROXY_NETWORK`.
   On a shared network, trust the proxy's individual address. Reserve it in your
   proxy configuration so recreating the proxy preserves that address. You can
   inspect the network from the Docker host with `docker network inspect <name>`.
2. In Portainer, go to **Stacks → Add stack → Web editor** and paste
   [compose.portainer.yaml](../compose.portainer.yaml). Under **Environment
   variables**, use **Load variables from .env file** or add values individually.
   Fill in the origin, network and proxy CIDRs before deploy. If authentication
   is required for your GHCR package, configure and select its Portainer registry
   with read access. [Portainer stack documentation](https://docs.portainer.io/user/docker/stacks/add).
3. Configure the proxy for your HTTPS hostname with upstream
   `http://household-app:8080`. Preserve the original `Host` and set
   `X-Forwarded-Proto: https` and the verified client address in
   `X-Forwarded-For`. At the public edge, replace client-supplied forwarding
   headers with values from the actual connection.
4. Deploy and wait for `db`, `app` and `backup` to become healthy. On a fresh
   installation, the backup worker retries while the app creates the roles and
   applies migrations. Check `/health/ready` through your HTTPS hostname
   for HTTP 200. Sign in at that hostname and change the bootstrap password
   if this is a new household.
5. In **Settings → Backup**, create a backup and run its restore check. The
   supplied Compose mounts `backups` read-only in the app and shares a new
   `backup_exchange` volume with the worker for encrypted file transfers. Keep
   existing volume names when applying this update. For
   host-loss recovery, configure an offsite destination in the stack variables,
   test the connection and enable automatic offsite copies in the app. The
   [installation guide](installation.md#externes-backup-s3-backblaze-nas)
   gives S3, restic REST and mounted-path examples.

If your HTTPS proxy runs on the Docker host, use the loopback topology from
[`compose.yaml`](../compose.yaml). When using the Portainer file for that topology,
remove both `proxy` network definitions, keep the app on the default network,
and add `ports: ["127.0.0.1:8100:8080"]` under `app`. Point the proxy at
`http://127.0.0.1:8100` and use the source address Household actually sees
from that connection for `HOUSEHOLD_TRUSTED_PROXIES`.

## Switch an existing installation

This procedure assumes PostgreSQL 18 and the current single-file Household
stack. If you still use `.local/secrets`, follow the
[legacy credential migration](installation.md#umstieg-von-einer-älteren-installation-mit-localsecrets).
Plan a separate PostgreSQL major-version upgrade before using this stack with
data from an earlier PostgreSQL major version.

1. On the existing release, take a backup and run its restore check. Save the
   current stack configuration, variables and image digest for rollback.
2. Confirm the actual Docker volume names in the existing containers' mounts.
   The default names are `<stack>_postgres`, `<stack>_uploads`,
   `<stack>_backups` and `<stack>_backup_work`. Update a Portainer-managed
   stack under the same name to retain these volumes. If importing a
   command-line installation or using a new stack name, stop the previous
   services without removing volumes and declare the existing volumes
   explicitly to avoid silently creating an empty household. This example
   uses `existing_household_postgres` for PostgreSQL.

   ```yaml
   volumes:
     postgres:
       external: true
       name: existing_household_postgres
   ```

   Apply this to each existing volume, using its actual name. Preserve any
   custom mounts for integrations or offsite destinations as well.
3. Switch the Compose configuration with the current image digest first. Verify
   readiness, existing sign-in, a known record or file, and a backup restore
   check. Then update the digest in Portainer when ready to upgrade. Migrations
   run on app startup. Review the release notes and
   [upgrade and rollback procedure](operations.md#upgrades-and-rollback)
   before changing versions. A rollback across incompatible schema versions
   requires the compatible database backup and the previous image.

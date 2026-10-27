# dyndns-updater

A small Go service with no third-party dependencies. Run it on an always-on machine on your brother's network. It checks immediately, then waits five minutes between completed checks, rotating through one lookup URL each time (including after failures). All outbound connections use IPv4, bypass proxy environment variables, and refuse redirects.

An address different from the last successfully published address triggers an HTTPS GET to the update URL. Only `good <requested-ip>` and `nochg <requested-ip>` count as success. Failed requests leave state unchanged and retry on the next interval. Last successful IPv4 is saved using an atomic file replacement and loaded after restart. A failure to save causes a later retry; provider can acknowledge that repeat with `nochg`.

## Configuration

Set these environment variables in your service manager. Keep credentials and private configuration outside this repository.

| Variable | Meaning |
|---|---|
| `DYNDNS_UPDATE_URL` | Required HTTPS update URL, with literal `{ip}` where the detected IPv4 belongs. Use provider's documented endpoint below with your hostname. No credentials in the URL. |
| `DYNDNS_USERNAME` / `DYNDNS_PASSWORD` | provider dynamic DNS credentials, sent using HTTP Basic authentication. Both or neither must be set. |
| `DYNDNS_STATE_FILE` | Required absolute path outside this checkout; parent directory must exist and be writable. Persist this file across restarts. |
| `DYNDNS_LOOKUP_URLS` | Optional comma-separated or whitespace-separated HTTPS URLs; takes precedence over the file. |
| `DYNDNS_LOOKUP_FILE` | One URL per line, blank lines and `#` comments allowed. Defaults to `examples/urls.txt` in the working directory. |
| `DYNDNS_INTERVAL` | Go duration, default `5m`, minimum `1s`. Keep a sensible interval to respect free services. |
| `DYNDNS_LISTEN` | Health/Prometheus listener, default `:8080`. Bind to loopback or firewall it if needed. |

provider's documented URL is `https://provider.invalid/nic/update?system=dyndns&hostname=$HOSTNAME&myip=$IP`. Insert the actual dynamic DNS hostname and replace `$IP` with `{ip}`. Create the dynamic DNS record and its dedicated user in provider first. A generic webhook that merely returns HTTP 200 is deliberately insufficient: this service expects the DynDNS response protocol.

## Build and run

Requires the Go version declared in `go.mod`:

```sh
go build -o /tmp/dyndns-updater .
/tmp/dyndns-updater
```

Set the environment first. Run a single instance per state file, under a service manager configured to start at boot and restart on failure. Use an absolute `DYNDNS_LOOKUP_FILE` when the working directory differs. SIGINT/SIGTERM cancels in-flight requests and stops the service. The HTTP timeout is 15 seconds. Logs omit URLs, credentials, response bodies, and IPs.

`/health` reports updater status and returns 503 until the first successful check and after a failed check. `/metrics` exposes check/failure counters. These are activity indicators, not a DNS propagation check.

## Network limitation

The updater sees the network's public egress IPv4. If the provider uses CGNAT, DNS can track that shared address but cannot make the UniFi console reachable from outside; remote access would need an outbound tunnel, VPN, or a provider-supplied public address. Run this on the intended network and avoid a VPN routing these requests elsewhere.

## Checks

```sh
go test ./...
go vet ./...
```

No database, scheduler, framework, dashboard, or DNS API dependency. `examples/urls.txt` contains twelve distinct sites, live-tested over IPv4 on 27 October 2026. Lookup list and rotation position load/reset at startup. Delete state (while stopped) when changing the target hostname, to force publication to the new target.

## Linux systemd deployment

Build the binary for the target machine and put it in `/opt/dyndns-updater/`. Copy `examples/urls.txt` there as `urls.txt`. Install `deploy/dyndns-updater.service` in `/etc/systemd/system/`. Create `/etc/dyndns-updater.env` outside the repo, readable only by root, containing the required update URL and credentials as environment assignments. The unit supplies the state and lookup paths; interval/listener overrides can also go in that external file. Then run:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now dyndns-updater
sudo journalctl -u dyndns-updater -f
```

The unit creates a persistent state directory and runs under a dedicated dynamic user. It has not been exercised on a Linux deployment in this session.

## Docker Compose

Run [examples/compose.yaml](examples/compose.yaml) on an always-on Docker host on your brother's network. Export `DYNDNS_UPDATE_URL`, `DYNDNS_USERNAME`, and `DYNDNS_PASSWORD` through your shell or secret manager. The update URL must contain the literal `{ip}` placeholder. Compose refuses to start if a required variable is missing or empty.

From this checkout:

```sh
docker compose -f examples/compose.yaml up -d
docker compose -f examples/compose.yaml logs -f
```

Alternatively, keep the three environment assignments in a private file outside the checkout and load it with Compose's `--env-file` option:

```sh
docker compose --env-file "$HOME/.config/dyndns-updater.env" -f examples/compose.yaml up -d
```

The example uses the published GHCR `latest` image and its bundled lookup URLs. Set `DYNDNS_IMAGE` to a release tag or digest to pin a version, and `DYNDNS_INTERVAL` to change the default five-minute interval. An image must have been published before it can be pulled.

The named `state` volume persists the last successfully published IP across container recreation. Keep the Compose project name unchanged to reuse that volume. `restart: unless-stopped` restarts the service after failures and Docker restarts unless you explicitly stop it; Docker must start at boot. No inbound ports are required. Stop with `docker compose -f examples/compose.yaml down`; do not add `--volumes` unless you intend to delete the saved state.

## Releases and containers

GitHub Actions uses release-please and GoReleaser to publish binary archives to GitHub Releases and multi-platform images to GHCR and Docker Hub.

# dyndns-updater

A small Go service with no third-party dependencies. Run it on an always-on machine on your brother's network. It checks immediately, then waits five minutes between completed checks, rotating through one lookup URL each time (including after failures). All outbound connections use IPv4, bypass proxy environment variables, and refuse redirects.

An address different from the last successfully published address triggers an HTTPS GET to the update URL. The default success rule accepts DynDNS2 `good`/`nochg` responses, with an optional matching IPv4; other response formats use an explicit success regex. Every response line must succeed. HTTP 2xx alone is insufficient. Failed requests leave state unchanged and retry on the next interval. Last successful IPv4 is saved using an atomic file replacement and loaded after restart. A failure to save causes a later retry; the provider must acknowledge the repeated update before state is saved.

## Configuration

Set these environment variables in your service manager. Keep credentials and private configuration outside this repository.

| Variable | Meaning |
|---|---|
| `DYNDNS_UPDATE_URL` | Required HTTPS URL template. Use `{ip}` or `{ipv4}` for the detected address and named placeholders for other values. No embedded Basic credentials or fragments. |
| `DYNDNS_USERNAME` / `DYNDNS_PASSWORD` | Optional HTTP Basic credentials. Set both or neither; omit for query-token or header authentication. |
| `DYNDNS_PARAMETERS` | JSON object of string values for named placeholders, such as `hostname`, `domain`, `host`, `token`, `key`, `zone`, or `record_id`. Values can be secrets: keep this environment variable private. |
| `DYNDNS_UPDATE_METHOD` | `GET` (default), `POST`, `PUT`, or `PATCH`. |
| `DYNDNS_UPDATE_HEADERS` | JSON object of header names and string templates. Supports token authentication and a provider-specific `User-Agent`. Do not combine an Authorization header with Basic credentials. |
| `DYNDNS_UPDATE_BODY` | Optional JSON or form-urlencoded template. The default content type for a body is `application/json`. GET cannot have a body. |
| `DYNDNS_SUCCESS_REGEX` | Optional Go/RE2 regex matching the entire trimmed response body. Supports `{ip}` and named placeholders as escaped literals. Non-2xx responses always fail. |
| `DYNDNS_STATE_FILE` | Required absolute path outside this checkout; parent directory must exist and be writable. Persist this file across restarts. |
| `DYNDNS_LOOKUP_URLS` | Optional comma-separated or whitespace-separated HTTPS URLs; takes precedence over the file. |
| `DYNDNS_LOOKUP_FILE` | One URL per line, blank lines and `#` comments allowed. Defaults to `examples/urls.txt` in the working directory. |
| `DYNDNS_INTERVAL` | Go duration, default `5m`, minimum `1s`. Keep a sensible interval to respect free services. |
| `DYNDNS_LISTEN` | Health/Prometheus listener, default `:8080`. Bind to loopback or firewall it if needed. |

## URL and request templates

`{ip}` and `{ipv4}` are filled from the current lookup. Any other `{name}` comes from `DYNDNS_PARAMETERS`, a JSON object supplied privately at runtime. For example, a URL ending in `?hostname={hostname}&myip={ip}&token={token}` takes `hostname` and `token` from that object. Parameter names use letters, digits, and underscores and begin with a letter; the two IP names are reserved.

Path values are percent-encoded as path components; query values are query-encoded. Headers use literal substitution with control characters rejected. JSON body substitutions are escaped as JSON string content; put placeholders inside JSON strings, for example `{"content":"{ip}"}`. For form bodies set Content-Type to `application/x-www-form-urlencoded`; values are form-encoded. Static query parameters, repeated parameters, provider-specific names, and flags can be written directly into the URL. The scheme and host cannot be templated.

Missing parameters, malformed JSON, conflicting authentication, invalid headers, and invalid success expressions fail before any network update. Only update requests receive configured credentials, parameters, and headers; lookup requests remain separate. Request URLs, bodies, parameter values, headers, and provider response bodies are not logged.

A URL or body must explicitly contain an IP placeholder: implicit source-IP-only endpoints are not supported. One update request is configured per process. If you change its target or parameters, stop the service and clear its saved IP to force an update for the new configuration.

## Provider URL formats

These are configuration templates, not a provider registry. The implementation contains no provider names. Populate the named parameters privately; Basic authentication refers to `DYNDNS_USERNAME` / `DYNDNS_PASSWORD`. The list covers common documented update APIs, not every DNS service. Authenticated provider updates have not been live-tested against customer accounts.

| Provider / official documentation | URL template | Authentication / response |
|---|---|---|
| [Dyn](https://help.dyn.com/perform-update.html) | `https://members.dyndns.org/nic/update?hostname={hostname}&myip={ip}` | Basic; default DynDNS2 success. Other documented options include `system`, `wildcard`, `mx`, `backmx`. |
| [No-IP](https://www.noip.com/integrate/request) | `https://dynupdate.no-ip.com/nic/update?hostname={hostname}&myip={ip}` | Basic with update-key credentials; default success. `hostname` may be comma-separated. Set a User-Agent following its documented format using UPDATE_HEADERS. |
| [Dynu](https://downloads.dynu.com/DynamicDNS/IP-Update-Protocol) | `https://api.dynu.com/nic/update?hostname={hostname}&myip={ip}` | Basic; default success. Also supports `username`, `password`, `group`, `myipv6`; do not set IPv6 from this IPv4-only updater. |
| [Hurricane Electric](https://dns.he.net/docs.html) | `https://dyn.dns.he.net/nic/update?hostname={hostname}&myip={ip}` | Basic: hostname and record update key; default success. A `password` query parameter is also documented. |
| [deSEC](https://desec.readthedocs.io/en/latest/dyndns/update-api.html) | `https://update.dedyn.io/?hostname={hostname}&myipv4={ip}&myipv6=preserve` | Basic: hostname/token, or Authorization `Token {token}`; default `good` success. Explicitly preserve IPv6. |
| [STRATO](https://www.strato.de/faq/hosting/so-einfach-richten-sie-dyndns-fuer-ihre-domains-ein/) | `https://dyndns.strato.com/nic/update?hostname={hostname}&myip={ip}` | Basic: domain and update password; default success. `system`, `mx`, `backmx` are documented options; avoid unintended record changes. |
| [nsupdate.info](https://nsupdateinfo.readthedocs.io/en/latest/user.html) | `https://nsupdate.info/nic/update?hostname={hostname}&myip={ip}` | Basic: full hostname and host update secret; default success. |
| [dynv6](https://dynv6.com/docs/apis) | `https://dynv6.com/nic/update?hostname={hostname}&myip={ip}` | DynDNS-compatible endpoint, Basic hostname/token. Its separate `/api/update?zone={zone}&token={token}&ipv4={ip}` uses native text responses and needs a matching success regex. |
| [Duck DNS](https://www.duckdns.org/spec.jsp) | `https://www.duckdns.org/update?domains={domains}&token={token}&ip={ip}` | Query token; success regex `OK`. `domains` is comma-separated subnames. Do not enable `clear`; `verbose=true` changes the response format. |
| [Namecheap](https://www.namecheap.com/support/knowledgebase/article.aspx/29/11/how-to-dynamically-update-the-hosts-ip-with-an-https-request/) | `https://dynamicdns.park-your-domain.com/update?host={host}&domain={domain}&password={password}&ip={ip}` | Dedicated DDNS password in query; XML response needs an explicit success regex, checking zero errors and the requested IP. `host` can be `@`, a subdomain, or `*`. |
| [FreeDNS](https://freedns.afraid.org/faq/help.php?help_id=1) | `https://freedns.afraid.org/dynamic/update.php?{key}&address={ip}` | Account-generated update key; provider-specific text response needs an explicit success regex. Do not enable `offline`. |
| [easyDNS](https://kb.easydns.com/knowledge/dynamic-dns/) | `https://api.cp.easydns.com/dyn/generic.php?hostname={hostname}&myip={ip}` | Basic username/dynamic token; success regex `(NOERROR\|OK)`. Optional `TLD`, `MX`, `backmx`, `wildcard` affect other records. Use an interval of at least `10m`. |
| [DNSExit](https://dnsexit.com/services/free-dynamic-dns-service/) | `https://api.dnsexit.com/dns/ud/?apikey={key}&host={hostname}&ip={ip}` | Query API key; JSON success codes 0 or 1. Example regex: `(?s).*"code"\s*:\s*[01]\s*[,}].*`. Interval must exceed four minutes. |
| [Alviy](https://www.alviy.com/en/docs/update-api) | `https://dynupdate.alviy.com/token/update?hostname={hostname}&token={token}&myip={ip}` | Query token; default success. `/nic/update` alternatively supports Basic authentication. |
| [mayDDNS](https://mayddns.com/support/api) | `https://mayddns.com/nic/update?hostname={hostname}&myip={ip}` | Basic; default success. The source-IP-only UUID endpoint does not meet the explicit IP requirement. |

The regex is automatically anchored around the whole response. Use `(?s).*...*` only when the response has surrounding XML/JSON/text, and match explicit success fields rather than a generic word. To check Namecheap XML, for example, use `(?s).*<IP>{ip}</IP>.*<ErrCount>0</ErrCount>.*<Done>true</Done>.*`. Error responses must not match your rule. An explicit `^$` is available only for APIs whose documented successful 2xx response has an empty body.

### REST DNS APIs

Static-token REST APIs are configurable using the same mechanism. Record and zone IDs must already be known; this updater does not discover or create records.

| API / documentation | Method and URL template | Headers, body, and success rule |
|---|---|---|
| [Cloudflare](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/edit/) | PATCH `https://api.cloudflare.com/client/v4/zones/{zone_id}/dns_records/{record_id}` | Authorization header `Bearer {token}`; JSON body `{"content":"{ip}"}`; success regex `(?s).*"success"\s*:\s*true.*`. Restrict the token to the intended DNS zone. |
| [DigitalOcean](https://docs.digitalocean.com/products/networking/dns/reference/api/domain-records/) | PUT `https://api.digitalocean.com/v2/domains/{domain}/records/{record_id}` | Authorization header `Bearer {token}`; JSON body `{"data":"{ip}"}`; success regex `(?s).*"data"\s*:\s*"{ip}".*`. |
| [Gandi LiveDNS](https://api.gandi.net/docs/livedns/) | PUT `https://api.gandi.net/v5/livedns/domains/{domain}/records/{host}/A` | Authorization header `Bearer {token}`; JSON body `{"rrset_values":["{ip}"]}`; match the documented successful response for this operation. This replaces the A-record set at that name. |

[Route 53](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ChangeResourceRecordSets.html) needs signed AWS requests and XML change batches; it is not supported by static URL/header templates. RFC 2136, OAuth refresh flows, and provider-specific signing are also outside this HTTP updater. DNS-O-Matic was reviewed as a legacy DynDNS-style API; verify current service availability before considering it. Providers inaccessible through their primary documentation are not advertised as supported here.

Respect each provider's retry and rate-limit policy. This service uses its configured interval after failures, so select an interval suitable for your provider and fix authentication errors before leaving it unattended.

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

Run [examples/compose.yaml](examples/compose.yaml) on an always-on Docker host on your brother's network. Export `DYNDNS_UPDATE_URL` plus the authentication and template variables your provider needs through your shell or secret manager. The URL or body must contain `{ip}` or `{ipv4}`. Compose requires the URL; the application validates all other required values at startup. Basic credentials are optional for token-based providers.

From this checkout:

```sh
docker compose -f examples/compose.yaml up -d
docker compose -f examples/compose.yaml logs -f
```

Alternatively, keep your provider environment assignments in a private file outside the checkout and load it with Compose's `--env-file` option:

```sh
docker compose --env-file "$HOME/.config/dyndns-updater.env" -f examples/compose.yaml up -d
```

The example uses the published GHCR `latest` image and its bundled lookup URLs. Set `DYNDNS_IMAGE` to a release tag or digest to pin a version, and `DYNDNS_INTERVAL` to change the default five-minute interval. An image must have been published before it can be pulled.

The named `state` volume persists the last successfully published IP across container recreation. Keep the Compose project name unchanged to reuse that volume. `restart: unless-stopped` restarts the service after failures and Docker restarts unless you explicitly stop it; Docker must start at boot. No inbound ports are required. Stop with `docker compose -f examples/compose.yaml down`; do not add `--volumes` unless you intend to delete the saved state.

## Releases and containers

GitHub Actions uses release-please and GoReleaser to publish binary archives to GitHub Releases and multi-platform images to GHCR and Docker Hub.

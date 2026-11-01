# phortos

A caching proxy for Debian and Ubuntu package repositories on your LAN: the first host downloads
a package from the internet, and every other host gets it from phortos. Sources stay as they are
(`http://deb.debian.org`, `https://download.docker.com`); apt asks phortos per repository host
whether to go through it, and goes direct when phortos is down or does not serve that host.

- **Never in the way.** A stopped phortos costs apt about a second; it is never a reason an
  update fails.
- **Never wrong bytes.** Every package is checked against the SHA-256 the repository's own
  `Packages` index gives, and every index against its `InRelease`, before phortos keeps it.
  Anything it cannot check is passed through and not kept. apt still checks the signatures itself.
- **One download for the fleet.** Concurrent requests for a file share one download; requests apt
  pipelines on a connection are fetched at once, not one by one.
- **HTTPS repositories too, opt-in per host.** phortos can end TLS for listed hosts with a CA that
  only apt trusts, name-constrained to those hosts and anchored to a pinned key (see
  [Security](#security)).
- **phortosctl** points a host's apt at phortos, undoes it, checks it worked, and keeps itself
  and phortos' CAs current before every `apt update`.
- **Small.** Two static Go binaries. phortosctl uses the standard library only; phortos adds one
  module, to read its YAML config file.

## How it works

phortosctl writes `/etc/apt/apt.conf.d/00phortos`, which tells apt to ask a small program before
it reaches a repository host:

```
Acquire::http::Proxy-Auto-Detect "/usr/local/lib/phortos/detect";
Acquire::https::Proxy-Auto-Detect "/usr/local/lib/phortos/detect";
```

`detect` asks phortos (`GET /api/route`, within one second) whether it serves that host. phortos
answers with its proxy address, and apt goes through it; or `DIRECT`, or nothing in time, and apt
goes direct. So phortos alone decides what it proxies, and hosts keep no list. apt asks once per
repository host per run.

`mirror+file:` sources were considered and rejected: on Ubuntu 24.04, apt hangs when the first
mirror refuses connections, which is exactly what a stopped cache looks like.

If phortos dies in the middle of an apt run, that run fails like it would with a mirror going
away; the next run goes direct.

### What is cached

| Request | Policy |
|---|---|
| `InRelease`, `Release`, `Release.gpg` | Revalidated upstream after `--release-ttl` (conditional, usually a 304). Served stale when upstream does not answer within `--release-within`. |
| `by-hash` index files | Checked against the hash in their path, kept. |
| Other index files (`Packages.gz`, …) | Checked against the `InRelease` phortos holds. |
| Package files | Checked against the `Packages` index phortos read itself, kept until evicted. |
| Anything else on a served host | Passed through, not kept (logged with why). |
| A host phortos does not serve | Refused. Hosts never send these: `detect` answered `DIRECT`. |

phortos reads `Packages` indexes in gzip, bzip2 and xz. A package asked for before any index lists
it waits briefly for one being read, then is passed through.

## Quick start

### 1. Run phortos

On a Linux host with systemd (a dry run without `--yes`):

```sh
sudo ./phortos install --max-size=200G --yes
```

It copies itself to `/usr/local/bin`, runs as its own `phortos` user with the store in
`/var/lib/phortos/store` and its keys in `/var/lib/phortos/keys`, writes its settings to
`/etc/phortos/phortos.yaml`, and restarts on failure; a systemd watchdog restarts it if it hangs.
`sudo phortos uninstall --yes` removes the service and keeps the store and the keys.

At start phortos logs a one-liner for hosts, with its pin:

```
curl -fsSL -k --pinnedpubkey 'sha256//…' https://phortos.lan:3143/setup | sudo sh -s -- configure --yes
```

`--advertise=phortos.lan` sets the name it puts there (the machine's hostname otherwise).

### 2. Point hosts at it

Run the one-liner on each host, as root. `-k` turns off curl's CA check, and `--pinnedpubkey`
replaces it with a stronger one: curl takes nothing unless the server holds the pinned key. The
script downloads phortosctl over that connection, checks its SHA-256, and runs it. Without
`configure --yes` it runs `plan`, which only reports.

Then check that apt goes through phortos:

```sh
sudo /usr/local/lib/phortos/phortosctl verify
```

phortosctl stays installed: apt runs it before every update (to fetch phortos' current CAs and,
unless `--no-auto-upgrade`, the phortosctl phortos hands out) and per repository host (`detect`).

## Serving repositories

phortos serves the hosts in its config file, `/etc/phortos/phortos.yaml`:

```yaml
# plain-HTTP repository hosts
hosts:
  - deb.debian.org
  - ftp.debian.org
  - security.debian.org
  - archive.ubuntu.com
  - "*.archive.ubuntu.com"
  - security.ubuntu.com
  - ports.ubuntu.com
  - download.proxmox.com
# HTTPS repository hosts, served by ending TLS (needs the keys)
intercept:
  - download.docker.com
  - developer.download.nvidia.com
max-size: 200G
```

`phortos install` writes the plain-HTTP defaults above. To serve another repository, add its host
to the list: phortos reads the file again within seconds (or on `systemctl reload phortos`), and
`hosts`, `intercept` and `max-size` apply at once. Other settings apply at the next restart.

Hosts pick up a newly intercepted HTTPS host by themselves: the next `apt update` installs its CA
entry, and the one after goes through phortos.

Repositories that need credentials are never proxied: a host named in `/etc/apt/auth.conf` or
`auth.conf.d/` is always `DIRECT` on that machine, so credentials never pass through phortos.
Ubuntu Pro's `esm.ubuntu.com` is always direct too.

## phortosctl

Every command that changes something prints exactly what it would do, and does it only with
`--yes`. phortosctl edits none of the host's own files: it adds `/etc/apt/apt.conf.d/00phortos`,
`/etc/phortos/` and `/usr/local/lib/phortos/`, and refuses a host whose apt has proxy settings of
its own.

| Command | Does |
|---|---|
| `plan` | Reports phortos' addresses, the pin, the intercepted hosts and CAs, hosts with credentials, conflicting proxy settings, and the files configure would write. Changes nothing. |
| `configure` | Writes the files above (apt's configuration last). `--server` and `--pin` come from the setup script. `--no-auto-upgrade` keeps phortosctl at its version. |
| `unconfigure` | Removes everything phortosctl wrote. |
| `verify` | Runs `apt-get update` and fails on any warning or error, or when phortos saw no request from it. |
| `refresh` | What apt runs before every update: fetches phortos' working CAs over the pinned connection, checks them, rebuilds the CA bundle and apt's configuration when they changed, and installs a new phortosctl when phortos hands one out. On any problem it keeps what is there and never fails the update. |
| `upgrade` | Installs the phortosctl phortos hands out, when it differs. |
| `detect URL` | What apt runs per repository host: prints phortos' proxy address or `DIRECT`. |
| `version` | Prints the version. |

## Running phortos

Every flag can also be a key in the config file (`--config`); the command line wins.

| Flag | Default | |
|---|---|---|
| `--dir` | (required) | The store's folder. `--init` makes it, with the marker file phortos checks so it never writes into the wrong folder. |
| `--addr` | `:3142` | The proxy's address (apt's conventional proxy port). |
| `--hosts` | Debian, Ubuntu, Proxmox | Plain-HTTP repository hosts to serve; `*.example.org` serves its subdomains. |
| `--intercept` | none | HTTPS repository hosts to end TLS for; needs `--keys`. |
| `--keys` | none | The folder of phortos' identity and working CAs. With it phortos serves its pinned API on `--api-addr` (`:3143`). |
| `--advertise` | hostname | The name hosts reach phortos by, in the one-liner it logs. |
| `--max-size` | none | The store's cap, such as `200G`; least recently used files are evicted beyond it. |
| `--release-ttl` | `1m` | How long a release file is answered without asking upstream. |
| `--release-within` | `10s` | How long a release file's revalidation may take before the kept copy is served. |
| `--stall-after` | `1m` | How long a download may receive nothing before it is given up. |
| `--min-rate` | `32K` | A download slower than this (bytes a second) for a whole `--stall-after` is given up, and its connection closed, so the next try opens a fresh one. `0` turns it off. |
| `--resolver` | system | A DNS server (`host:port`) to look repositories up with. |
| `--self-check` | `30s` | How often phortos asks its own `/healthz`, exiting after 3 failures so systemd restarts it. |
| `--reload-every` | `5s` | How often the config file is read again. |
| `--debug` | off | Log every answer with where it came from (hit, miss, joined, passed). |

Endpoints:

| Endpoint | |
|---|---|
| `GET /healthz` (`:3142`) | 200 when phortos can serve. |
| `GET /metrics` (`:3142`) | Prometheus counters: file requests by result (hit, miss, joined, passed), prefetches, download failures, release revalidations, bytes served, fetched and passed, evictions, store size, and `phortos_build_info`. Counters start at 0 when phortos starts. |
| `GET /api/route?url=…` (`:3142`) | What `detect` asks. |
| `GET /setup`, `/get/<file>`, `/trust` (`:3143`, pinned TLS) | The setup script, phortosctl, and the working CAs. |

## Security

phortos is built for a trusted LAN. The proxy port has no TLS and no authentication: anyone who
can reach it can fetch the repositories it serves through it, and read its metrics. It never
fetches from a host it does not serve.

**Plain HTTP repositories** need no keys at all; apt's signature checks are what make them safe,
with or without phortos. A cache can delay an update, never forge one.

**HTTPS repositories** are intercepted only when listed under `--intercept`. Three layers of keys:

| Layer | What | Lifetime | Where |
|---|---|---|---|
| Identity | An ECDSA P-256 key. Its public key's SHA-256 is the pin. | none | Private, on phortos only. The pin is on every host. |
| Working CA | The CA apt checks against, name-constrained to the intercepted hosts and signed by the identity key. | 30 days, rotated by phortos | On hosts, in apt's CA bundle only. |
| Leaf certificates | One per intercepted host. | 1 day | On phortos only. |

- The working CA is added to a CA bundle that apt uses for the intercepted hosts alone. It is
  never put in the system's trust store, so curl, browsers and Docker never trust it.
- `refresh` accepts a working CA only with a valid signature from the pinned identity. `detect`
  goes `DIRECT` when the host's CA is not the one phortos serves, has expired, or is not yet valid
  (a wrong clock), so a broken trust chain costs caching, never updates.
- phortos checks every upstream certificate in full, with no option to turn that off, and never
  follows a redirect from HTTPS to HTTP.
- A stolen identity key would let someone intercept apt's traffic to the listed hosts, but still
  not forge packages: their signatures do not depend on TLS. The worst case equals what a
  plain-HTTP repository gives all the time.
- The setup one-liner is safe to send over an untrusted network once you have the pin from a
  trusted one (SSH, your own console): curl runs nothing unless the server proves it holds the
  pinned key.

How to report a vulnerability, and what counts as one: [SECURITY.md](SECURITY.md).

## What has been tested

| Platform | State |
|---|---|
| phortos on Linux amd64 (systemd, Ubuntu 26.04) | in use: a cache for a fleet of Proxmox and Ubuntu hosts, plain HTTP and intercepted HTTPS |
| Debian 13 (apt 3.0.3), Proxmox VE 9, amd64 | in use, through `configure`, `refresh` and phortosctl's own upgrades |
| Ubuntu 26.04 (apt 3.2.0), amd64 | in use, likewise |
| Debian 12 (apt 2.6.1), Ubuntu 24.04 (apt 2.8.3) | the fallback, interception and CA rotation behaviour phortos relies on, in containers |
| Linux arm64 | phortosctl and phortos are built for it; not tested |

What a fleet gains, measured on one Proxmox host's upgrade of 289 packages (1 GB): 37.5 s with
phortos fetching everything, 12.3 s once another host had upgraded first, and 6.5 s (179 MB/s)
for a download-only run served entirely from the store.

## Building

```sh
go generate ./internal/frontdoor/setup/dist   # phortosctl for every platform phortos hands out
go build ./cmd/phortos ./cmd/phortosctl
go test ./...
```

A phortos built without `go generate` works, but serves no phortosctl. The tests run on Linux;
some need a POSIX shell.

## License

Copyright 2026 darki73. Licensed under Apache-2.0: see [LICENSE](LICENSE). phortos includes
go.yaml.in/yaml/v3 (Apache-2.0 and MIT), whose notices are in [NOTICE](NOTICE).

If you redistribute phortos, or something built from it (a fork, a binary, a product), the
licence asks you to keep the copyright headers, ship the [NOTICE](NOTICE), and say what you
changed. Please also link back to this repository.

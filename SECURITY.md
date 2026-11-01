# Security Policy

## Reporting a vulnerability

If you find a security vulnerability in phortos, please report it privately.

**Do not open a public issue.** Use
[GitHub's private vulnerability reporting](https://github.com/nauarch/phortos/security/advisories/new)
instead, so the details stay private until a fix is out.

Critical issues are fixed and released as soon as possible.

## Threat model

phortos is built for a trusted LAN. These are choices, not vulnerabilities:

- **The proxy port has no TLS and no authentication.** Anyone who can reach it can fetch the
  repositories phortos serves through it, and read its metrics. Keep it on a network you trust.
- **The identity key lives on the phortos host.** Whoever controls that host can intercept apt's
  traffic to the intercepted repositories. They still cannot forge packages: apt checks the
  repositories' signatures, which do not depend on TLS.
- **The pin is copied once from a trusted channel.** The setup one-liner is as trustworthy as the
  pin in it; take it from phortos' log over SSH or a console, never from an unauthenticated page.

## Scope

Reports are welcome on anything that breaks these guarantees:

- **Cache integrity.** A file is kept only after it matches the SHA-256 that phortos learned from
  the repository's own indexes (or its by-hash path), and an index only after it matches the
  `InRelease` phortos holds. Any way to make phortos keep or serve a file under a sum it does not
  match is in scope.
- **Upstream reach.** phortos fetches only from the hosts it is configured to serve, follows no
  redirect from HTTPS to HTTP, and checks every upstream certificate. Any request that makes it
  fetch from elsewhere, or over a weaker channel, is in scope.
- **The trust chain.** Working CAs are name-constrained to the intercepted hosts and signed by
  the identity key; phortosctl installs only CAs whose signature verifies against its pin, and
  only into apt's own CA bundle. Anything that makes a host trust a certificate for another name,
  trust phortos' CA outside apt, or accept a CA the pinned identity did not sign is in scope.
- **Credentials.** Hosts with credentials in apt's `auth.conf` are always reached directly.
  Anything that makes credentials reach phortos is in scope.
- **The setup script.** It runs only over the pinned connection, and the address phortos writes
  into it must be a plain `host:port`. Any way to inject into it, or to get it run without the
  pin, is in scope.
- **phortosctl on a host.** It runs as root, writes only the files it lists in its dry run, and
  `detect` runs as apt's unprivileged `_apt` user. Anything that makes it write, remove or run
  something else, or that lets `detect`'s input reach more than a route lookup, is in scope.
- **The store.** Files are kept by digest, so no request names a path outside the store, and
  phortos refuses a folder without its marker file. The store keeps under `--max-size`.

## Supported versions

Only the latest release receives security fixes.

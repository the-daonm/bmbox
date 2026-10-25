# Security policy

bmbox runs as root and manages bridges, firewall rules, systemd units and
BMC credentials, so security reports are taken seriously.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub:
**Security → Report a vulnerability** on
https://github.com/the-daonm/bmbox, with steps to reproduce and the
affected version (`bmbox --version`).

You should get an answer within a week. Fixes are released as soon as they
are ready, and credited unless you prefer otherwise.

## Supported versions

Security fixes go to the latest release.

## Scope notes

- BMCs listen on `127.0.0.1` by default. Binding them to other addresses
  exposes power control of the nodes to that network. Use TLS (the default
  for Redfish) and strong passwords.
- IPMI 2.0 itself has well-known weaknesses (for example RAKP hash
  disclosure). Prefer Redfish on shared networks.

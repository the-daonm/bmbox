# BMCs

Each node with a `bmc` gets its own emulated BMC, running as the systemd
unit `bmbox-<lab>-<node>-bmc.service`:

- **Redfish** (sushy-tools) only exposes its own node. It is served over
  HTTPS with a certificate from bmbox's CA. Trust
  `/var/lib/bmbox/ca/ca.crt` (one CA per host, for every lab), or set
  `tls: false` for plain HTTP. `up` reissues certificates before they
  expire.
- **IPMI** (virtualbmc) speaks IPMI 2.0 (`lanplus`). Passwords are limited
  to 20 characters.

BMCs listen on `127.0.0.1` unless `address` says otherwise.

## Passwords

Without one in the topology, `up` generates a random password per node and
keeps it. `passwordFile: /path` or `passwordEnv: VAR` keep it out of the
topology. The value is stored too, so a later `up` without the variable (at
boot, for instance) keeps working. `sudo bmbox credentials` shows them.

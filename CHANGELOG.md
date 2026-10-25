# Changelog

Notable changes. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/). Before 1.0, minor
versions may change behaviour.

## [Unreleased]

### Added
- `bmbox setup`: installs the packages bmbox needs, starts libvirt, and
  installs sushy-tools and virtualbmc at tested versions.
- `bmbox doctor`: checks the host and every lab, with fixes.
- `bmbox credentials`: BMC endpoints, usernames, passwords and CA.
- Labs come back after a host reboot (`bmbox-<lab>-boot.service`, opt
  out with `spec.autostart: false`). `up --lab NAME` re-applies the saved
  topology.
- Redfish BMCs over HTTPS with certificates from a per-host CA
  (`bmc.tls`, default on).
- BMC passwords from `passwordFile` or `passwordEnv`.
- `--output json` for `status`, `list`, `credentials` and `doctor`.
- `--quiet`, `--verbose`, and timestamped progress outside a terminal.
- Release packages (.deb, .rpm, tarball) and CI.

### Changed
- BMCs without a password get a generated one instead of `password`. Run
  `sudo bmbox credentials` after upgrading.
- Redfish endpoints are `https://` by default.
- State lives in `/var/lib/bmbox` for every user of the host instead of
  `~/.bmbox`. Existing labs are moved by their next `up`.
- `state.json` carries a format version. A state from a newer bmbox is
  refused.
- The Go module path is `github.com/the-daonm/bmbox`.
- Lab names `labs`, `tools` and `ca` are reserved.

### Before
- Topology, bridges, libvirt domains with UEFI and serial console, storage,
  per-node Redfish/IPMI BMCs, destroy and status, DHCP and iPXE netboot,
  traffic on lab bridges under `bridge-nf-call-iptables`, orphan detection
  and `--prune`.

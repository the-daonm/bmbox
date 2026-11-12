# Development

```bash
make build    # bin/bmbox, version from git
make test     # unit tests
make lint     # gofmt, go vet, staticcheck
make e2e      # end-to-end test on a libvirt host with sudo
make dist     # tarball, .deb and .rpm in dist/
make docs     # serve this site locally on http://127.0.0.1:8000
```

`scripts/e2e.sh` runs the whole lifecycle against real libvirt in its own
lab (`e2e`, subnet 172.30.90.0/24, BMC ports 8390/6390): up, Redfish and
IPMI power control, netboot to a kernel on the serial console, console,
convergence and `--prune`, traffic past a `FORWARD` drop, a simulated reboot,
locking and destroy. It fails if anything outside its lab changed on the
host. CI runs it on every pull request on GitHub-hosted runners (which
have KVM), after preparing them with `bmbox setup`.

## Layout

```
cmd/            CLI commands
pkg/topology    topology.yaml schema, defaults, validation
pkg/engine      up (with orphan detection and prune), destroy, status, boot units
pkg/network     bridges over netlink, sysctls, FORWARD rules
pkg/hypervisor  libvirt: firmware, storage pool, domain XML
pkg/bmc         Redfish and IPMI BMCs, passwords, TLS
pkg/netboot     per-lab dnsmasq: DHCP, TFTP, iPXE
pkg/systemd     transient and persistent units over D-Bus
pkg/console     serial console client
pkg/setup       host setup
pkg/doctor      host checks
pkg/workspace   /var/lib/bmbox state
pkg/logging     quiet, verbose, timestamps
```

## Contributing

Issues and pull requests are welcome. See
[CONTRIBUTING.md](https://github.com/the-daonm/bmbox/blob/main/CONTRIBUTING.md).
Report security problems as described in
[SECURITY.md](https://github.com/the-daonm/bmbox/blob/main/SECURITY.md).

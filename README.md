# bmbox

[![ci](https://github.com/the-daonm/bmbox/actions/workflows/ci.yml/badge.svg)](https://github.com/the-daonm/bmbox/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**Virtual bare-metal labs from a single YAML file.**

> **Status: beta.** bmbox is used and tested end to end, but before 1.0 the
> topology format (`bmbox.io/v1alpha1`) and command-line behaviour can still
> change between minor versions. Changes are listed in the
> [changelog](CHANGELOG.md).

bmbox builds a lab of "bare-metal" servers on one Linux host: KVM + UEFI
virtual machines that PXE boot, with a dedicated Redfish or IPMI BMC each,
on their own networks with DHCP and iPXE. Use it to develop and test
provisioning systems (OpenStack Ironic, Metal3, MAAS, Tinkerbell, your own
tooling) without racks of hardware.

```yaml
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata:
  name: demo
spec:
  networks:
    - name: pxe
      cidr: 172.30.10.0/24
      dhcp: {}
  nodes:
    - name: node1
      nics: [{network: pxe}]
      bmc: {type: redfish}
```

```console
$ sudo bmbox up -f topology.yaml
$ sudo bmbox credentials
NODE   TYPE     USERNAME  PASSWORD          ENDPOINT
node1  redfish  admin     q7XkM2pR9vTb3nWc  https://127.0.0.1:8300/redfish/v1/Systems/...
```

## Features

- **Declarative.** `up` converges the host to `topology.yaml` and can be run
  again at any time. Disks and NVRAM are kept, and what was removed from the
  file is reported (or deleted with `--prune`).
- **Real server behaviour.** UEFI (OVMF) with per-node NVRAM, PXE first then
  disk, sparse qcow2 disks, stable MACs, and a serial console with
  history.
- **One BMC per node.** Redfish (sushy-tools) over HTTPS, or IPMI
  (virtualbmc). Each BMC only controls its own node. Passwords are generated
  unless you set them.
- **Networks.** Linux bridges, optional host address, DHCP with fixed
  addresses, and netboot of a kernel and initrd or your own iPXE script.
  Existing bridges, such as an Ironic provisioning network, can be attached
  as external networks.
- **Survives reboots.** A boot unit brings every lab back after the host
  restarts.
- **Plays well with the host.** Everything bmbox creates is tagged as its
  own, and `destroy` removes only that. It keeps traffic flowing when Docker
  or Neutron set the `FORWARD` policy to `DROP`. Several labs can share a
  host.
- **Scriptable.** `--output json` for status, list, credentials and doctor,
  and timestamped logs outside a terminal.

## Requirements

- Linux on x86_64 with KVM (`/dev/kvm`) and systemd
- libvirt (`qemu:///system`), QEMU and OVMF
- For BMCs: Python 3 (bmbox installs sushy-tools and virtualbmc itself)
- For DHCP and netboot: dnsmasq and iPXE

`bmbox setup` installs all of these on Debian, Ubuntu, Fedora, RHEL 10 and
Arch (CI runs it on each). RHEL 9 and its rebuilds are not supported: their
Python 3.9 is too old for sushy-tools.

## Install

From a [release](https://github.com/the-daonm/bmbox/releases), as a
package:

```bash
sudo apt install ./bmbox_<version>_amd64.deb     # Debian, Ubuntu
sudo dnf install ./bmbox-<version>-1.x86_64.rpm  # Fedora, RHEL
```

or with Go (1.27+):

```bash
go install github.com/the-daonm/bmbox@latest
sudo install ~/go/bin/bmbox /usr/local/bin/bmbox
```

Then prepare the host and check it:

```bash
sudo bmbox setup     # packages, libvirt, BMC emulators; --dry-run to preview
sudo bmbox doctor    # KVM, libvirt, firmware, tools, labs: what is missing and how to fix it
```

`setup` only installs what is missing and can be run again. It puts
sushy-tools and virtualbmc, at the versions bmbox is tested with, in a
virtualenv under `/var/lib/bmbox/tools/venv`, and remembers every tool
path. Install bmbox in a system path such as `/usr/local/bin`, because the
boot units run that binary.

## Quick start

```bash
sudo bmbox up -f examples/topology.yaml    # networks, disks, nodes (off), DHCP, BMCs
sudo bmbox status                          # power, DHCP addresses, BMC state
sudo bmbox credentials                     # BMC endpoints, users, passwords, CA

# Power node1 on through its BMC, like a real server:
CA=/var/lib/bmbox/ca/ca.crt
curl --cacert $CA -u admin:PASSWORD -H 'Content-Type: application/json' \
  -X POST -d '{"ResetType":"On"}' \
  https://127.0.0.1:8300/redfish/v1/Systems/<uuid>/Actions/ComputerSystem.Reset
ipmitool -I lanplus -H 127.0.0.1 -p 6231 -U admin -P PASSWORD power on   # node2

sudo bmbox console node1                   # serial console, Ctrl-] to detach
sudo bmbox destroy                         # remove everything up created
```

`bmbox validate -f FILE` checks a topology and `bmbox render NODE -f FILE`
prints the libvirt domain XML, both offline.

## Commands

| Command | What it does |
|---|---|
| `setup` | Install and configure what bmbox needs on this host |
| `doctor` | Check the host and every lab, with fixes |
| `validate` | Parse and validate a topology |
| `render [NODE]` | Print the domain XML bmbox would define |
| `up` | Create or update a lab (`--prune` removes what left the topology) |
| `status` | Power state, DHCP leases and BMC health of each node |
| `credentials [NODE]` | BMC endpoint, username, password and CA |
| `console NODE` | Attach to a node's serial console |
| `list` | Labs on this host |
| `destroy` | Remove everything `up` created for a lab |

Global flags: `-f/--file` topology (default `topology.yaml`),
`-c/--connect` libvirt URI (default `$LIBVIRT_DEFAULT_URI` or
`qemu:///system`), `-q/--quiet`, `-v/--verbose` (prints the iptables, systemd
and libvirt calls). Commands that act on an existing lab take `--lab NAME`
instead of a file. `up --lab NAME` re-applies the topology the lab was last
brought up with.

## Topology

Everything except names has a default. See
[examples/topology.yaml](examples/topology.yaml) for a commented example.

| Field | Default | Meaning |
|---|---|---|
| `metadata.name` | required | Lab name, prefix of everything bmbox creates |
| `spec.defaults` | 2 CPUs, 4 GiB, one 20 GiB disk, boot `[network, disk]` | Node defaults |
| `spec.storage.path` | `/var/lib/libvirt/bmbox/<lab>` | Disks and NVRAM (a libvirt `dir` pool) |
| `spec.uefi.loader`, `varsTemplate` | asked from libvirt | OVMF images |
| `spec.autostart` | `true` | Bring the lab back when the host boots |
| `networks[].bridge` | `bmb-<lab>-<net>` | Bridge name |
| `networks[].external` | `false` | Attach to an existing bridge that bmbox never changes |
| `networks[].cidr` | none | Gives the host the first address, as gateway or provisioning server |
| `networks[].dhcp` | off | `{}` or `{range: first-last}`: DHCP from bmbox (IPv4) |
| `netboot` | none | `kernel`, `initrd`, `cmdline`, or your own iPXE `script` |
| `nodes[].cpus`, `memory`, `disks`, `boot` | from `defaults` | Node hardware |
| `nodes[].nics[]` | | `network`, `mac` (stable default), `ip` (fixed DHCP address) |
| `nodes[].bmc.type` | `redfish` | `redfish` or `ipmi` |
| `nodes[].bmc.address`, `port` | `127.0.0.1`, 8300+ / 6330+ | Where the BMC listens |
| `nodes[].bmc.username` | `admin` | |
| `nodes[].bmc.password` / `passwordFile` / `passwordEnv` | generated | See [BMCs](#bmcs) |
| `nodes[].bmc.tls` | `true` for Redfish | Serve Redfish over HTTPS |

## BMCs

Each node with a `bmc` gets its own emulated BMC, running as the systemd
unit `bmbox-<lab>-<node>-bmc.service`:

- **Redfish** (sushy-tools) only exposes its own node. It is served over
  HTTPS with a certificate from bmbox's CA. Trust
  `/var/lib/bmbox/ca/ca.crt` (one CA per host, for every lab), or set
  `tls: false` for plain HTTP. `up` reissues certificates before they
  expire.
- **IPMI** (virtualbmc) speaks IPMI 2.0 (`lanplus`). Passwords are limited
  to 20 characters.

Passwords: without one in the topology, `up` generates a random password per
node and keeps it. `passwordFile: /path` or `passwordEnv: VAR` keep it out of
the topology. The value is stored too, so a later `up` without the variable
(at boot, for instance) keeps working. `sudo bmbox credentials` shows them.
BMCs listen on `127.0.0.1` unless `address` says otherwise.

## DHCP and netboot

A network with `dhcp: {}` (it needs a `cidr`) gets DHCP from bmbox: one
dnsmasq per lab, as `bmbox-<lab>-dnsmasq.service`, answering on the lab's
bridges only. With `spec.netboot`, nodes that PXE boot get iPXE and then a
generated `boot.ipxe` that boots the kernel and initrd:

```yaml
networks:
  - name: pxe
    cidr: 172.30.10.0/24
    dhcp: {}                     # or {range: 172.30.10.100-172.30.10.199}
netboot:
  kernel: /srv/netboot/vmlinuz   # absolute path (copied) or http(s) URL
  initrd: /srv/netboot/initrd.img
  cmdline: console=ttyS0         # so `bmbox console` shows the kernel
  # script: /srv/netboot/menu.ipxe   # or your own iPXE script instead
nodes:
  - name: node1
    nics: [{network: pxe, ip: 172.30.10.10}]
```

Config, leases and the TFTP root live in `/var/lib/bmbox/<lab>`. dnsmasq
binds only the lab bridges, so it can run next to libvirt's or Ironic's
dnsmasq. Do not enable `dhcp` on a network where another DHCP server
already answers, such as an Ironic provisioning network.

## Firewall

When `br_netfilter` is loaded with `net.bridge.bridge-nf-call-iptables=1`
(Docker and Neutron set this), frames between two nodes on the same bridge
go through the iptables `FORWARD` chain. With Docker's `DROP` policy, the
nodes could not reach each other. bmbox does what libvirt does: in a
`BMBOX-FORWARD` chain, which `FORWARD` jumps to first, it accepts traffic
that enters and leaves the same lab bridge, in iptables and ip6tables:

```
-A FORWARD -j BMBOX-FORWARD
-A BMBOX-FORWARD -i bmb-demo-pxe -o bmb-demo-pxe -m comment --comment "bmbox:demo/pxe" -j ACCEPT
```

Host-wide settings are left alone, and nothing is routed or NATed between a
lab and the outside. External bridges get no rule. `destroy` and
`up --prune` remove the rules. A firewall reload that flushes `FORWARD`
drops them, and the next `up` restores them.

## After a reboot

Bridges, firewall rules and the BMC and dnsmasq units do not survive a host
reboot, while the VMs and disks do. `up` therefore enables
`bmbox-<lab>-boot.service`, which runs `bmbox up --lab <lab>` at boot,
before `libvirt-guests` restores any running guests. Set
`spec.autostart: false` to opt out, and run `sudo bmbox up --lab <lab>`
yourself. `bmbox doctor` lists labs that will not come back.

## Files

```
/var/lib/bmbox/
├── tools.json              tool paths found by setup / up
├── tools/venv/             sushy-tools and virtualbmc
├── ca/ca.crt               CA of the Redfish certificates (trust this)
├── labs/<lab>/
│   ├── topology.yaml       topology of the last up (root only)
│   ├── state.json          what up created, used by status and destroy
│   ├── domains/<node>.xml  rendered domain XML
│   └── bmc/<node>/         BMC config, certificate, password (root only)
└── <lab>/                  dnsmasq config, leases, TFTP root
/var/lib/libvirt/bmbox/<lab>/   disks and NVRAM
/run/bmbox/<lab>/               serial console sockets and logs
```

`BMBOX_HOME` points the workspace somewhere else. Labs created by older
versions in `~/.bmbox` are still found, and are moved by their next `up`.

## Development

```bash
make build    # bin/bmbox, version from git
make test     # unit tests
make lint     # gofmt, go vet, staticcheck
make e2e      # end-to-end test on a libvirt host with sudo
make dist     # tarball, .deb and .rpm in dist/
```

`scripts/e2e.sh` runs the whole lifecycle against real libvirt in its own
lab (`e2e`, subnet 172.30.90.0/24, BMC ports 8390/6390): up, Redfish and
IPMI power control, netboot to a kernel on the serial console, console,
convergence and `--prune`, traffic past a `FORWARD` drop, a simulated reboot,
locking and destroy. It fails if anything outside its lab changed on the
host. CI runs it on every pull request on GitHub-hosted runners (which
have KVM), after preparing them with `bmbox setup`.

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

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
Report security problems as described in [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)

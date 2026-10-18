# bmbox

Declarative virtualized bare-metal testbed engine: describe a lab of
"bare-metal" servers (KVM + UEFI) in `topology.yaml`, and bmbox builds the
Linux bridges, storage and libvirt domains for it.

## Status

| Week | Scope | State |
|------|-------|-------|
| 3 | CLI, topology parser/validation, bridges via netlink, libvirt domains (UEFI, serial socket), storage pool + per-node NVRAM, workspace | done |
| 4 | Per-node BMCs (Redfish via sushy-tools, IPMI via virtualbmc) as systemd units, `bmbox destroy`, `bmbox status`, `bmbox console` | done |
| 5 | Per-lab DHCP and iPXE netboot (dnsmasq); node-to-node traffic under `bridge-nf-call-iptables` | done |
| 5 | Ironic export, BMC TLS and generated passwords, CI | planned |

## Usage

```bash
go build -o bin/bmbox .

bmbox validate -f examples/topology.yaml   # parse + validate
bmbox render node1 -f examples/topology.yaml   # print domain XML (offline)
sudo bmbox up -f examples/topology.yaml    # bridges, disks, NVRAM, domains (SHUTOFF), DHCP, BMCs
sudo bmbox up --prune                      # also remove what was dropped from the topology
sudo bmbox status                          # power state, DHCP lease, BMC endpoints
bmbox list                                 # labs in the workspace
sudo bmbox console node1                   # serial console (Ctrl-] to detach)
sudo bmbox destroy                         # remove everything up created
```

BMCs need [sushy-tools](https://opendev.org/openstack/sushy-tools) (Redfish)
and/or [virtualbmc](https://opendev.org/openstack/virtualbmc) (IPMI). sudo
resets `PATH`, so point bmbox at them with `BMBOX_SUSHY_EMULATOR` /
`BMBOX_VBMC` (or `--sushy-emulator` / `--vbmc`), e.g.
`sudo BMBOX_SUSHY_EMULATOR=~/venv/bin/sushy-emulator BMBOX_VBMC=~/venv/bin/vbmc bmbox up`.
The paths are remembered in `~/.bmbox/tools.json`, so later runs need no
variables.

## DHCP and netboot

A network with `dhcp: {}` (it needs a `cidr`) gets DHCP from bmbox: one
dnsmasq per lab, running as the systemd unit `bmbox-<lab>-dnsmasq.service`,
answers on the lab bridges only. A NIC can have a fixed address (`ip:`).
With `spec.netboot`, nodes that PXE boot get iPXE, then a generated
`boot.ipxe` that boots the given kernel and initrd:

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

Config, leases and the TFTP root are kept in `/var/lib/bmbox/<lab>`.
Because dnsmasq binds only the lab bridges (`bind-interfaces`), it can run
next to libvirt's or Ironic's dnsmasq. Do not enable `dhcp` on a network
where another DHCP server already answers, such as an Ironic provisioning
network.

You need dnsmasq (the `dnsmasq-base` package is enough) and the iPXE UEFI
binary (`ipxe` package: `/usr/lib/ipxe/snponly.efi`). They can also be
given with `BMBOX_DNSMASQ` / `BMBOX_IPXE` or `--dnsmasq` / `--ipxe`, and
are remembered like the BMC tools. They do not have to be installed
system-wide: `apt-get download dnsmasq-base ipxe` followed by `dpkg -x` into
a directory works.

## Firewall (bridge-nf-call-iptables)

When `br_netfilter` is loaded and `net.bridge.bridge-nf-call-iptables=1`
(Docker and Neutron set this host-wide), frames between two nodes on the
same bridge pass through the iptables `FORWARD` chain. If that chain's
policy is `DROP`, as Docker sets it, the nodes cannot reach each other.
Traffic to the host (DHCP, TFTP) goes through `INPUT` and is not affected.
bmbox does not change the host-wide sysctl. The per-bridge
`nf_call_iptables` knob cannot turn filtering off either, because the
kernel ORs it with the host setting. Instead, `up` does what libvirt does:
in a `BMBOX-FORWARD` chain, which `FORWARD` jumps to first, it accepts
traffic that enters and leaves the same lab bridge:

```
-A FORWARD -j BMBOX-FORWARD
-A BMBOX-FORWARD -i bmb-demo-pxe -o bmb-demo-pxe -m comment --comment "bmbox:demo/pxe" -j ACCEPT
```

The rule is added with both `iptables` and `ip6tables`, using the host's
own binaries (legacy or nft), so it lands in the chain that holds the
host's `DROP`. Rules are tagged with their lab network. `destroy` and
`up --prune` remove them, and the chain and jump go away with the last
rule. External bridges get no rule. Nothing is routed or NATed between a
lab and the outside. A firewall reload that flushes `FORWARD` drops the
jump, and the next `up` restores it.

Flags: `-f/--file` topology file (default `topology.yaml`), `-c/--connect`
libvirt URI (default `$LIBVIRT_DEFAULT_URI` or `qemu:///system`).
`BMBOX_HOME` overrides the workspace directory (default `~/.bmbox` of the
invoking user, also under sudo).

## Layout

```
cmd/            Cobra commands (validate, render, up, status, list, console, destroy)
pkg/topology    topology.yaml schema, defaults, validation
pkg/network     Linux bridges via rtnetlink, sysctls, FORWARD rules for lab bridges
pkg/hypervisor  libvirt RPC client: firmware discovery, storage pool, domain XML
pkg/systemd     transient service units over D-Bus
pkg/bmc         per-node Redfish/IPMI BMCs as systemd units
pkg/netboot     per-lab dnsmasq: DHCP, TFTP, iPXE script
pkg/console     terminal <-> serial UNIX socket
pkg/engine      orchestrates up (incl. orphan detection/prune), destroy and status
pkg/workspace   ~/.bmbox state (topology snapshot, state.json, domain XML)
examples/       sample topology
```

Node disks and NVRAM live in a libvirt `dir` storage pool
(`/var/lib/libvirt/bmbox/<lab>` by default) so they are reachable by libvirtd
even when it runs in a container.

## Tests

```bash
go test ./...                                  # unit tests
go build -o bin/bmbox . && BMBOX=bin/bmbox scripts/e2e.sh   # on a libvirt host
```

`scripts/e2e.sh` brings up its own lab (`e2e`, subnet 172.30.90.0/24,
BMC ports 8390/6390) and runs the whole lifecycle against real libvirt: up,
Redfish and IPMI power control, netboot (DHCP lease, iPXE, the kernel on
the serial console), console, convergence and `--prune`, traffic on a lab
bridge despite a `FORWARD` drop (two network namespaces as endpoints),
locking and destroy. By default it netboots the host's `/boot/vmlinuz` and
`/boot/initrd.img` (`E2E_KERNEL` / `E2E_INITRD`). It snapshots the rest
of the host (domains, storage pools, bridges, bmbox units and data, the
`FORWARD` chains and the sysctls bmbox may change) before and after, and
fails if anything outside its lab changed. It honours
`BMBOX_HOME` and `LIBVIRT_DEFAULT_URI`; BMC tools are found as for
`bmbox up`, and dnsmasq/iPXE are found as described above.

Demo walkthroughs: [week 3](docs/demo-week3.md), [week 4](docs/demo-week4.md), [week 5](docs/demo-week5.md).

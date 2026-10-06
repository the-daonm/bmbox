# bmbox

Declarative virtualized bare-metal testbed engine: describe a lab of
"bare-metal" servers (KVM + UEFI) in `topology.yaml`, and bmbox builds the
Linux bridges, storage and libvirt domains for it.

## Status

| Week | Scope | State |
|------|-------|-------|
| 3 | CLI, topology parser/validation, bridges via netlink, libvirt domains (UEFI, serial socket), storage pool + per-node NVRAM, workspace | done |
| 4 | Per-node BMCs (Redfish via sushy-tools, IPMI via virtualbmc) as systemd units, `bmbox destroy`, `bmbox status`, `bmbox console` | done |

## Usage

```bash
go build -o bin/bmbox .

bmbox validate -f examples/topology.yaml   # parse + validate
bmbox render node1 -f examples/topology.yaml   # print domain XML (offline)
sudo bmbox up -f examples/topology.yaml    # bridges, disks, NVRAM, domains (SHUTOFF), BMCs
sudo bmbox up --prune                      # also remove what was dropped from the topology
sudo bmbox status                          # power state + BMC endpoints
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

Flags: `-f/--file` topology file (default `topology.yaml`), `-c/--connect`
libvirt URI (default `$LIBVIRT_DEFAULT_URI` or `qemu:///system`).
`BMBOX_HOME` overrides the workspace directory (default `~/.bmbox` of the
invoking user, also under sudo).

## Layout

```
cmd/            Cobra commands (validate, render, up, status, list, console, destroy)
pkg/topology    topology.yaml schema, defaults, validation
pkg/network     Linux bridges via rtnetlink, sysctl handling
pkg/hypervisor  libvirt RPC client: firmware discovery, storage pool, domain XML
pkg/bmc         per-node Redfish/IPMI BMCs as systemd transient units (D-Bus)
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
Redfish and IPMI power control, console, convergence and `--prune`,
locking and destroy. It snapshots the rest of the host (domains, storage
pools, bridges, bmbox units and the sysctls bmbox may change) before and
after, and fails if anything outside its lab changed. It honours
`BMBOX_HOME` and `LIBVIRT_DEFAULT_URI`; BMC tools are found as for
`bmbox up`.

Demo walkthroughs: [week 3](docs/demo-week3.md), [week 4](docs/demo-week4.md).

# bmbox

Declarative virtualized bare-metal testbed engine: describe a lab of
"bare-metal" servers (KVM + UEFI) in `topology.yaml`, and bmbox builds the
Linux bridges, storage and libvirt domains for it.

## Status

| Week | Scope | State |
|------|-------|-------|
| 3 | CLI, topology parser/validation, bridges via netlink, libvirt domains (UEFI, serial socket), storage pool + per-node NVRAM, workspace | done |
| 4 | BMC (sushy-tools) integration, `bmbox up`/`destroy` full flow, `bmbox console` | planned |

## Usage

```bash
go build -o bin/bmbox .

bmbox validate -f examples/topology.yaml   # parse + validate
bmbox render node1 -f examples/topology.yaml   # print domain XML (offline)
sudo bmbox up -f examples/topology.yaml    # create bridges, disks, NVRAM, domains (SHUTOFF)
```

Flags: `-f/--file` topology file (default `topology.yaml`), `-c/--connect`
libvirt URI (default `$LIBVIRT_DEFAULT_URI` or `qemu:///system`).
`BMBOX_HOME` overrides the workspace directory (default `~/.bmbox` of the
invoking user, also under sudo).

## Layout

```
cmd/            Cobra commands (validate, render, up)
pkg/topology    topology.yaml schema, defaults, validation
pkg/network     Linux bridges via rtnetlink, sysctl handling
pkg/hypervisor  libvirt RPC client: firmware discovery, storage pool, domain XML
pkg/engine      orchestrates `up`
pkg/workspace   ~/.bmbox state (topology snapshot, state.json, domain XML)
examples/       sample topology
```

Node disks and NVRAM live in a libvirt `dir` storage pool
(`/var/lib/libvirt/bmbox/<lab>` by default) so they are reachable by libvirtd
even when it runs in a container.

See [DEMO.md](DEMO.md) for the week-3 demo walkthrough.

# Topology

Everything except names has a default.

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
| `netboot` | none | `kernel`, `initrd`, `cmdline`, or your own iPXE `script` (see [netboot](netboot.md)) |
| `nodes[].cpus`, `memory`, `disks`, `boot` | from `defaults` | Node hardware |
| `nodes[].nics[]` | | `network`, `mac` (stable default), `ip` (fixed DHCP address) |
| `nodes[].bmc.type` | `redfish` | `redfish` or `ipmi` |
| `nodes[].bmc.address`, `port` | `127.0.0.1`, 8300+ / 6330+ | Where the BMC listens |
| `nodes[].bmc.username` | `admin` | |
| `nodes[].bmc.password` / `passwordFile` / `passwordEnv` | generated | See [BMCs](bmcs.md#passwords) |
| `nodes[].bmc.tls` | `true` for Redfish | Serve Redfish over HTTPS |

## Example

A commented topology, from
[`examples/topology.yaml`](https://github.com/the-daonm/bmbox/blob/main/examples/topology.yaml):

```yaml
--8<-- "examples/topology.yaml"
```

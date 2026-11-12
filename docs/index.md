# bmbox

**Virtual bare-metal labs from a single YAML file.**

!!! warning "Status: beta"
    bmbox is used and tested end to end, but before 1.0 the topology format
    (`bmbox.io/v1alpha1`) and command-line behaviour can still change between
    minor versions. Changes are listed in the [changelog](changelog.md).

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

Next: [install bmbox](install.md), then follow the [quick start](quickstart.md).

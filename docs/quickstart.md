# Quick start

```bash
sudo bmbox up -f examples/topology.yaml    # networks, disks, nodes (off), DHCP, BMCs
sudo bmbox status                          # power, DHCP addresses, BMC state
sudo bmbox credentials                     # BMC endpoints, users, passwords, CA
```

Power node1 on through its BMC, like a real server:

```bash
CA=/var/lib/bmbox/ca/ca.crt
curl --cacert $CA -u admin:PASSWORD -H 'Content-Type: application/json' \
  -X POST -d '{"ResetType":"On"}' \
  https://127.0.0.1:8300/redfish/v1/Systems/<uuid>/Actions/ComputerSystem.Reset
ipmitool -I lanplus -H 127.0.0.1 -p 6231 -U admin -P PASSWORD power on   # node2
```

Watch it boot, then clean up:

```bash
sudo bmbox console node1                   # serial console, Ctrl-] to detach
sudo bmbox destroy                         # remove everything up created
```

`bmbox validate -f FILE` checks a topology and `bmbox render NODE -f FILE`
prints the libvirt domain XML, both offline.

The topology used above is
[`examples/topology.yaml`](topology.md#example).

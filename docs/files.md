# Files

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

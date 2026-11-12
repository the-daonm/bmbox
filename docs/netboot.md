# DHCP and netboot

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
dnsmasq.

!!! warning
    Do not enable `dhcp` on a network where another DHCP server already
    answers, such as an Ironic provisioning network.

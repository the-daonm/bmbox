# Firewall

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

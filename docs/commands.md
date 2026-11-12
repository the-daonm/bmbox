# Commands

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

## Global flags

| Flag | Default | Meaning |
|---|---|---|
| `-f`, `--file` | `topology.yaml` | Topology file |
| `-c`, `--connect` | `$LIBVIRT_DEFAULT_URI` or `qemu:///system` | libvirt URI |
| `-q`, `--quiet` | | Less output |
| `-v`, `--verbose` | | Print the iptables, systemd and libvirt calls |

Commands that act on an existing lab take `--lab NAME` instead of a file.
`up --lab NAME` re-applies the topology the lab was last brought up with.

`status`, `list`, `credentials` and `doctor` accept `--output json`.

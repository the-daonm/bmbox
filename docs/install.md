# Install

## Requirements

- Linux on x86_64 with KVM (`/dev/kvm`) and systemd
- libvirt (`qemu:///system`), QEMU and OVMF
- For BMCs: Python 3 (bmbox installs sushy-tools and virtualbmc itself)
- For DHCP and netboot: dnsmasq and iPXE

`bmbox setup` installs all of these on Debian, Ubuntu, Fedora, RHEL 10 and
Arch (CI runs it on each). RHEL 9 and its rebuilds are not supported: their
Python 3.9 is too old for sushy-tools.

## Install bmbox

From a [release](https://github.com/the-daonm/bmbox/releases), as a
package:

```bash
sudo apt install ./bmbox_<version>_amd64.deb     # Debian, Ubuntu
sudo dnf install ./bmbox-<version>-1.x86_64.rpm  # Fedora, RHEL
```

or with Go (1.27+):

```bash
go install github.com/the-daonm/bmbox@latest
sudo install ~/go/bin/bmbox /usr/local/bin/bmbox
```

## Prepare the host

```bash
sudo bmbox setup     # packages, libvirt, BMC emulators; --dry-run to preview
sudo bmbox doctor    # KVM, libvirt, firmware, tools, labs: what is missing and how to fix it
```

`setup` only installs what is missing and can be run again. It puts
sushy-tools and virtualbmc, at the versions bmbox is tested with, in a
virtualenv under `/var/lib/bmbox/tools/venv`, and remembers every tool
path.

!!! note
    Install bmbox in a system path such as `/usr/local/bin`, because the
    [boot units](reboot.md) run that binary.

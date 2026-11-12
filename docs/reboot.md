# After a reboot

Bridges, firewall rules and the BMC and dnsmasq units do not survive a host
reboot, while the VMs and disks do. `up` therefore enables
`bmbox-<lab>-boot.service`, which runs `bmbox up --lab <lab>` at boot,
before `libvirt-guests` restores any running guests.

Set `spec.autostart: false` to opt out, and run `sudo bmbox up --lab <lab>`
yourself. `bmbox doctor` lists labs that will not come back.

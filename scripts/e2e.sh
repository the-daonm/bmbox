#!/usr/bin/env bash
# End-to-end test of bmbox on a real libvirt host.
#
#   BMBOX=bin/bmbox scripts/e2e.sh
#
# The test brings up its own lab, "e2e" (two nodes: n1 with a Redfish BMC,
# n2 with an IPMI BMC, both netbooting from the lab's DHCP), on its own
# subnet and ports, so other labs on the host are left alone and checked to
# stay that way. Needs sudo, virsh, curl, ipmitool, flock and python3, plus
# sushy-tools, virtualbmc, dnsmasq and iPXE as for `bmbox up`
# (BMBOX_SUSHY_EMULATOR / BMBOX_VBMC / BMBOX_DNSMASQ / BMBOX_IPXE, or
# remembered paths).
#
# Honoured: BMBOX (binary, default ./bmbox), BMBOX_HOME, LIBVIRT_DEFAULT_URI,
# E2E_CIDR (default 172.30.90.0/24), E2E_REDFISH_PORT (8390),
# E2E_IPMI_PORT (6390), E2E_KERNEL / E2E_INITRD (netbooted kernel and
# initrd, default the host's /boot/vmlinuz and /boot/initrd.img; the kernel
# must print to ttyS0).
set -u -o pipefail

BMBOX=${BMBOX:-./bmbox}
LAB=e2e
CIDR=${E2E_CIDR:-172.30.90.0/24}
REDFISH_PORT=${E2E_REDFISH_PORT:-8390}
IPMI_PORT=${E2E_IPMI_PORT:-6390}
KERNEL=${E2E_KERNEL:-/boot/vmlinuz}
INITRD=${E2E_INITRD:-/boot/initrd.img}
URI=${LIBVIRT_DEFAULT_URI:-qemu:///system}
WS=${BMBOX_HOME:-/var/lib/bmbox}
# Pass BMBOX_HOME on only when set, so the default workspace is what is tested.
HOMEENV=${BMBOX_HOME:+BMBOX_HOME=$BMBOX_HOME}
STATE=$WS/labs/$LAB/state.json
LOCK=$WS/labs/$LAB/.lock
NS=https://bmbox.io/xmlns/libvirt/v1

die() { echo "e2e: $*" >&2; exit 2; }

# ---- preflight --------------------------------------------------------------
[ -x "$BMBOX" ] || die "bmbox binary not found at $BMBOX (set BMBOX=path/to/bmbox)"
BMBOX=$(cd "$(dirname "$BMBOX")" && pwd)/$(basename "$BMBOX")
for tool in sudo virsh curl ipmitool flock python3 ip systemctl; do
	command -v "$tool" >/dev/null || die "missing required tool: $tool"
done
sudo -n true 2>/dev/null || sudo true || die "sudo is required"
for f in "$KERNEL" "$INITRD"; do
	sudo test -r "$f" || die "netboot file $f not found (set E2E_KERNEL / E2E_INITRD)"
done
host_ip() { python3 -c "import ipaddress,sys; print(ipaddress.ip_network(sys.argv[1], strict=False)[int(sys.argv[2])])" "$CIDR" "$1"; }
N1_IP=$(host_ip 10)
EP_A=$(host_ip 200) # test endpoints on the pxe bridge (network namespaces)
EP_B=$(host_ip 201)
PXE_BR=bmb-$LAB-pxe

TMP=$(mktemp -d)
# Test-only firewall rule and namespaces, removed however the run ends.
fwd_cleanup() {
	while sudo iptables -w -D FORWARD -i "$PXE_BR" -m comment --comment bmbox-e2e-drop -j DROP 2>/dev/null; do :; done
	for n in a b; do sudo ip netns del "bmbox-e2e-$n" 2>/dev/null; done
}
trap 'fwd_cleanup; rm -rf "$TMP"' EXIT
OUT=$TMP/out
FULL=$TMP/full.yaml
SMALL=$TMP/small.yaml

cat >"$FULL" <<EOF
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata:
  name: $LAB
spec:
  networks:
    - name: pxe
      cidr: $CIDR
      dhcp: {}
    - name: data
  netboot:
    kernel: $KERNEL
    initrd: $INITRD
    cmdline: console=ttyS0
  nodes:
    - name: n1
      memory: 1GiB
      disks: [{size: 40GiB}, {size: 2GiB}]
      nics: [{network: pxe, ip: $N1_IP}, {network: data}]
      bmc: {type: redfish, port: $REDFISH_PORT}
    - name: n2
      memory: 1GiB
      nics: [{network: pxe}]
      bmc: {type: ipmi, port: $IPMI_PORT}
EOF
# The same lab without n2, the data network, n1's 2nd disk and DHCP.
cat >"$SMALL" <<EOF
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata:
  name: $LAB
spec:
  networks:
    - name: pxe
      cidr: $CIDR
  nodes:
    - name: n1
      memory: 1GiB
      disks: [{size: 40GiB}]
      nics: [{network: pxe}]
      bmc: {type: redfish, port: $REDFISH_PORT}
EOF

# ---- helpers ----------------------------------------------------------------
# bmbox and virsh always see the same workspace and libvirt.
bm() {
	sudo env $HOMEENV LIBVIRT_DEFAULT_URI="$URI" \
		BMBOX_SUSHY_EMULATOR="${BMBOX_SUSHY_EMULATOR:-}" BMBOX_VBMC="${BMBOX_VBMC:-}" \
		BMBOX_DNSMASQ="${BMBOX_DNSMASQ:-}" BMBOX_IPXE="${BMBOX_IPXE:-}" \
		"$BMBOX" "$@"
}
vsh() { sudo virsh -c "$URI" "$@"; }

pass=0
fail=0
check() {
	local desc=$1
	shift
	if "$@" >"$OUT" 2>&1; then
		printf 'PASS  %s\n' "$desc"
		pass=$((pass + 1))
	else
		printf 'FAIL  %s\n' "$desc"
		tail -5 "$OUT" | sed 's/^/      /'
		fail=$((fail + 1))
	fi
}
section() { printf '\n== %s\n' "$1"; }
wait_for() { # seconds command...
	local t=$1
	shift
	for _ in $(seq "$t"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
	return 1
}

# Lab ownership is read from bmbox's own marks, never from name prefixes:
# lab "e2e" must not be confused with a lab called "e2e-x".
lab_of_domain() { vsh metadata "$1" "$NS" 2>/dev/null | sed -n 's/.* lab="\([^"]*\)".*/\1/p'; }
domains() { vsh list --all --name | awk 'NF {print $1}'; }
e2e_domains() { for d in $(domains); do [ "$(lab_of_domain "$d")" = "$LAB" ] && echo "$d"; done; }
other_domains() { for d in $(domains); do [ "$(lab_of_domain "$d")" != "$LAB" ] && echo "$d"; done | sort; }
e2e_bridges() { ip -o link show type bridge | grep -F "alias bmbox:$LAB/" | awk -F': ' '{print $2}'; }
other_bridges() { ip -o link show type bridge | grep -vF "alias bmbox:$LAB/" | awk -F': ' '{print $2}' | sort; }
# Unit descriptions end in "for <lab>/<node>" (BMC) or "for <lab>" (dnsmasq).
units() { systemctl list-units 'bmbox-*' --all --no-legend --plain; }
e2e_units() { units | grep -E " for $LAB(/[^ ]*)? *\$" | awk '{print $1}'; }
other_units() { units | grep -vE " for $LAB(/[^ ]*)? *\$" | awk '{print $1, $3, $4}' | sort; }
DHCP_UNIT=bmbox-$LAB-dnsmasq.service
DHCP_DIR=/var/lib/bmbox/$LAB
dhcp_active() { systemctl is-active --quiet "$DHCP_UNIT"; }
# virsh pads --name output with trailing spaces: trim before comparing.
pools() { vsh pool-list --all --name | awk 'NF {print $1}'; }
e2e_pool() { pools | grep -x "bmbox-$LAB"; }
e2e_rules() { for t in iptables ip6tables; do sudo $t -w -S BMBOX-FORWARD 2>/dev/null | grep -F "bmbox:$LAB/"; done; }
leftovers() { { e2e_domains; e2e_bridges; e2e_units; e2e_pool; sudo ls -d "$DHCP_DIR" 2>/dev/null; e2e_rules; } | grep -c .; }

# Everything outside the e2e lab that bmbox could touch.
host_snapshot() {
	echo "## domains"; other_domains
	echo "## pools"; pools | grep -vx "bmbox-$LAB" | sort
	echo "## bridges"; other_bridges
	echo "## bmbox units"; other_units
	echo "## bmbox data"; sudo ls /var/lib/bmbox 2>/dev/null | grep -vxE "$LAB|labs|tools|tools.json|ca"
	for t in iptables ip6tables; do
		echo "## $t FORWARD"
		sudo $t -w -S FORWARD 2>/dev/null
		sudo $t -w -S BMBOX-FORWARD 2>/dev/null | grep -vF "bmbox:$LAB/"
	done
	echo "## sysctls"
	for k in net.ipv4.ip_forward net.bridge.bridge-nf-call-iptables net.bridge.bridge-nf-call-ip6tables; do
		echo "$k=$(sysctl -n "$k" 2>/dev/null)"
	done
}

state() { # python expression over the state document s
	sudo cat "$STATE" | python3 -c "import json,sys; s=json.load(sys.stdin); print($1)"
}
node_state() { state "[n for n in s['nodes'] if n['name']=='$1'][0]$2"; }

redfish() { curl -s --cacert "$CA" -u "admin:$N1_PASS" "$@"; }
power_state() { redfish "$RF/Systems/$UUID1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["PowerState"])'; }
reset() {
	[ "$(redfish -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
		-X POST -d "{\"ResetType\":\"$1\"}" "$RF/Systems/$UUID1/Actions/ComputerSystem.Reset")" = 204 ]
}
ipmi() { ipmitool -I lanplus -H 127.0.0.1 -p "$IPMI_PORT" -U admin -P "${IPMI_PASS:-$N2_PASS}" "$@"; }
is_power() { [ "$(power_state)" = "$1" ]; }

console_to() { # file seconds: attach to n1 without input, keep the output
	# Replay the whole boot: n1 has netbooted, the firmware lines are far
	# above the default 20-line tail.
	sudo timeout "$2" env $HOMEENV "$BMBOX" console n1 --lab "$LAB" -n 5000 </dev/null >"$1" 2>&1
	return 0
}
replay_is_clean() { # file: has a replay, and the replay (not live output) never clears
	python3 - "$1" <<'PY'
import sys
data = open(sys.argv[1], "rb").read()
replay, sep, _ = data.partition(b"--- live ---")
sys.exit(0 if sep and b"\x1b[2J" not in replay and b"Start PXE" in replay else 1)
PY
}

lock_free() { sudo flock -n "$LOCK" true; }

# ---- run ----------------------------------------------------------------------
echo "bmbox e2e: lab=$LAB workspace=$WS libvirt=$URI ($("$BMBOX" --version))"

section "Clean start"
bm destroy --lab "$LAB" >/dev/null 2>&1
check "no resources of lab $LAB on the host" test "$(leftovers)" -eq 0
host_snapshot >"$TMP/before"

section "Topology"
check "validate" "$BMBOX" validate -f "$FULL"
check "render n1 is UEFI q35 with a serial socket" \
	bash -c "'$BMBOX' render n1 -f '$FULL' | grep -q 'type=\"pflash\"' && '$BMBOX' render n1 -f '$FULL' | grep -q 'serial type=\"unix\"'"

section "up"
T0=$(date +%s) # journal lines of this run only
check "up succeeds" bm up -f "$FULL"
check "both domains defined and shut off" \
	bash -c "[ \"\$(sudo virsh -c $URI domstate bmbox-$LAB-n1)\" = 'shut off' ] && [ \"\$(sudo virsh -c $URI domstate bmbox-$LAB-n2)\" = 'shut off' ]"
check "disks are sparse (40 GiB disk allocates < 1 MiB)" \
	bash -c "[ \$(sudo virsh -c $URI vol-info --pool bmbox-$LAB n1-disk0.qcow2 --bytes | awk '/Allocation/{print \$2}') -lt 1048576 ]"
check "each node has its own NVRAM" \
	bash -c "sudo virsh -c $URI vol-list bmbox-$LAB | grep -q n1-VARS.fd && sudo virsh -c $URI vol-list bmbox-$LAB | grep -q n2-VARS.fd"
check "both bridges carry the ownership alias" test "$(e2e_bridges | wc -l)" -eq 2
check "both BMC units are active" \
	bash -c "[ \$(systemctl list-units 'bmbox-$LAB-*-bmc.service' --state=active --no-legend | grep -cF ' for $LAB/') -eq 2 ]"
bm status --lab "$LAB" >"$TMP/status" 2>&1
check "status lists n1" grep -q '^n1 ' "$TMP/status"
check "status lists n2" grep -q '^n2 ' "$TMP/status"
check "dnsmasq unit is active" dhcp_active
check "status reports DHCP active" grep -q "^DHCP: active ($DHCP_UNIT)" "$TMP/status"
check "TFTP root holds iPXE, script, kernel and initrd" \
	bash -c "[ \"\$(sudo ls $DHCP_DIR/tftp | sort | tr '\n' ' ')\" = 'boot.ipxe initrd ipxe.efi vmlinuz ' ]"

UUID1=$(node_state n1 "['uuid']")
RF=$(node_state n1 "['bmc']['endpoint'].split('/Systems/')[0]")
[ -n "$UUID1" ] && [ -n "$RF" ] || die "could not read n1's UUID/BMC from $STATE"
bm credentials --lab "$LAB" -o json >"$TMP/creds" 2>&1
cred() { python3 -c "import json,sys; print([c for c in json.load(open(sys.argv[1])) if c['node']==sys.argv[2]][0][sys.argv[3]])" "$TMP/creds" "$1" "$2"; }
N1_PASS=$(cred n1 password)
N2_PASS=$(cred n2 password)
CA=$(cred n1 caCert)
check "BMC passwords are generated (16 characters, one per node)" \
	bash -c "[ ${#N1_PASS} -eq 16 ] && [ ${#N2_PASS} -eq 16 ] && [ '$N1_PASS' != '$N2_PASS' ]"
check "credentials need root" \
	bash -c "'$BMBOX' credentials --lab $LAB 2>&1 | grep -q 'root only'"
export RF UUID1 IPMI_PORT N1_PASS N2_PASS CA
export -f redfish power_state ipmi is_power

section "Redfish (n1)"
check "endpoint is HTTPS, signed by the bmbox CA" \
	bash -c "[[ \$RF == https://* ]] && curl -sf --cacert '$CA' -u 'admin:$N1_PASS' -o /dev/null '$RF/Systems/$UUID1'"
check "certificate is not trusted without the CA" bash -c "! curl -s -o /dev/null '$RF/Systems/$UUID1'"
check "unauthenticated request is rejected (401)" \
	test "$(curl -s --cacert "$CA" -o /dev/null -w '%{http_code}' "$RF/Systems/$UUID1")" = 401
check "Systems collection holds only this node" \
	bash -c "redfish \$RF/Systems | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d[\"Members@odata.count\"]==1, d'"
check "PowerState is Off" is_power Off
check "Reset On" reset On
check "PowerState becomes On" wait_for 15 is_power On

section "Netboot (n1)"
n1_leased() { sudo env $HOMEENV LIBVIRT_DEFAULT_URI="$URI" "$BMBOX" status --lab "$LAB" | grep -qE "^n1 +running +$N1_IP "; }
served() { sudo journalctl -u "$DHCP_UNIT" --since "@$T0" --no-pager | grep -q "sent $DHCP_DIR/tftp/$1 to $N1_IP"; }
check "n1 gets its fixed address $N1_IP (status)" wait_for 60 n1_leased
check "firmware loads iPXE over TFTP" wait_for 30 served ipxe.efi
ipxe_served() { served boot.ipxe && served vmlinuz && served initrd; }
check "iPXE loads boot.ipxe, kernel and initrd" wait_for 60 ipxe_served
check "the netbooted kernel prints on the serial console" wait_for 60 sudo grep -q "Linux version" "/run/bmbox/$LAB/n1.serial.log"

section "console (n1)"
console_to "$TMP/con1" 15
check "console shows the firmware starting PXE" grep -q 'Start PXE over IPv4' "$TMP/con1"
console_to "$TMP/con2" 3
check "re-attach replays history without clearing the screen" replay_is_clean "$TMP/con2"
check "Reset ForceOff" reset ForceOff
check "PowerState becomes Off" wait_for 15 is_power Off
check "console on a powered-off node prints the BMC power-on command" \
	bash -c "! sudo env $HOMEENV '$BMBOX' console n1 --lab $LAB </dev/null >'$OUT.c' 2>&1 && grep -q 'ResetType' '$OUT.c'"

section "IPMI (n2)"
check "power status off" bash -c 'ipmi power status | grep -q "is off"'
check "set boot device PXE" ipmi chassis bootdev pxe
check "power on" ipmi power on
check "domain is running" wait_for 15 bash -c "[ \"\$(sudo virsh -c $URI domstate bmbox-$LAB-n2)\" = running ]"
check "power off" ipmi power off
check "domain is shut off" wait_for 15 bash -c "[ \"\$(sudo virsh -c $URI domstate bmbox-$LAB-n2)\" = 'shut off' ]"
check "wrong password is rejected" bash -c '! IPMI_PASS=wrong ipmi power status'

section "Convergence"
bm up -f "$FULL" >"$TMP/up2" 2>&1
check "second up succeeds" grep -q 'is up' "$TMP/up2"
check "second up leaves running BMCs and dnsmasq alone" \
	bash -c "grep -q 'n1 *redfish *running' '$TMP/up2' && grep -q 'n2 *ipmi *running' '$TMP/up2' && grep -q 'dnsmasq *running' '$TMP/up2' && ! grep -q started '$TMP/up2'"
bm up -f "$SMALL" >"$TMP/up3" 2>&1
check "smaller topology: orphans are reported" \
	bash -c "grep -q 'node    n2' '$TMP/up3' && grep -q 'network data' '$TMP/up3' && grep -q 'volume  n1-disk1.qcow2' '$TMP/up3' && grep -q 'dhcp    $DHCP_UNIT' '$TMP/up3' && grep -q 'firewall rule for data' '$TMP/up3'"
check "orphans are kept without --prune" vsh domstate "bmbox-$LAB-n2"
check "up --prune succeeds" bm up -f "$SMALL" --prune
check "removed node is gone" bash -c "! sudo virsh -c $URI domstate bmbox-$LAB-n2"
check "removed BMC unit is gone" bash -c "! systemctl is-active --quiet bmbox-$LAB-n2-bmc.service"
check "removed disk is gone" bash -c "! sudo virsh -c $URI vol-list bmbox-$LAB | grep -q n1-disk1.qcow2"
check "removed network's bridge is gone" bash -c "! ip -o link show type bridge | grep -qF 'alias bmbox:$LAB/data'"
check "removed network's firewall rule is gone" bash -c "! sudo iptables -w -S BMBOX-FORWARD 2>/dev/null | grep -qF 'bmbox:$LAB/data'"
check "dnsmasq stopped and its data removed" bash -c "! systemctl is-active --quiet $DHCP_UNIT && ! sudo test -e $DHCP_DIR"
check "back to the full topology" bm up -f "$FULL"
check "dnsmasq is back" dhcp_active

section "Forwarding (bridge-nf-call-iptables)"
rule_args=(-i "$PXE_BR" -o "$PXE_BR" -m comment --comment "bmbox:$LAB/pxe" -j ACCEPT)
check "FORWARD jumps to BMBOX-FORWARD" sudo iptables -w -C FORWARD -j BMBOX-FORWARD
check "IPv4 rule accepts traffic within the pxe bridge" sudo iptables -w -C BMBOX-FORWARD "${rule_args[@]}"
check "IPv6 rule accepts traffic within the pxe bridge" sudo ip6tables -w -C BMBOX-FORWARD "${rule_args[@]}"
# Two endpoints on the lab bridge, and a DROP for the bridge's traffic at
# the end of FORWARD: what a DROP policy does, without touching the host's.
for n in a b; do
	sudo ip netns add "bmbox-e2e-$n"
	sudo ip link add "bmbx-e2e-$n" type veth peer name eth0 netns "bmbox-e2e-$n"
	sudo ip link set "bmbx-e2e-$n" master "$PXE_BR" up
	sudo ip -n "bmbox-e2e-$n" link set eth0 up
done
sudo ip -n bmbox-e2e-a addr add "$EP_A/${CIDR#*/}" dev eth0
sudo ip -n bmbox-e2e-b addr add "$EP_B/${CIDR#*/}" dev eth0
sudo iptables -w -A FORWARD -i "$PXE_BR" -m comment --comment bmbox-e2e-drop -j DROP
ep_ping() { sudo ip netns exec bmbox-e2e-a ping -c2 -W1 "$EP_B"; }
check "endpoints on the bridge reach each other despite the FORWARD drop" wait_for 5 ep_ping
if [ "$(sysctl -n net.bridge.bridge-nf-call-iptables 2>/dev/null)" = 1 ]; then
	sudo iptables -w -D BMBOX-FORWARD "${rule_args[@]}"
	check "control: without bmbox's rule the same traffic is dropped" bash -c "! sudo ip netns exec bmbox-e2e-a ping -c2 -W1 $EP_B"
	check "up restores the rule" bm up -f "$FULL"
	check "traffic flows again" wait_for 5 ep_ping
else
	echo "SKIP  control check: bridge-nf-call-iptables is not 1 on this host"
fi
fwd_cleanup

section "Locking"
# The workspace belongs to root: hold the lab lock from a root process.
sudo flock -n "$LOCK" sleep 20 &
LOCKER=$!
check "test holds the lab lock" wait_for 5 bash -c "! sudo flock -n '$LOCK' true"
check "a second command on the same lab is refused" \
	bash -c "sudo env $HOMEENV '$BMBOX' up -f '$FULL' 2>&1 | grep -q 'already running'"
sudo pkill -f "flock -n $LOCK sleep" 2>/dev/null
wait "$LOCKER" 2>/dev/null
check "lock released" wait_for 5 lock_free

section "destroy"
check "destroy succeeds" bm destroy --lab "$LAB"
check "nothing of lab $LAB is left" test "$(leftovers)" -eq 0
check "workspace of lab $LAB removed" test ! -e "$WS/labs/$LAB"
host_snapshot >"$TMP/after"
check "rest of the host unchanged (domains, pools, bridges, units, sysctls)" diff "$TMP/before" "$TMP/after"
check "second destroy reports there is nothing to do" \
	bash -c "! sudo env $HOMEENV LIBVIRT_DEFAULT_URI='$URI' '$BMBOX' destroy --lab $LAB >'$OUT.d' 2>&1 && grep -q 'nothing found' '$OUT.d'"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]

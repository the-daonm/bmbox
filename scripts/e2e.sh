#!/usr/bin/env bash
# End-to-end test of bmbox on a real libvirt host.
#
#   BMBOX=bin/bmbox scripts/e2e.sh
#
# The test brings up its own lab, "e2e" (two nodes: n1 with a Redfish BMC,
# n2 with an IPMI BMC), on its own subnet and ports, so other labs on the
# host are left alone and checked to stay that way. Needs sudo, virsh,
# curl, ipmitool, flock and python3, plus sushy-tools and virtualbmc as for
# `bmbox up` (BMBOX_SUSHY_EMULATOR / BMBOX_VBMC, or remembered paths).
#
# Honoured: BMBOX (binary, default ./bmbox), BMBOX_HOME, LIBVIRT_DEFAULT_URI,
# E2E_CIDR (default 172.30.90.0/24), E2E_REDFISH_PORT (8390),
# E2E_IPMI_PORT (6390).
set -u -o pipefail

BMBOX=${BMBOX:-./bmbox}
LAB=e2e
CIDR=${E2E_CIDR:-172.30.90.0/24}
REDFISH_PORT=${E2E_REDFISH_PORT:-8390}
IPMI_PORT=${E2E_IPMI_PORT:-6390}
URI=${LIBVIRT_DEFAULT_URI:-qemu:///system}
WS=${BMBOX_HOME:-$HOME/.bmbox}
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

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
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
    - name: data
  nodes:
    - name: n1
      memory: 1GiB
      disks: [{size: 40GiB}, {size: 2GiB}]
      nics: [{network: pxe}, {network: data}]
      bmc: {type: redfish, port: $REDFISH_PORT}
    - name: n2
      memory: 1GiB
      nics: [{network: pxe}]
      bmc: {type: ipmi, port: $IPMI_PORT}
EOF
# The same lab without n2, without the data network and without n1's 2nd disk.
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
	sudo env BMBOX_HOME="$WS" LIBVIRT_DEFAULT_URI="$URI" \
		BMBOX_SUSHY_EMULATOR="${BMBOX_SUSHY_EMULATOR:-}" BMBOX_VBMC="${BMBOX_VBMC:-}" \
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
e2e_units() { systemctl list-units 'bmbox-*' --all --no-legend --plain | grep -F " for $LAB/" | awk '{print $1}'; }
other_units() { systemctl list-units 'bmbox-*' --all --no-legend --plain | grep -vF " for $LAB/" | awk '{print $1, $3, $4}' | sort; }
# virsh pads --name output with trailing spaces: trim before comparing.
pools() { vsh pool-list --all --name | awk 'NF {print $1}'; }
e2e_pool() { pools | grep -x "bmbox-$LAB"; }
leftovers() { { e2e_domains; e2e_bridges; e2e_units; e2e_pool; } | grep -c .; }

# Everything outside the e2e lab that bmbox could touch.
host_snapshot() {
	echo "## domains"; other_domains
	echo "## pools"; pools | grep -vx "bmbox-$LAB" | sort
	echo "## bridges"; other_bridges
	echo "## bmbox units"; other_units
	echo "## sysctls"
	for k in net.ipv4.ip_forward net.bridge.bridge-nf-call-iptables net.bridge.bridge-nf-call-ip6tables; do
		echo "$k=$(sysctl -n "$k" 2>/dev/null)"
	done
}

state() { # python expression over the state document s
	sudo cat "$STATE" | python3 -c "import json,sys; s=json.load(sys.stdin); print($1)"
}
node_state() { state "[n for n in s['nodes'] if n['name']=='$1'][0]$2"; }

redfish() { curl -s -u admin:password "$@"; }
power_state() { redfish "$RF/Systems/$UUID1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["PowerState"])'; }
reset() {
	[ "$(redfish -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
		-X POST -d "{\"ResetType\":\"$1\"}" "$RF/Systems/$UUID1/Actions/ComputerSystem.Reset")" = 204 ]
}
ipmi() { ipmitool -I lanplus -H 127.0.0.1 -p "$IPMI_PORT" -U admin -P "${IPMI_PASS:-password}" "$@"; }
is_power() { [ "$(power_state)" = "$1" ]; }

console_to() { # file seconds: attach to n1 without input, keep the output
	sudo timeout "$2" env BMBOX_HOME="$WS" "$BMBOX" console n1 --lab "$LAB" </dev/null >"$1" 2>&1
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

lock_free() { flock -n "$LOCK" true; }

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

UUID1=$(node_state n1 "['uuid']")
RF=$(node_state n1 "['bmc']['endpoint'].split('/Systems/')[0]")
[ -n "$UUID1" ] && [ -n "$RF" ] || die "could not read n1's UUID/BMC from $STATE"
export RF UUID1 IPMI_PORT
export -f redfish power_state ipmi is_power

section "Redfish (n1)"
check "unauthenticated request is rejected (401)" \
	test "$(curl -s -o /dev/null -w '%{http_code}' "$RF/Systems/$UUID1")" = 401
check "Systems collection holds only this node" \
	bash -c "redfish \$RF/Systems | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d[\"Members@odata.count\"]==1, d'"
check "PowerState is Off" is_power Off
check "Reset On" reset On
check "PowerState becomes On" wait_for 15 is_power On

section "console (n1)"
console_to "$TMP/con1" 15
check "console shows the firmware starting PXE" grep -q 'Start PXE over IPv4' "$TMP/con1"
console_to "$TMP/con2" 3
check "re-attach replays history without clearing the screen" replay_is_clean "$TMP/con2"
check "Reset ForceOff" reset ForceOff
check "PowerState becomes Off" wait_for 15 is_power Off
check "console on a powered-off node prints the BMC power-on command" \
	bash -c "! sudo env BMBOX_HOME='$WS' '$BMBOX' console n1 --lab $LAB </dev/null >'$OUT.c' 2>&1 && grep -q 'ResetType' '$OUT.c'"

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
check "second up leaves running BMCs alone" \
	bash -c "grep -q 'n1 *redfish *running' '$TMP/up2' && grep -q 'n2 *ipmi *running' '$TMP/up2' && ! grep -q started '$TMP/up2'"
bm up -f "$SMALL" >"$TMP/up3" 2>&1
check "smaller topology: orphans are reported" \
	bash -c "grep -q 'node    n2' '$TMP/up3' && grep -q 'network data' '$TMP/up3' && grep -q 'volume  n1-disk1.qcow2' '$TMP/up3'"
check "orphans are kept without --prune" vsh domstate "bmbox-$LAB-n2"
check "up --prune succeeds" bm up -f "$SMALL" --prune
check "removed node is gone" bash -c "! sudo virsh -c $URI domstate bmbox-$LAB-n2"
check "removed BMC unit is gone" bash -c "! systemctl is-active --quiet bmbox-$LAB-n2-bmc.service"
check "removed disk is gone" bash -c "! sudo virsh -c $URI vol-list bmbox-$LAB | grep -q n1-disk1.qcow2"
check "removed network's bridge is gone" bash -c "! ip -o link show type bridge | grep -qF 'alias bmbox:$LAB/data'"
check "back to the full topology" bm up -f "$FULL"

section "Locking"
# Hold the lab lock from this shell on fd 9 (no child process can keep it).
exec 9>>"$LOCK"
check "test holds the lab lock" flock -n 9
check "a second command on the same lab is refused" \
	bash -c "sudo env BMBOX_HOME='$WS' '$BMBOX' up -f '$FULL' 9>&- 2>&1 | grep -q 'already running'"
exec 9>&-
check "lock released" lock_free

section "destroy"
check "destroy succeeds" bm destroy --lab "$LAB"
check "nothing of lab $LAB is left" test "$(leftovers)" -eq 0
check "workspace of lab $LAB removed" test ! -e "$WS/labs/$LAB"
host_snapshot >"$TMP/after"
check "rest of the host unchanged (domains, pools, bridges, units, sysctls)" diff "$TMP/before" "$TMP/after"
check "second destroy reports there is nothing to do" \
	bash -c "! sudo env BMBOX_HOME='$WS' LIBVIRT_DEFAULT_URI='$URI' '$BMBOX' destroy --lab $LAB >'$OUT.d' 2>&1 && grep -q 'nothing found' '$OUT.d'"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]

#!/usr/bin/env bash
# End-to-end test of bmbox on a real libvirt host.
#
# Run from a directory holding the bmbox binary and a topology with a
# Redfish node first and an IPMI node second (examples/topology.yaml):
#
#   BMBOX_SUSHY_EMULATOR=~/venv/bin/sushy-emulator BMBOX_VBMC=~/venv/bin/vbmc \
#     ./e2e.sh
#
# It destroys the lab named in the topology first, so do not point it at a
# lab you want to keep. Needs sudo, curl, ipmitool and python3.
set -u

BMBOX=${BMBOX:-./bmbox}
TOPO=${TOPO:-topology.yaml}
LAB=$(awk '/^metadata:/{m=1} m && $1=="name:" {print $2; exit}' "$TOPO")
NODE1=$(awk '/^  nodes:/{n=1} n && $1=="-" && $2=="name:" {print $3; exit}' "$TOPO")
NODE2=$(awk '/^  nodes:/{n=1} n && $1=="-" && $2=="name:" {c++; if (c==2) {print $3; exit}}' "$TOPO")
OUT=$(mktemp)
SMALL=$(mktemp --suffix=.yaml)
trap 'rm -f "$OUT" "$SMALL"' EXIT

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

bm() { sudo -E "$BMBOX" "$@"; }
count_bmbox() {
	echo $(($(sudo virsh list --all --name | grep -c "^bmbox-$LAB-") +
		$(sudo virsh pool-list --all --name | grep -c "^bmbox-$LAB$") +
		$(ip -o link show type bridge | grep -c "alias bmbox:$LAB/") +
		$(systemctl list-units "bmbox-$LAB-*" --all --no-legend | wc -l)))
}
uuid_of() {
	python3 -c "import json,sys; print([n['uuid'] for n in json.load(open(sys.argv[1]))['nodes'] if n['name']==sys.argv[2]][0])" \
		"$HOME/.bmbox/labs/$LAB/state.json" "$1"
}
redfish() { curl -s -u admin:password "$@"; }
power_state() { redfish "$RF/Systems/$UUID1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["PowerState"])'; }
reset() {
	[ "$(redfish -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
		-X POST -d "{\"ResetType\":\"$1\"}" "$RF/Systems/$UUID1/Actions/ComputerSystem.Reset")" = 204 ]
}
ipmi() { ipmitool -I lanplus -H 127.0.0.1 -p "$IPMI_PORT" -U admin -P "${IPMI_PASS:-password}" "$@"; }
wait_for() { # seconds command...
	local t=$1
	shift
	for _ in $(seq "$t"); do "$@" && return 0; sleep 1; done
	return 1
}

echo "bmbox e2e: lab=$LAB nodes=$NODE1,$NODE2 topology=$TOPO ($("$BMBOX" --version))"
others_before=$(sudo virsh list --all --name | grep -v "^bmbox-$LAB-" | sort)

section "Clean start"
bm destroy -f "$TOPO" >/dev/null 2>&1
check "no resources of lab $LAB on the host" test "$(count_bmbox)" -eq 0

section "Topology"
check "validate" "$BMBOX" validate -f "$TOPO"
check "render $NODE1 is UEFI q35 with serial socket" \
	sh -c "'$BMBOX' render $NODE1 -f '$TOPO' | grep -q 'type=\"pflash\"' && '$BMBOX' render $NODE1 -f '$TOPO' | grep -q 'serial type=\"unix\"'"

section "up"
check "up succeeds" bm up -f "$TOPO"
check "both domains defined and shut off" \
	sh -c "[ \"\$(sudo virsh domstate bmbox-$LAB-$NODE1)\" = 'shut off' ] && [ \"\$(sudo virsh domstate bmbox-$LAB-$NODE2)\" = 'shut off' ]"
check "disks are sparse (40 GiB disk allocates < 1 MiB)" \
	sh -c "[ \$(sudo virsh vol-info --pool bmbox-$LAB $NODE1-disk0.qcow2 --bytes | awk '/Allocation/{print \$2}') -lt 1048576 ]"
check "each node has its own NVRAM" \
	sh -c "sudo virsh vol-list bmbox-$LAB | grep -q $NODE1-VARS.fd && sudo virsh vol-list bmbox-$LAB | grep -q $NODE2-VARS.fd"
check "bridges carry the bmbox ownership alias" \
	sh -c "ip -o link show type bridge | grep -q 'alias bmbox:$LAB/'"
check "BMC units are active" \
	sh -c "[ \$(systemctl list-units 'bmbox-$LAB-*-bmc.service' --state=active --no-legend | wc -l) -eq 2 ]"
check "status lists both nodes" sh -c "sudo '$BMBOX' status -f '$TOPO' | grep -q '^$NODE2 '"

UUID1=$(uuid_of "$NODE1")
RF=$(python3 -c "import json,sys; n=[n for n in json.load(open(sys.argv[1]))['nodes'] if n['name']==sys.argv[2]][0]; print(n['bmc']['endpoint'].split('/Systems/')[0])" \
	"$HOME/.bmbox/labs/$LAB/state.json" "$NODE1")
IPMI_PORT=$(python3 -c "import json,sys; print([n['bmc']['port'] for n in json.load(open(sys.argv[1]))['nodes'] if n['name']==sys.argv[2]][0])" \
	"$HOME/.bmbox/labs/$LAB/state.json" "$NODE2")

export RF UUID1 IPMI_PORT
export -f redfish power_state ipmi

section "Redfish ($NODE1)"
check "unauthenticated request is rejected (401)" \
	sh -c "[ \"\$(curl -s -o /dev/null -w '%{http_code}' $RF/Systems/$UUID1)\" = 401 ]"
check "Systems collection holds only this node" \
	sh -c "curl -s -u admin:password $RF/Systems | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d[\"Members@odata.count\"]==1, d'"
check "PowerState is Off" bash -c '[ "$(power_state)" = Off ]'
check "Reset On" reset On
check "PowerState becomes On" wait_for 10 bash -c '[ "$(power_state)" = On ]'

section "console ($NODE1)"
check "console shows the firmware starting PXE" \
	sh -c "sudo timeout 20 '$BMBOX' console $NODE1 -f '$TOPO' </dev/null 2>&1 | grep -q 'Start PXE over IPv4'"
check "re-attach replays history without clearing the screen" \
	bash -c "out=\$(sudo timeout 3 '$BMBOX' console $NODE1 -f '$TOPO' </dev/null 2>&1); grep -q -- '--- live ---' <<<\"\$out\" && ! grep -q \$'\\x1b\\[2J' <<<\"\$out\""
check "Reset ForceOff" reset ForceOff
check "PowerState becomes Off" wait_for 10 bash -c '[ "$(power_state)" = Off ]'
check "console on a powered-off node prints the BMC power-on command" \
	sh -c "sudo '$BMBOX' console $NODE1 -f '$TOPO' </dev/null 2>&1 | grep -q 'ResetType'"

section "IPMI ($NODE2)"
check "power status off" bash -c 'ipmi power status | grep -q off'
check "set boot device PXE" ipmi chassis bootdev pxe
check "power on" ipmi power on
check "domain is running" wait_for 10 sh -c "[ \"\$(sudo virsh domstate bmbox-$LAB-$NODE2)\" = running ]"
check "power off" ipmi power off
check "wrong password is rejected" bash -c '! IPMI_PASS=wrong ipmi power status'

section "Convergence"
check "second up leaves running BMCs alone" \
	sh -c "out=\$(sudo -E '$BMBOX' up -f '$TOPO'); echo \"\$out\" | grep -q 'redfish  running' && echo \"\$out\" | grep -q 'ipmi     running' && ! echo \"\$out\" | grep -q started"
# Same lab without the second node and the second network.
python3 - "$TOPO" "$SMALL" "$NODE2" <<'EOF'
import sys, re
src, dst, drop = sys.argv[1:]
text = open(src).read()
nodes = text.split("\n  nodes:\n", 1)
blocks = re.split(r"(?m)^(?=    - name: )", nodes[1])
keep = [b for b in blocks if not b.startswith("    - name: " + drop + "\n")]
head = nodes[0]
head = re.sub(r"(?ms)^    - name: data\b.*?(?=^    - name: |\Z)", "", head)
body = "".join(keep)
body = re.sub(r"(?m)^        - network: data\n", "", body)
open(dst, "w").write(head + "\n  nodes:\n" + body)
EOF
check "up with a smaller topology reports orphans" \
	sh -c "sudo -E '$BMBOX' up -f '$SMALL' | grep -q 'Not in topology any more'"
check "orphans are kept without --prune" sudo virsh domstate "bmbox-$LAB-$NODE2"
check "up --prune removes them" sudo -E "$BMBOX" up -f "$SMALL" --prune
check "removed node is gone" sh -c "! sudo virsh domstate bmbox-$LAB-$NODE2"
check "removed network bridge is gone" sh -c "! ip -o link show type bridge | grep -q 'alias bmbox:$LAB/data'"
check "back to the full topology" sudo -E "$BMBOX" up -f "$TOPO"

section "Locking"
check "a second command on the same lab is refused" \
	sh -c "(flock '$HOME/.bmbox/labs/$LAB/.lock' sleep 4 &) ; sleep 0.5; sudo '$BMBOX' up -f '$TOPO' 2>&1 | grep -q 'already running'"
sleep 4

section "destroy"
check "destroy succeeds" bm destroy -f "$TOPO"
check "nothing of lab $LAB is left" test "$(count_bmbox)" -eq 0
check "workspace of lab $LAB removed" test ! -e "$HOME/.bmbox/labs/$LAB"
check "other domains on the host untouched" \
	test "$others_before" = "$(sudo virsh list --all --name | grep -v "^bmbox-$LAB-" | sort)"
check "destroy again is a no-op error, not a crash" sh -c "! sudo '$BMBOX' destroy --lab $LAB 2>&1 | grep -q panic"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]

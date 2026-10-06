# Hướng dẫn demo bmbox — Tuần 3

Mục tiêu: từ một file `topology.yaml`, bmbox tự động **tạo Linux bridge → tạo storage pool, đĩa sparse QCOW2, NVRAM riêng → define máy ảo UEFI trên libvirt ở trạng thái SHUTOFF**, không gõ lệnh shell thủ công nào.

Môi trường demo: `htc@lab22` (Ubuntu 22.04, libvirt 8.0 chạy trong container Kolla `nova_libvirt`, QEMU 6.2, OVMF).

---

## 0. Chuẩn bị (làm trước buổi demo)

Build binary tĩnh trên máy dev và copy lên lab22:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X bmbox/cmd.version=0.3.0" -o bin/bmbox-linux-amd64 .

ssh htc@lab22 'mkdir -p ~/bmbox-test'
scp bin/bmbox-linux-amd64 htc@lab22:bmbox-test/bmbox
scp examples/topology.yaml htc@lab22:bmbox-test/topology.yaml
```

Đảm bảo lab đang **sạch** để demo từ đầu (xem [mục 7 — Dọn dẹp](#7-dọn-dẹp)):

```bash
ssh htc@lab22
cd ~/bmbox-test
sudo virsh list --all | grep bmbox      # không có dòng nào
ip -br link | grep bmb-                 # không có dòng nào
```

---

## 1. Giới thiệu topology

```bash
cat topology.yaml
```

Điểm cần nói:
- 2 network: `pxe` (có CIDR → host làm gateway `172.30.10.1`) và `data` (L2 thuần).
- 2 node: `node1` (8 GiB RAM, 2 đĩa 40G + 10G, 2 NIC), `node2` (giá trị mặc định, MAC cố định).
- `boot: [network, disk]` — giống server thật: PXE trước, đĩa sau.
- `bmc:` đã được parse/validate, sẽ được dùng ở tuần 4 (sushy-tools).

## 2. Parse & validate

```bash
./bmbox validate
```

Kết quả: bảng network/node sau khi áp giá trị mặc định — tên bridge sinh tự động (`bmb-demo-pxe`), MAC sinh tất định (chạy lại vẫn giống), kích thước đĩa, thứ tự boot.

**Demo bắt lỗi** — validator gom *tất cả* lỗi, chỉ rõ đường dẫn trường:

```bash
cat > bad.yaml <<'EOF'
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: X}
spec:
  nodes:
    - name: a
      memory: 1MiB
      nics: [{network: nope}]
      typo: true
EOF
./bmbox validate -f bad.yaml        # lỗi trường lạ "typo" (strict decode)
sed -i '/typo/d' bad.yaml
./bmbox validate -f bad.yaml        # 3 lỗi: tên lab, memory, network không tồn tại
rm bad.yaml
```

## 3. Xem trước Domain XML (offline, không thay đổi gì)

```bash
./bmbox render node1 | less
```

Chỉ ra trong XML:
- `<type machine="q35">`, `<loader type="pflash">OVMF_CODE_4M.fd` + `<nvram template=...>` → **UEFI**.
- `<boot order="1">` trên NIC, `order="3"` trên đĩa → boot order theo thiết bị.
- `<serial type="unix">` → `/run/bmbox/demo/node1.serial.sock` (tuần 4 dùng cho `bmbox console`).
- `<metadata><bmbox:node lab="demo" name="node1"/>` → đánh dấu sở hữu, bmbox không bao giờ đụng VM không phải của mình.

## 4. `bmbox up` — dựng lab

```bash
sudo ./bmbox up
```

Kết quả mong đợi:

```
==> Networks
    pxe        bridge=bmb-demo-pxe    created  172.30.10.1/24
    data       bridge=bmb-demo-data   created
==> Libvirt 8.0.0 (qemu:///system)
    firmware  loader=/usr/share/OVMF/OVMF_CODE_4M.fd
              vars=/usr/share/OVMF/OVMF_VARS_4M.fd
    pool      bmbox-demo -> /var/lib/libvirt/bmbox/demo
==> Nodes
    node1      bmbox-demo-node1         shutoff  defined
    node2      bmbox-demo-node2         shutoff  defined
```

Ý chính: firmware được **hỏi từ libvirt** (domain capabilities), không hard-code.

## 5. Kiểm chứng kết quả

**Network (tạo qua Netlink):**
```bash
ip -br addr show | grep bmb-
ip -d link show bmb-demo-pxe | grep -oE 'stp_state [0-9]|forward_delay [0-9]+'
cat /proc/sys/net/ipv6/conf/bmb-demo-pxe/disable_ipv6      # 1
ip -d link show bmb-demo-pxe | grep -o 'alias.*'            # bmbox:demo/pxe
```

**Máy ảo ở trạng thái chờ:**
```bash
sudo virsh list --all | grep bmbox        # shut off
sudo virsh domiflist bmbox-demo-node1
```

**Đĩa sparse + NVRAM độc lập cho từng node:**
```bash
sudo virsh vol-list bmbox-demo --details
#  node1-disk0.qcow2  ...  40.00 GiB   196.00 KiB   <- capacity 40G, chiếm thực 196K
#  node1-VARS.fd      ...  528.00 KiB  528.00 KiB   <- NVRAM riêng
```

**Workspace cục bộ:**
```bash
tree ~/.bmbox         # hoặc: find ~/.bmbox
cat ~/.bmbox/labs/demo/state.json
```
`state.json` ghi lại mọi thứ đã tạo (bridge, sysctl cũ/mới, volume, UUID) — là đầu vào cho `bmbox destroy` ở tuần 4.

## 6. Các điểm "kỹ thuật" nên demo thêm

**a) Idempotent** — chạy lại không tạo trùng, không xoá đĩa/NVRAM:
```bash
sudo ./bmbox up        # network "ok", node "updated"
```

**b) An toàn trên host dùng chung** — từ chối đụng vào tài nguyên không phải của bmbox, và kiểm tra *trước khi* tạo bất cứ thứ gì:
```bash
sed 's/name: demo/name: demo2/' topology.yaml > t2.yaml
sudo ./bmbox up -f t2.yaml
# Error: subnet 172.30.10.0/24 of bmb-demo2-pxe overlaps 172.30.10.1/24 already configured on bmb-demo-pxe
ip -br link | grep demo2     # không có gì được tạo
rm -rf t2.yaml ~/.bmbox/labs/demo2
```

**c) Node thật sự boot được UEFI + serial** (smoke test):
```bash
sudo virsh start bmbox-demo-node2
sudo python3 - <<'EOF'
import socket, time, re
s = socket.socket(socket.AF_UNIX); s.connect("/run/bmbox/demo/node2.serial.sock"); s.settimeout(1)
buf, t = b"", time.time()
while time.time() - t < 30:
    try: buf += s.recv(4096)
    except socket.timeout: pass
print(re.sub(r"\x1b\[[0-9;?]*[A-Za-z]", "", buf.decode(errors="replace")))
EOF
# >>Start PXE over IPv4.
sudo virsh destroy bmbox-demo-node2       # tắt lại
```

## 7. Dọn dẹp

> Từ tuần 4: chỉ cần `sudo ./bmbox destroy` (xem [demo-week4.md](demo-week4.md)). Các lệnh dọn tay dưới đây giữ lại để tham khảo.

```bash
for n in node1 node2; do
  sudo virsh destroy bmbox-demo-$n 2>/dev/null
  sudo virsh undefine bmbox-demo-$n --nvram
done
sudo virsh pool-destroy bmbox-demo
sudo virsh pool-delete bmbox-demo
sudo virsh pool-undefine bmbox-demo
sudo ip link del bmb-demo-pxe
sudo ip link del bmb-demo-data
rm -rf ~/.bmbox/labs/demo
```

---

## Ghi chú kiến trúc (để trả lời câu hỏi)

- **Vì sao đĩa không nằm trong `~/.bmbox`?** libvirtd trên lab22 chạy trong container `nova_libvirt`; `/var/lib/libvirt` của nó là docker volume, không thấy `/home` của host (và `/home/htc` là 0750 nên qemu cũng không đọc được trên host thường). Vì vậy đĩa/NVRAM được tạo qua **libvirt Storage Pool API** tại `/var/lib/libvirt/bmbox/<lab>` — chạy được cả với libvirt cài trực tiếp lẫn trong container. `~/.bmbox` giữ topology, state và XML.
- **Vì sao không dùng cgo libvirt-go?** Dùng `digitalocean/go-libvirt` (nói chuyện RPC trực tiếp với libvirtd) → binary tĩnh, build trên máy dev, copy lên server là chạy.
- **Sysctl:** chỉ chỉnh tham số theo từng bridge (`disable_ipv6`); tham số toàn cục chỉ được *nâng* (`ip_forward=1`), không hạ, vì host dùng chung với OpenStack/Docker. Giá trị cũ được lưu để khôi phục.
- **Hạn chế đã biết:** host đặt `net.bridge.bridge-nf-call-iptables=1` toàn cục (OpenStack/Docker cần), cờ per-bridge không ghi đè được → traffic giữa các node đi qua iptables FORWARD. Hiện policy là ACCEPT nên không ảnh hưởng; cần xử lý khi tích hợp DHCP/PXE.

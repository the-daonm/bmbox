# Hướng dẫn demo bmbox — Tuần 4

Mục tiêu: **một lệnh `bmbox up`** dựng lab hoàn chỉnh (mạng, máy ảo UEFI, **BMC Redfish/IPMI cho từng node**), điều khiển nguồn node **qua OOB** như server thật, xem boot qua **`bmbox console`**, rồi **một lệnh `bmbox destroy`** trả host về nguyên trạng.

Môi trường: `htc@lab22` (Ubuntu 22.04, libvirt 8.0 trong container Kolla, sushy-tools 2.2 và virtualbmc 3.3 trong `~/venv`).

---

## 0. Chuẩn bị

```bash
# máy dev
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X bmbox/cmd.version=0.4.0" -o bin/bmbox-linux-amd64 .
scp bin/bmbox-linux-amd64 htc@lab22:bmbox-test/bmbox
scp examples/topology.yaml htc@lab22:bmbox-test/topology.yaml

# lab22
ssh htc@lab22
cd ~/bmbox-test
# sudo xoá PATH, nên chỉ đường cho bmbox tới sushy-tools/virtualbmc trong venv.
# Chỉ cần ở lần up đầu tiên: bmbox nhớ đường dẫn trong ~/.bmbox/tools.json.
export BMC_TOOLS="BMBOX_SUSHY_EMULATOR=$HOME/venv/bin/sushy-emulator BMBOX_VBMC=$HOME/venv/bin/vbmc"
```

Kiểm tra lab sạch: `sudo ./bmbox status` báo *lab "demo" is not up*.

## 1. Topology có BMC

```bash
grep -A3 "bmc:" topology.yaml
```

- `node1`: `bmc: {type: redfish}` → port tự gán **8300**.
- `node2`: `bmc: {type: ipmi, port: 6231}`.
- Mặc định BMC chỉ nghe trên `127.0.0.1`, user/password `admin`/`password` (đổi được qua `address`, `username`, `password`).

## 2. `bmbox up`

```bash
sudo env $BMC_TOOLS ./bmbox up
```

```
==> Networks
    pxe        bridge=bmb-demo-pxe    created  172.30.10.1/24
    data       bridge=bmb-demo-data   created
==> Libvirt 8.0.0 (qemu:///system)
    ...
==> Nodes
    node1      bmbox-demo-node1         shutoff  defined
    node2      bmbox-demo-node2         shutoff  defined
==> BMCs
    node1      redfish  started  http://127.0.0.1:8300/redfish/v1/Systems/<uuid>
    node2      ipmi     started  127.0.0.1:6231
```

Ý chính:
- **Mỗi node một BMC riêng** (như server thật): Redfish = 1 tiến trình sushy-emulator chỉ thấy đúng domain của node; IPMI = 1 tiến trình virtualbmc riêng (không dùng chung `vbmcd`).
- BMC chạy dưới dạng **systemd transient unit** (tạo qua D-Bus): tự restart khi lỗi, log ở journald, sống độc lập với tiến trình bmbox.
- `up` **kiểm tra port trước** khi tạo bất cứ thứ gì (lab22 có `docker-proxy` giữ 8100, sushy tuần 2 giữ 8008, vbmcd giữ 6230).

```bash
sudo ./bmbox status
systemctl list-units 'bmbox-*'
journalctl -u bmbox-demo-node1-bmc.service -n 5
```

## 3. Điều khiển OOB qua Redfish (node1)

```bash
R=http://127.0.0.1:8300/redfish/v1
U=$(sudo ./bmbox status | awk '/node1/{print $NF}' | xargs basename)

curl -s -o /dev/null -w '%{http_code}\n' $R/Systems/$U          # 401: cần xác thực
curl -s -u admin:password $R/Systems | python3 -m json.tool       # chỉ 1 system: node1
curl -s -u admin:password $R/Systems/$U | python3 -c \
  'import json,sys; d=json.load(sys.stdin); print(d["PowerState"], d["Boot"]["BootSourceOverrideMode"])'
# Off UEFI

# Bật nguồn
curl -s -u admin:password -H 'Content-Type: application/json' \
  -X POST -d '{"ResetType":"On"}' $R/Systems/$U/Actions/ComputerSystem.Reset
```

## 4. Xem boot qua serial console

```bash
sudo ./bmbox console node1
# Connected to demo/node1 serial console. Detach with Ctrl-].
# >>Start PXE over IPv4.
```

Nhấn **Ctrl-]** để thoát. Có thể ghi log không tương tác:
`sudo timeout 30 ./bmbox console node1 < /dev/null > boot.log`.

Tắt nguồn lại:
```bash
curl -s -u admin:password -H 'Content-Type: application/json' \
  -X POST -d '{"ResetType":"ForceOff"}' $R/Systems/$U/Actions/ComputerSystem.Reset
```

## 5. Điều khiển OOB qua IPMI (node2)

```bash
I="ipmitool -I lanplus -H 127.0.0.1 -p 6231 -U admin -P password"
$I power status          # Chassis Power is off
$I chassis bootdev pxe
$I power on
sudo ./bmbox status      # node2 running
$I power off
ipmitool -I lanplus -H 127.0.0.1 -p 6231 -U admin -P wrong power status   # bị từ chối
```

## 6. Idempotent và đổi topology

```bash
sudo ./bmbox up | sed -n '/BMCs/,$p'
# node1 redfish running ...   <- BMC đang chạy, cấu hình không đổi: không restart
```

Xoá `node2` và network `data` khỏi topology rồi chạy lại `up`: bmbox **phát hiện** tài nguyên thừa (qua dấu sở hữu: metadata domain, alias bridge, mô tả unit) và chỉ báo, không tự xoá:

```
==> Not in topology any more (kept; run 'bmbox up --prune' to remove)
    node    node2 (bmbox-demo-node2)
    bmc     node2 (bmbox-demo-node2-bmc.service)
    volume  node2-VARS.fd
    volume  node2-disk0.qcow2
    network data (bmb-demo-data)
```

`sudo ./bmbox up --prune` xoá chúng. `sudo ./bmbox list` liệt kê các lab trong workspace.

Hai lệnh cùng lúc trên một lab bị chặn bằng khoá:
`Error: another bmbox command is already running for lab "demo"`.

## 7. `bmbox destroy`

```bash
sudo ./bmbox destroy
```

```
==> BMCs
    node1      stopped bmbox-demo-node1-bmc.service
    node2      stopped bmbox-demo-node2-bmc.service
==> Nodes and storage
    node1      undefined bmbox-demo-node1
    node2      undefined bmbox-demo-node2
    pool       deleted bmbox-demo (/var/lib/libvirt/bmbox/demo)
==> Networks
    pxe        deleted bmb-demo-pxe
    data       deleted bmb-demo-data

Lab "demo" destroyed.
```

Kiểm chứng host sạch, tài nguyên khác không bị đụng:
```bash
sudo virsh list --all | grep bmbox; sudo virsh pool-list --all | grep bmbox
ip -br link | grep bmb-; systemctl list-units 'bmbox-*' --all; ls ~/.bmbox/labs
sudo virsh domstate virtual-baremetal-01                # VM tuần 2 vẫn còn
pgrep -af 'sushy-emulator|vbmcd'                         # BMC tuần 2 vẫn chạy
```

`destroy` có thể chạy lại nhiều lần, chạy được từ bất kỳ thư mục nào với `--lab demo`, và vẫn dọn sạch khi `up` lỗi giữa chừng hoặc topology đã bị sửa: nó gộp `state.json`, topology và **những gì tìm thấy trên host** qua dấu sở hữu.

---

## Ghi chú kiến trúc

- **Thứ tự huỷ ngược với tạo:** BMC → domain (kèm NVRAM, snapshot metadata) → volume → pool (xoá thư mục) → bridge → sysctl → `/run/bmbox/<lab>` → workspace.
- **An toàn trên host dùng chung:** domain chỉ bị xoá nếu có metadata `bmbox:node` đúng lab/node; bridge chỉ bị xoá nếu alias `bmbox:<lab>/<net>`; pool chỉ bị xoá nếu đúng đường dẫn và không còn file lạ; sysctl chỉ được khôi phục nếu giá trị vẫn là giá trị bmbox đặt và không lab nào khác cần.
- **Bí mật:** file htpasswd (bcrypt) và cấu hình IPMI được ghi quyền `0600` trong `~/.bmbox/labs/<lab>/bmc/<node>/`.
- **Hạn chế đã biết:** sushy-tools 2.2 trả HTTP 500 (thay vì 404) khi hỏi domain ngoài phạm vi — journal ghi rõ `access denied`. Redfish hiện dùng HTTP thường, chưa TLS.

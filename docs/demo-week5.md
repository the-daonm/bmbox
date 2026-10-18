# Hướng dẫn demo bmbox — Tuần 5

Mục tiêu: lab do bmbox dựng **chạy trọn một vòng provisioning thật**. Bật node qua BMC → node **nhận IP từ DHCP của lab** → **PXE boot qua iPXE** vào kernel khai báo trong topology → xem quá trình boot qua `bmbox console`. Các node **trao đổi được với nhau** kể cả khi host lọc gói bridge bằng iptables (`bridge-nf-call-iptables=1`) và chain `FORWARD` để `DROP`.

Môi trường: `htc@lab22` (Ubuntu 22.04, libvirt 8.0 trong container Kolla, có sẵn Ironic dùng dnsmasq riêng, Docker và Neutron đặt `bridge-nf-call-iptables=1`).

| Mục tiêu tuần 5 | Trạng thái |
| --- | --- |
| 1. DHCP/TFTP/iPXE theo từng lab, khai báo trong topology | xong |
| 3. Node trao đổi được khi bật `bridge-nf-call-iptables` | xong |
| 2. Tích hợp Ironic (`bmbox export ironic`) | chưa, cần chủ lab cho phép |
| 4. Bảo mật BMC (HTTPS, mật khẩu ngẫu nhiên) | chưa |
| 5. Đóng gói (CI, báo cáo, video) | chưa |

---

## 0. Chuẩn bị

```bash
# máy dev
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X bmbox/cmd.version=0.5.0" -o bin/bmbox-linux-amd64 .
scp bin/bmbox-linux-amd64 htc@lab22:bmbox-test/bmbox
scp examples/topology.yaml htc@lab22:bmbox-test/topology.yaml
```

Trên lab22 cần **dnsmasq** và **iPXE** (bản UEFI `snponly.efi`). Để không đổi gói hệ thống của host dùng chung, chỉ tải gói `.deb` về rồi giải nén vào thư mục người dùng:

```bash
ssh htc@lab22
mkdir -p ~/bmbox-test/pkgs && cd ~/bmbox-test/pkgs
apt-get download dnsmasq-base ipxe
for f in *.deb; do dpkg -x $f root; done
# root/usr/sbin/dnsmasq, root/usr/lib/ipxe/snponly.efi
```

Chỉ cần chỉ đường ở lần `up` đầu tiên, sau đó bmbox nhớ trong `~/.bmbox/tools.json` (giống sushy-tools/virtualbmc tuần 4):

```bash
cd ~/bmbox-test
sudo ./bmbox up --dnsmasq ~/bmbox-test/pkgs/root/usr/sbin/dnsmasq \
                --ipxe ~/bmbox-test/pkgs/root/usr/lib/ipxe/snponly.efi
```

Ảnh để netboot: **CirrOS** (kernel + initramfs khoảng 20 MB). CirrOS chạy hoàn toàn trong RAM, có `ping` và DHCP client, nên node "bare-metal" boot vào một Linux dùng được mà không cần đĩa:

```bash
mkdir -p ~/bmbox-test/cirros && cd ~/bmbox-test/cirros
for f in kernel initramfs; do curl -fLO https://download.cirros-cloud.net/0.6.2/cirros-0.6.2-x86_64-$f; done
```

## 1. Topology: DHCP, IP cố định và netboot

```yaml
  networks:
    - name: pxe
      cidr: 172.30.10.0/24   # host giữ 172.30.10.1 trên bridge
      dhcp: {}               # dnsmasq của bmbox cấp 172.30.10.2-254
    - name: data

  netboot:
    kernel: /home/htc/bmbox-test/cirros/cirros-0.6.2-x86_64-kernel
    initrd: /home/htc/bmbox-test/cirros/cirros-0.6.2-x86_64-initramfs
    cmdline: console=ttyS0 dslist=nocloud

  nodes:
    - name: node1
      nics:
        - network: pxe
          ip: 172.30.10.11     # địa chỉ DHCP cố định
        - network: data
```

- `dhcp: {}`: dải mặc định là toàn bộ địa chỉ host của subnet nằm cùng phía với gateway. Có thể ghi `dhcp: {range: 172.30.10.100-172.30.10.199}`.
- `ip:` trên NIC: dnsmasq luôn cấp đúng địa chỉ này cho MAC của NIC (DHCP reservation). Hostname là tên node.
- `netboot`: `kernel`/`initrd` là đường dẫn tuyệt đối trên host (bmbox chép vào thư mục TFTP) hoặc URL `http(s)://` (iPXE tải thẳng). Thay vì kernel, có thể dùng `script:` để chạy script iPXE riêng.
- `console=ttyS0` để kernel in ra serial, xem được bằng `bmbox console`. `dslist=nocloud` để CirrOS không chờ dịch vụ metadata của cloud (nếu không, nó chờ khoảng 15 phút trước khi hiện login).

`bmbox validate` kiểm tra các lỗi khai báo, ví dụ: DHCP trên network không có `cidr`, dải DHCP chứa gateway hoặc nằm ngoài subnet, IP trùng nhau, IP nằm ngoài subnet, `netboot` khi không có network nào bật DHCP, hoặc đường dẫn kernel không tuyệt đối.

## 2. `bmbox up`

```bash
sudo ./bmbox up
```

```
==> Networks
    pxe        bridge=bmb-demo-pxe    created  172.30.10.1/24
    data       bridge=bmb-demo-data   created
    firewall  added: traffic within lab bridges accepted in FORWARD (iptables, ip6tables)
==> Libvirt 8.0.0 (qemu:///system)
    ...
==> Nodes
    node1      bmbox-demo-node1         shutoff  defined
    node2      bmbox-demo-node2         shutoff  defined
==> DHCP
    pxe        bmb-demo-pxe    172.30.10.2-172.30.10.254
    netboot    /home/htc/bmbox-test/cirros/cirros-0.6.2-x86_64-kernel
    dnsmasq    started bmbox-demo-dnsmasq.service
==> BMCs
    node1      redfish  started  http://127.0.0.1:8300/redfish/v1/Systems/<uuid>
    node2      ipmi     started  127.0.0.1:6231
```

Ý chính:
- **Mỗi lab một dnsmasq**, chạy dưới dạng systemd unit `bmbox-<lab>-dnsmasq.service` (cơ chế giống BMC tuần 4). dnsmasq chạy dưới user `nobody`, không bật DNS (`port=0`), và chỉ nghe trên bridge của lab (`bind-interfaces`). Nhờ vậy nó **chạy song song được với dnsmasq của Ironic**, vốn cũng giữ cổng 67 trên lab22.
- dnsmasq được bật **sau khi định nghĩa node và trước khi bật BMC**, nên không thể bật nguồn một node khi chưa có DHCP.
- Cấu hình, lease và thư mục TFTP nằm ở `/var/lib/bmbox/<lab>/`. `up` chỉ coi là xong khi dnsmasq đã trả lời TFTP, vì dnsmasq chỉ phục vụ TFTP sau khi đã mở xong mọi socket, kể cả DHCP.
- `up` kiểm tra trước mọi thứ cần thiết (dnsmasq, iPXE, file kernel/initrd) trước khi tạo bất cứ tài nguyên nào.

```bash
sudo cat /var/lib/bmbox/demo/dnsmasq.conf
```

```
interface=bmb-demo-pxe
dhcp-range=set:pxe,172.30.10.2,172.30.10.254,255.255.255.0,1h
dhcp-host=52:54:00:62:be:90,172.30.10.11,node1
dhcp-host=52:54:00:aa:00:02,node2
enable-tftp
tftp-root=/var/lib/bmbox/demo/tftp
dhcp-userclass=set:ipxe,iPXE
dhcp-boot=tag:ipxe,boot.ipxe                # iPXE  -> script
dhcp-boot=tag:!ipxe,tag:efi64,ipxe.efi      # UEFI  -> iPXE
```

## 3. Bật nguồn qua BMC → PXE → iPXE → kernel

```bash
R=http://127.0.0.1:8300/redfish/v1
U=$(sudo ./bmbox status | awk '/node1/{print $NF}' | xargs basename)
curl -s -u admin:password -H 'Content-Type: application/json' \
  -X POST -d '{"ResetType":"On"}' $R/Systems/$U/Actions/ComputerSystem.Reset
ipmitool -I lanplus -H 127.0.0.1 -p 6231 -U admin -P password power on    # node2

sudo ./bmbox console node1 -n 100
```

Trích từ console của node1:

```
>>Start PXE over IPv4.
  Station IP address is 172.30.10.11
  NBP filename is ipxe.efi
  NBP file downloaded successfully.
iPXE 1.21.1+git-20220113.fbbdc3926-0ubuntu1 -- Open Source Network Boot Firmware
Configuring (net0 52:54:00:62:be:90)...... ok
tftp://172.30.10.1/boot.ipxe... ok
bmbox: booting vmlinuz
vmlinuz... ok
initrd... ok
EFI stub: Loaded initrd from command line option
[    0.000000] Linux version 5.15.0-71-generic ...
[    0.000000] Command line: vmlinuz initrd=initrd console=ttyS0 dslist=nocloud
...
cirros login:
```

Như vậy chuỗi boot là: firmware UEFI xin DHCP và nhận `ipxe.efi` qua TFTP → iPXE xin DHCP lần nữa và nhận `boot.ipxe` → script tải kernel và initrd → Linux khởi động. Toàn bộ mất chưa tới 10 giây. Script do bmbox sinh ra:

```
#!ipxe
# Generated by bmbox for lab demo.
echo bmbox: booting vmlinuz
kernel vmlinuz initrd=initrd console=ttyS0 dslist=nocloud
initrd initrd
boot
```

`status` hiện thêm cột **IP**, đọc từ file lease của dnsmasq:

```
DHCP: active (bmbox-demo-dnsmasq.service)

NODE   POWER    IP             BMC      BMC STATE  ENDPOINT
node1  running  172.30.10.11   redfish  active     http://127.0.0.1:8300/redfish/v1/Systems/<uuid>
node2  running  172.30.10.254  ipmi     active     127.0.0.1:6231
```

Log DHCP/TFTP: `sudo journalctl -u bmbox-demo-dnsmasq.service`:

```
DHCPACK(bmb-demo-pxe) 172.30.10.11 52:54:00:62:be:90 node1
sent /var/lib/bmbox/demo/tftp/ipxe.efi to 172.30.10.11
sent /var/lib/bmbox/demo/tftp/boot.ipxe to 172.30.10.11
sent /var/lib/bmbox/demo/tftp/vmlinuz to 172.30.10.11
sent /var/lib/bmbox/demo/tftp/initrd to 172.30.10.11
```

## 4. Node nói chuyện với nhau

Đăng nhập node1 qua `bmbox console node1` (user `cirros`, mật khẩu `gocubsgo`) rồi ping node2:

```
$ ip -4 -o addr show eth0
2: eth0    inet 172.30.10.11/24 ...
$ ping -c3 172.30.10.254
3 packets transmitted, 3 received, 0% packet loss
```

## 5. `bridge-nf-call-iptables` và chain FORWARD

**Vấn đề:** trên lab22, Docker và Neutron nạp `br_netfilter` và đặt `net.bridge.bridge-nf-call-iptables=1` cho toàn host. Khi đó, gói đi **giữa hai node trên cùng một bridge** cũng phải qua chain `FORWARD` của iptables. Host nào để policy `FORWARD` là `DROP` (Docker mặc định làm vậy) thì các node không thấy nhau. Gói từ node tới host (DHCP, TFTP) đi qua `INPUT` nên không bị ảnh hưởng.

**Vì sao không tắt sysctl:** sysctl này là thiết lập chung của cả host, tắt đi sẽ làm hỏng Docker và Neutron. Bridge có tuỳ chọn riêng `nf_call_iptables`, nhưng kernel lấy **OR** giữa tuỳ chọn đó và thiết lập của host, nên không dùng nó để tắt lọc được.

**Cách bmbox làm (giống libvirt):** chấp nhận gói đi vào và đi ra trên **cùng một bridge của lab**, trong chain riêng `BMBOX-FORWARD`. `FORWARD` nhảy tới chain này đầu tiên:

```bash
sudo iptables -S FORWARD | head -2; sudo iptables -S BMBOX-FORWARD
```

```
-P FORWARD ACCEPT
-A FORWARD -j BMBOX-FORWARD
-N BMBOX-FORWARD
-A BMBOX-FORWARD -i bmb-demo-pxe -o bmb-demo-pxe -m comment --comment "bmbox:demo/pxe" -j ACCEPT
-A BMBOX-FORWARD -i bmb-demo-data -o bmb-demo-data -m comment --comment "bmbox:demo/data" -j ACCEPT
```

- Rule được thêm cho cả `iptables` và `ip6tables`, bằng chính công cụ của host (lab22 dùng iptables-nft), để nằm chung chain với `DROP` của host.
- Mỗi rule mang comment là chủ sở hữu (`bmbox:<lab>/<network>`). `destroy` và `--prune` tìm rule theo comment này để xoá. Khi rule cuối cùng bị xoá, chain và lệnh nhảy cũng bị xoá theo.
- Bridge `external` (của người khác) không được thêm rule.
- bmbox chỉ mở traffic **trong** một bridge, không định tuyến hay NAT giữa lab và bên ngoài.

**Kiểm chứng trên VM thật** (lab22 có policy `ACCEPT`, nên mô phỏng `DROP` bằng một rule chỉ áp cho bridge của lab, đặt ở cuối `FORWARD`; rule này được gỡ ngay sau đó):

```bash
sudo iptables -A FORWARD -i bmb-demo-pxe -j DROP          # như policy DROP, chỉ cho lab này
# node1: ping -c3 172.30.10.254  -> 3 received            (nhờ BMBOX-FORWARD)
sudo iptables -D BMBOX-FORWARD -i bmb-demo-pxe -o bmb-demo-pxe \
  -m comment --comment bmbox:demo/pxe -j ACCEPT           # đối chứng: xoá rule bmbox
# node1: ping -c3 172.30.10.254  -> 0 received, 100% packet loss
sudo ./bmbox up | grep firewall                           # up thêm lại rule
#     firewall  added: traffic within lab bridges accepted in FORWARD (iptables, ip6tables)
# node1: ping -c3 172.30.10.254  -> 3 received
sudo iptables -D FORWARD -i bmb-demo-pxe -j DROP          # gỡ rule thử nghiệm
```

> Nếu firewall của host bị nạp lại và xoá sạch `FORWARD`, lệnh nhảy tới `BMBOX-FORWARD` sẽ mất. Chạy lại `sudo ./bmbox up` để thêm lại.

## 6. Đổi topology: bỏ DHCP

Xoá `dhcp: {}` và `netboot` khỏi topology rồi chạy `up`: dnsmasq bị báo là thừa nhưng không bị xoá tự động. `--prune` mới dừng unit và xoá `/var/lib/bmbox/<lab>`. Network bị xoá khỏi topology thì rule firewall của nó cũng được báo và prune tương tự:

```
==> Not in topology any more (kept; run 'bmbox up --prune' to remove)
    firewall rule for data (bmb-demo-data)
    dhcp    bmbox-demo-dnsmasq.service
```

Chạy `up` lần hai khi không có gì thay đổi thì dnsmasq được giữ nguyên (`dnsmasq running`), không bị restart.

## 7. `bmbox destroy`

```
==> BMCs
    node1      stopped bmbox-demo-node1-bmc.service
    node2      stopped bmbox-demo-node2-bmc.service
==> DHCP
    dnsmasq    stopped bmbox-demo-dnsmasq.service
==> Nodes and storage
    node1      undefined bmbox-demo-node1
    node2      undefined bmbox-demo-node2
    pool       deleted bmbox-demo (/var/lib/libvirt/bmbox/demo)
==> Networks
    pxe        deleted bmb-demo-pxe
    data       deleted bmb-demo-data
    data       firewall rule removed
    pxe        firewall rule removed

Lab "demo" destroyed.
```

Sau đó `FORWARD` trở về đúng như trước (không còn `BMBOX-FORWARD`), không còn unit `bmbox-*`, và `/var/lib/bmbox/demo` đã bị xoá.

## 8. Test đầu-cuối

```bash
go build -o bin/bmbox . && BMBOX=bin/bmbox scripts/e2e.sh
```

Trên lab22: **63/63 bước đạt, khoảng 52 giây** (tuần 4 là 46 bước). Các nhóm mới:

| Nhóm | Kiểm tra |
| --- | --- |
| up | dnsmasq active, `status` báo DHCP, thư mục TFTP có đủ `ipxe.efi`, `boot.ipxe`, `vmlinuz`, `initrd` |
| Netboot (n1) | n1 nhận đúng IP cố định; firmware tải iPXE; iPXE tải script, kernel, initrd; kernel in ra serial |
| Convergence | `up` lần hai không restart dnsmasq; bỏ DHCP thì dnsmasq bị báo thừa, `--prune` dừng nó và xoá dữ liệu; rule firewall của network bị bỏ cũng bị xoá |
| Forwarding | có jump và rule IPv4/IPv6; hai endpoint (network namespace) trên bridge ping được nhau dù cuối `FORWARD` có `DROP`; đối chứng: xoá rule thì bị chặn; `up` thêm lại rule |
| destroy | ngoài những gì có từ tuần 4, ảnh chụp host trước/sau giờ so cả `FORWARD`/`BMBOX-FORWARD` (v4 và v6) và `/var/lib/bmbox` |

Mặc định e2e netboot chính kernel `/boot` của host (`E2E_KERNEL`/`E2E_INITRD` để đổi). Node boot tới shell `(initramfs)` vì không có ổ root, như vậy đủ để chứng minh chuỗi netboot.

---

## Ghi chú kiến trúc

- **Thứ tự `up`:** bridge → rule firewall → sysctl → pool/đĩa/NVRAM → domain (SHUTOFF) → **dnsmasq** → BMC → phát hiện tài nguyên thừa.
- **Thứ tự `destroy`:** BMC → **dnsmasq** → domain → volume → pool → bridge → **rule firewall** → sysctl → `/run/bmbox/<lab>` → workspace.
- **Package mới:** `pkg/systemd` (transient unit qua D-Bus, dùng chung cho BMC và dnsmasq), `pkg/netboot` (cấu hình dnsmasq, script iPXE, đồng bộ thư mục TFTP, kiểm tra TFTP, đọc lease), `pkg/network/firewall.go`.
- **Đồng bộ TFTP:** chỉ chép kernel/initrd khi kích thước hoặc thời gian sửa khác với file gốc. File thừa (ví dụ khi đổi từ kernel sang script) bị xoá.

## Lỗi đã sửa trong tuần

- **`--lab` không được kiểm tra:** `sudo bmbox destroy --lab ..` xoá toàn bộ `~/.bmbox`, còn `--lab ../..` có thể xoá cả thư mục home. Giờ `--lab` phải theo quy tắc của `metadata.name`.
- **Rule firewall không xoá được:** `iptables -S` in comment trong dấu ngoặc kép, và dấu ngoặc bị đưa nguyên vào `-D` nên không khớp rule nào. Lỗi được phát hiện khi chạy thật trên lab22; unit test đã được sửa để giả lập đúng cách in này.

## Hạn chế còn lại

- DHCP chỉ hỗ trợ IPv4. Kernel/initrd tải qua TFTP (nhanh với ảnh nhỏ; ảnh lớn nên dùng URL `http://`).
- Không bật `dhcp` trên network đã có DHCP server khác (ví dụ mạng provisioning của Ironic), vì sẽ có hai server cùng trả lời.
- Lab không có NAT ra ngoài. Gateway trên bridge chỉ phục vụ DHCP/TFTP và truy cập từ host.
- BMC vẫn dùng HTTP và mật khẩu mặc định (mục tiêu 4).

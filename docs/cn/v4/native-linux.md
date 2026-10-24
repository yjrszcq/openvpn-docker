# Linux 裸机安装

Linux 压缩包包含 `ovpn`、OpenVPN、OpenSSL、Easy-RSA、musl 加载器、共享库、providers、网络工具、Shell 和编辑器，运行时无需在宿主安装这些组件。现有 Docker 部署方式、配置和命令继续保留。

x86-64 使用 `linux-amd64`，AArch64 使用 `linux-arm64`。宿主需要 Linux TUN 支持、网络管理权限，以及可供客户端访问的 OpenVPN 端口。请自行配置宿主及云防火墙；`ovpn` 只维护实例规则，不关闭 SELinux 或 firewalld。IPv4 隧道支持通过 IPv4/IPv6 使用 UDP/TCP 传输，不支持 IPv6 隧道地址。

兼容范围包括 Debian 13.7、Ubuntu 26.10、Rocky Linux 10.2、Alpine 3.24.3 和当前 Arch Linux。AArch64 上的 Arch 兼容性单独指 Arch Linux ARM。本地 AArch64 验证使用仿真；Release 工作流还要求原生 AArch64 验收通过后才上传附件。

## 在线安装

请等待 Release 页面上的两个架构压缩包和 `SHA256SUMS` 齐全后再安装。引导脚本需要 Bash、curl、tar、`sha256sum` 及常见 Linux 命令行工具，不会安装宿主软件包。

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/install.sh')"
```

安装器自动选择最新稳定 Release 和宿主架构，验证压缩包后安装到 `/opt/ovpn`，创建 `/usr/local/bin/ovpn`，并注册 systemd 或 OpenRC 服务。首次安装保持服务停止，也不启用开机启动。遇到无关的已有命令、目录或服务定义时拒绝覆盖。

需要指定目录和已发布版本时，替换 `vX.Y.Z`：

```bash
sudo env OVPN_INSTALL_DIR=/opt/vpn OVPN_VERSION=vX.Y.Z bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/install.sh')"
```

后续更新请使用相同的 `OVPN_INSTALL_DIR`。每台宿主支持一个由安装器管理的 `ovpn` 服务。

## 配置与启动

```bash
sudo cp /opt/ovpn/config/config.example.yaml /opt/ovpn/config/config.yaml
sudo /opt/ovpn/bin/nano /opt/ovpn/config/config.yaml
```

将 `server.endpoint` 改为公网域名或 IP，并选择不与现有网络重叠的 `ipv4.network`。启动前检查传输方式、NAT、路由和 DNS 设置。也可使用包内编辑器 `/opt/ovpn/bin/nano`。

systemd：

```bash
sudo systemctl enable --now ovpn
sudo systemctl status ovpn
```

OpenRC：

```bash
sudo rc-update add ovpn default
sudo rc-service ovpn start
```

前台运行：

```bash
sudo /opt/ovpn/ovpn
```

无参数执行会启动服务；数据为空时使用已有配置初始化。缺少配置时提示操作方法并停止。请勿与托管服务同时启动前台实例。SIGTERM 或 Ctrl-C 会停止子进程并清理实例网络规则。

## 文件与日常命令

| 相对解压目录的路径 | 用途 |
| --- | --- |
| `ovpn` | 可重定位的 CLI 与前台启动入口 |
| `runtime/`、`bin/` | 包内程序、库、资源和许可证 |
| `config/config.yaml` | 用户配置 |
| `data/` | SQLite、PKI、客户端配置、派生配置和审计状态 |
| `logs/` | 指向 `data/logs/`，包含轮转后的 OpenVPN 日志 |
| `run/` | 运行时套接字和服务诊断输出 |

```bash
sudo ovpn runtime health
sudo ovpn client create laptop --ipv4 auto
sudo ovpn client export laptop --output /root/laptop.ovpn
sudo ovpn state doctor
sudo ovpn config plan
sudo ovpn config apply --yes
```

CLI、可选 REST API 和已有 `OVPN_*` 覆盖继续有效。托管 systemd 服务可通过 `systemctl edit ovpn`，在 `[Service]` 中使用相应的 `Environment=...` 设置。启动诊断可通过 `journalctl -u ovpn` 查看；OpenRC 将其写入 `run/service*.log`。API 的启用和保护方式见 [REST API 指南](rest-api.md)。

## 更新与恢复

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/update.sh')"
```

更新器先下载验证，再锁定安装目录。服务原先运行时，会停止、保存快照、更新并重启检查健康；原先停止则继续停止。用户配置、证书、数据库、客户端配置、日志、权限和开机启动状态会保留。替换或健康检查失败时恢复旧程序和快照。更新中断后，再次执行更新器即可恢复事务；若 `.update-backup/` 仍保留，表示恢复需要处理，请在实例恢复健康前保留它。

更新仅允许兼容的数据 schema，并拒绝降级。重复安装、同版本更新不会破坏状态。手工启动的实例需要先自行停止，更新器不会自动终止它们。独立备份方法见[操作手册](operations.md)。

## 手工解压与移动目录

从同一 Release 下载匹配架构的压缩包和 `SHA256SUMS`，按需替换 `X.Y.Z` 和架构：

```bash
awk '$2 == "openvpn-server-X.Y.Z-linux-amd64.tar.gz" {print}' SHA256SUMS | sha256sum -c -
tar -xzf openvpn-server-X.Y.Z-linux-amd64.tar.gz
cd openvpn-server
cp config/config.example.yaml config/config.yaml
# 启动前编辑 config/config.yaml。
sudo ./ovpn
```

手工前台运行不需要服务管理器。需要通过 PATH 使用 CLI 时，添加解压根目录，例如 `export PATH="/opt/ovpn:$PATH"`，并保持整个目录完整。支持绝对路径、符号链接、不同工作目录和含空格的路径。

初始化前可直接移动目录。已初始化的实例需要先停止，再移动完整目录，然后执行 `sudo /new/path/ovpn repair apply --yes`，重建含绝对路径的派生文件后再启动。仍指向旧位置的服务定义和命令链接也需要更新。安装器管理的更新器使用原先注册的目录和服务；手工移动后需要先同步调整这些路径。

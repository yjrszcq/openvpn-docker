# OpenVPN 服务端 Docker 镜像

[English](README.md)

这是一个用于运行 OpenVPN Community Edition 服务端的 Docker 镜像，提供
Shell 控制面。它适用于家庭实验室、小型团队和需要基于证书的 IPv4 TUN VPN
接入的 Linux 服务器，不提供 Web 管理界面。

## 概述

- 从校验和固定的源码构建 OpenVPN runtime，支持 `linux/amd64` 和
  `linux/arm64`。
- 首次启动空持久化目录时，自动创建 PKI、服务端身份、CRL、tls-crypt 密钥和
  渲染后的服务端配置。
- 支持经公网 IPv4 或 IPv6 承载的 UDP/TCP、IPv4 NAT、路由推送、全隧道、
  DNS 推送和客户端互访。
- 使用持久化 client-IP 清单管理独立的静态地址区和动态地址池。
- 启动前检测持久化状态不一致；关键状态默认拒绝启动。

本镜像支持 IPv4 TUN、IPv4/IPv6 公网传输、双向证书认证、Easy-RSA、tls-crypt
和 CRL。暂不支持 Web UI、TAP、IPv6 隧道地址或双栈数据平面、外部或离线 CA
工作流、LDAP/RADIUS/OIDC，以及 Kubernetes 集成。

## 快速开始

### 前提条件

- 已安装 Docker Engine 和 Docker Compose plugin。
- Linux 主机提供 `/dev/net/tun`，并允许容器使用 `NET_ADMIN`。
- 有可被客户端访问的公网域名或 IP，并在主机与云防火墙中开放 `1194/udp`。
  若修改默认值，则开放所选端口和协议。
- 选择一个不与服务器或客户端网络重叠的私有 IPv4 CIDR。

### 配置并启动

创建最小的 `compose.yaml`：

```yaml
services:
  openvpn:
    image: szcq/openvpn:2.7.5
    container_name: openvpn
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./openvpn-data:/etc/openvpn
    environment:
      OVPN_ENDPOINT: vpn.example.com
      OVPN_PROTO: udp
      OVPN_TRANSPORT_FAMILY: auto
      OVPN_PORT: "1194"
      OVPN_NETWORK: 10.42.0.0/24
      OVPN_TOPOLOGY: subnet
      OVPN_DYNAMIC_POOL_SIZE: "64"
      OVPN_NAT: "false"
      OVPN_NAT_INTERFACE: auto
      OVPN_REDIRECT_GATEWAY: "false"
      OVPN_CLIENT_TO_CLIENT: "true"
      OVPN_DNS: ""
      OVPN_ROUTES: ""
      OVPN_CRITICAL_MODE: exit
```

将 `vpn.example.com` 替换为客户端实际连接的公网域名或 IP，并按部署环境选择
未使用的网段。仓库中的 `docker-compose.yaml` 还包含一个可选的低权限
maintenance 服务。

示例使用 host 网络模式，OpenVPN 直接监听宿主机，因此没有 Docker 的
`ports:` 映射；`NET_ADMIN` 也会影响宿主机网络命名空间。启用 NAT、路由推送或
全隧道路由时，服务可能修改该命名空间中的 IPv4 转发和 iptables 规则。仅应在
受控的 Linux 主机上使用这种布局，并审阅最终的防火墙规则。

启动服务：

```bash
docker compose up -d
docker compose logs -f openvpn
```

首次启动只会初始化空的 `./openvpn-data` 目录，并将 bootstrap 配置写入
`config/project.env`。之后修改 bootstrap 环境变量不会重写已有实例。

## 配置项

| 变量 | 运行时默认值 / Compose 回退值 | 快速开始值 | 说明 |
| --- | --- | --- | --- |
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | `szcq/openvpn:2.7.5` | Compose 使用的镜像。生产环境请固定到已发布的 OpenVPN 版本 tag。 |
| `OVPN_ENDPOINT` | 必填 | `vpn.example.com` | 首次初始化时写入客户端 profile 的公网域名或 IP。 |
| `OVPN_PROTO` | `udp` | `udp` | 传输协议：`udp` 或 `tcp`。 |
| `OVPN_TRANSPORT_FAMILY` | `auto` | `auto` | 公网传输地址族：`auto` 会识别 IP 字面量，并为域名使用双栈传输；也可显式设为 `ipv4` 或 `ipv6`。 |
| `OVPN_PORT` | `1194` | `1194` | OpenVPN 监听端口。 |
| `OVPN_NETWORK` | `10.8.0.0/24` | `10.42.0.0/24` | IPv4 隧道网段，必须选择无重叠且规范的 CIDR。 |
| `OVPN_TOPOLOGY` | `subnet` | `subnet` | 必须使用的 IPv4 拓扑；不接受其他拓扑。 |
| `OVPN_DYNAMIC_POOL_SIZE` | 可用客户端地址数的一半 | `64` | 从可用地址尾部划出的动态池；`0` 和全部可用地址都是合法边界。 |
| `OVPN_NAT` | `false` | `false` | 对离开 VPN 网络命名空间的客户端流量执行 NAT。 |
| `OVPN_NAT_INTERFACE` | `auto` | `auto` | NAT 出口接口，也可指定 Linux 接口名。 |
| `OVPN_REDIRECT_GATEWAY` | `false` | `false` | 将客户端默认流量经 VPN 转发。 |
| `OVPN_CLIENT_TO_CLIENT` | `true` | `true` | 允许 VPN 客户端直接互访。 |
| `OVPN_DNS` | 空 | 空 | 推送给客户端的逗号分隔 IPv4 DNS 服务器。 |
| `OVPN_ROUTES` | 空 | 空 | 推送给客户端的逗号分隔 IPv4 CIDR。 |
| `OVPN_CRITICAL_MODE` | `exit` | `exit` | 仅在需要保留关键状态容器进行排障时使用 `maintenance`。 |
| `OVPN_EDITOR` | `EDITOR`，否则 `nano` | 未设置 | 交互式 client-IP 操作使用的编辑器。 |

运行时默认值只在环境未提供相应值时生效。快速开始值是
`docker-compose.yaml` 与 `.env.example` 中有意选择的值，并不是另一套
运行时默认值。

使用 `OVPN_TRANSPORT_FAMILY=auto` 时，IPv4 字面量（如 `198.51.100.10`）会选择
IPv4 传输，IPv6 字面量（如 `2001:db8::10`）会选择 IPv6 传输。使用域名时，
服务端开启双栈传输 socket，客户端在连接时解析并尝试 A/AAAA 记录；`config apply`
不会解析 DNS。因此仅有公网 IPv6 的服务器可配置 AAAA 记录并继续使用 `auto`；
只有需要拒绝 IPv4 传输时才显式设置 `ipv6`。这只改变 OpenVPN 外层连接；隧道地址、
推送路由和 DNS 配置仍为 IPv4。若服务器本身没有 IPv4 出口，VPN 客户端也不能
通过现有 IPv4 NAT 访问公网 IPv4；本镜像不提供 NAT64。

对于前缀长度为 `p` 的规范 CIDR，可用客户端地址数为 `2^(32-p)-3`：网络地址、
服务端地址（`网络地址 + 1`）和广播地址均被保留。例如，`10.42.0.0/24` 可提供
253 个客户端地址（`10.42.0.2` 至 `10.42.0.254`），动态池可设为 `0` 至 `253`，
未设置时默认取 `floor(253 / 2) = 126`。动态池始终占用可用地址区间末尾的一段
连续地址，前面的剩余部分就是静态区。最小可用网段为 `/30`，恰好提供一个
客户端地址（`.2`）。

应明确选择路由方式：

- 私网路由：保持 `OVPN_NAT=false` 与 `OVPN_REDIRECT_GATEWAY=false`，并用
  `OVPN_ROUTES` 推送可访问的私网路由。每个目标网络都必须经 VPN 主机拥有到
  VPN CIDR 的返回路由。
- Internet 全隧道：设置 `OVPN_NAT=true` 与 `OVPN_REDIRECT_GATEWAY=true`。
  除非主机存在多个候选出口接口，否则保持 `OVPN_NAT_INTERFACE=auto`。

## 命令文档

本 README 不再重复命令手册。请按所使用的镜像版本选择对应参考：

- [v1 命令参考](docs/cn/v1/commands.md)：发布提交 `6619921e5257e604f5df2c63d2fa10505b680d84`。
  - [v1 操作手册](docs/cn/v1/operations.md)：按工作流组织的命令组合用法。
- [v2 命令参考](docs/cn/v2/commands.md)：当前 CLI。
  - [v2 操作手册](docs/cn/v2/operations.md)：按工作流组织的命令组合用法。

## 安全说明

- 默认设计将 CA 保持在持久化数据卷内，便于日常运维；数据卷被攻破可能导致 CA 泄露。
- 私钥和导出的 `.ovpn` profile 都是敏感凭据，应以严格权限保存并经信任渠道交付。
- 稳定版本发布前会校验源码校验和、runtime 版本、配置加载及所需能力。
- 网络规则的作用域取决于所选网络模式。快速开始使用 host 网络模式，共享主机网络
  命名空间；启用 NAT、路由推送或全隧道路由时会修改主机的 IPv4 转发和 iptables
  规则。隔离容器网络模式下，此类修改才留在容器网络命名空间内。主机防火墙、云
  安全组与端口转发仍由运维人员负责。

## 开发

版本与发布输入集中在 `versions.env`。修改代码前运行：

```bash
tests/smoke/shell/check.sh           # Shell 语法与风格检查
tests/smoke/shell/cli-smoke.sh       # CLI 结构验证
tests/smoke/shell/workflow-smoke.sh  # 工作流逻辑验证
```

CI 会校验 OpenVPN 版本、源码校验和、支持矩阵和项目镜像版本。测试使用
`OVPN_NETWORK=10.88.0.0/24`。部分检查需要 Docker 和 `/dev/net/tun`。

## 镜像、构建与发布

Docker Hub 稳定镜像仅使用 OpenVPN runtime 版本作为 tag：

```text
szcq/openvpn:<OPENVPN_VERSION>
```

生产环境请固定明确 tag，不要依赖会变化的 tag。GitHub Container Registry 还会
接收用于项目发布管理的镜像版本 tag。

本地构建当前源码树：

```bash
scripts/docker-build.sh -t szcq/openvpn-server:dev .
OVPN_IMAGE=szcq/openvpn-server:dev docker compose up -d
```

GitHub Actions 会执行兼容性、容器、E2E、升级状态和多架构门禁。默认分支的
Candidate 会发布 GHCR 候选镜像；Candidate 成功后会自动触发 Release，创建稳定
GHCR tag 并发布 Docker Hub 的 OpenVPN 版本 tag。

每周运行（也可手动运行）的 Upstream Check 会检查官方 OpenVPN 是否有新版本。
发现新版时，它会推送 `automation/openvpn-<版本>` 分支，并创建指向 `dev` 的 PR。
审阅并合并该 PR 到 `dev` 后会运行 PR 检查，但 Candidate 不会从 `dev` 发布；将
已审阅的变更从 `dev` 提升到 `main` 才会触发 Candidate 和随后 Release。手动启动
Candidate 时也应选择 `main`。

维护者注意：若 `DOCKER_TOKEN` 过期，在 `Settings → Secrets and variables →
Actions` 更新它，然后手动启动一个默认分支的 Candidate。Candidate 成功会排队一个
新的 Release。更新仓库 secret 后，不要依赖重跑旧的 Release。

## 许可证

Copyright (C) 2026 yjrszcq。

本仓库中的原创源代码和构建配置采用 [GPL-2.0-only](LICENSE)；
[NOTICE](NOTICE) 说明其适用范围并列出第三方组件边界。容器镜像包含 OpenVPN
Community Edition 及其他第三方组件，它们继续适用各自的许可证，不会因本项目
而被重新许可。镜像在 `/usr/local/share/licenses/` 中提供本项目许可文件和
OpenVPN 的 `COPYING` 文件。

发布镜像由本源码树和 [versions.env](versions.env) 中声明并校验和固定的 OpenVPN
源码构建；获取逻辑见
[scripts/fetch-openvpn-source.sh](scripts/fetch-openvpn-source.sh)。

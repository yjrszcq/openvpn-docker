# OpenVPN 服务端 Docker 镜像

[English](README.md)

这是一个用于运行 OpenVPN Community Edition 服务端的 Docker 镜像，提供面向运维的 Shell 控制面。它适用于家庭实验室、小型团队和需要基于证书的 IPv4 TUN VPN 接入的 Linux 服务器，不提供 Web 管理界面。

## 功能

- 从校验和固定的源码构建 OpenVPN runtime，支持 `linux/amd64` 和 `linux/arm64`。
- 首次启动空持久化目录时，自动创建 PKI、服务端身份、CRL、tls-crypt 密钥和 OpenVPN 配置。
- 支持 UDP/TCP、IPv4 NAT、路由推送、全隧道、DNS 推送和客户端互访。
- 通过 `add-client`、`export-client`、`list-clients`、`revoke-client` 管理客户端证书与 profile。
- 在启动前检测持久化状态不一致；只执行安全或字节等价的修复；关键状态默认拒绝启动。
- 提供低权限的 maintenance 服务，用于诊断和修复。

## 范围

本镜像支持 IPv4 TUN、双向证书认证、Easy-RSA、tls-crypt 和 CRL。暂不支持 Web UI、TAP、IPv6、外部或离线 CA 工作流、LDAP/RADIUS/OIDC，以及 Kubernetes 集成。

## 快速开始

### 前提条件

- 已安装 Docker Engine 和 Docker Compose plugin。
- Linux 主机提供 `/dev/net/tun`，并允许容器使用 `NET_ADMIN`。
- 有可被客户端访问的公网域名或 IP，以及开放的 UDP 或 TCP 端口。
- 选择一个不与服务器或客户端网络重叠的私有 IPv4 CIDR。

### 配置并启动

创建最小的 `compose.yaml`：

```yaml
services:
  openvpn:
    image: szcq/openvpn:2.7.5
    container_name: openvpn
    restart: unless-stopped
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./openvpn-data:/etc/openvpn
    ports:
      - "1194:1194/udp"
    environment:
      OVPN_ENDPOINT: vpn.example.com
      OVPN_PROTO: udp
      OVPN_PORT: "1194"
      OVPN_NETWORK: 10.42.0.0/24
      OVPN_NAT: "true"
      OVPN_NAT_INTERFACE: auto
      OVPN_REDIRECT_GATEWAY: "false"
      OVPN_CLIENT_TO_CLIENT: "false"
      OVPN_DNS: ""
      OVPN_ROUTES: ""
      OVPN_CRITICAL_MODE: exit

```

仓库中的 `docker-compose.example.yaml` 也包含下文说明的可选
`openvpn-maintenance` 服务。


将 `vpn.example.com` 替换为客户端实际连接的公网域名或 IP。请按部署环境选择未使用的网段；示例没有假定 `10.8.0.0/24` 一定可用。

启动服务：

```bash
docker compose up -d
docker compose logs -f openvpn
```

首次启动只会初始化空的 `./openvpn-data` 目录。初始化配置会写入 `config/project.env`；之后修改 bootstrap 环境变量不会重写已有实例。

Compose 的端口映射随 `OVPN_PORT` 与 `OVPN_PROTO` 变化。修改其中任一值后，还必须在主机与云防火墙中开放同一端口和协议。

## 配置项

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | Compose 使用的镜像，生产环境请固定到已发布的 OpenVPN 版本 tag。 |
| `OVPN_ENDPOINT` | 必填 | 首次初始化时写入客户端 profile 的公网域名或 IP。 |
| `OVPN_PROTO` | `udp` | 传输协议：`udp` 或 `tcp`。 |
| `OVPN_PORT` | `1194` | OpenVPN 监听端口。 |
| `OVPN_NETWORK` | `10.8.0.0/24` | IPv4 隧道网段，必须选择无重叠 CIDR。 |
| `OVPN_NAT` | `true` | 对离开 VPN 容器网络命名空间的客户端流量执行 NAT。 |
| `OVPN_NAT_INTERFACE` | `auto` | NAT 出口接口，也可指定 Linux 接口名。 |
| `OVPN_REDIRECT_GATEWAY` | `false` | 将客户端默认流量经 VPN 转发。 |
| `OVPN_CLIENT_TO_CLIENT` | `false` | 允许 VPN 客户端直接互访。 |
| `OVPN_DNS` | 空 | 推送给客户端的逗号分隔 IPv4 DNS 服务器。 |
| `OVPN_ROUTES` | 空 | 推送给客户端的逗号分隔 IPv4 CIDR。 |
| `OVPN_CRITICAL_MODE` | `exit` | 仅在需要保留关键状态容器进行排障时使用 `maintenance`。 |

Bootstrap 配置是实例事实，而不是普通的运行时覆盖项。查看已持久化的值：

```bash
docker compose exec openvpn ovpn config print
```

## 客户端管理

创建客户端证书并导出 profile：

```bash
docker compose exec openvpn ovpn add-client laptop
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn
```

`export-client` 的标准输出只包含 profile，因此重定向时不会混入状态文本。列出或吊销客户端：

```bash
docker compose exec openvpn ovpn list-clients
docker compose exec openvpn ovpn revoke-client laptop
```

被吊销的证书会写入 CRL，其 active profile 会移出活动客户端集合。

## 运维与维护

最小快速开始配置不包含 maintenance 服务。需要一次性诊断或修复时，在 `services:`
下追加以下服务；仓库模板已包含它。它挂载同一份持久化数据，但不申请 TUN、
`NET_ADMIN` 或公开端口。

```yaml
  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
    restart: "no"
    volumes:
      - ./openvpn-data:/etc/openvpn
    profiles:
      - maintenance
    command:
      - doctor
    entrypoint:
      - /usr/local/bin/ovpn
```

```bash
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance doctor --json
docker compose run --rm openvpn-maintenance repair --plan
docker compose run --rm openvpn-maintenance repair
```

`doctor` 只读；`repair --plan` 也只读，并展示符合条件的 SAFE 与等价恢复操作。`repair` 会暂存、验证、创建快照，并原子应用允许的修复。`CRITICAL` 和 `UNRECOVERABLE` 默认以退出码 `78` 拒绝继续运行；只有在运维人员需要保留不健康容器排障时才使用 `OVPN_CRITICAL_MODE=maintenance`。

运行状态与兼容性信息：

```bash
docker compose exec openvpn ovpn status
docker compose exec openvpn ovpn healthcheck
docker compose exec openvpn ovpn capabilities
docker compose exec openvpn ovpn version
```

## 持久化数据与备份

`./openvpn-data` 保存 CA 私钥、服务端与客户端私钥、profile、tls-crypt 密钥和实例元数据。必须限制此目录的访问权限，并安全备份。

一致性备份至少应包含 `config/`、`meta/`、`pki/`、`secrets/` 与 `ccd/`；也建议保留 `clients/`，其中的 profile 可能是恢复所需的冗余材料。推荐在停止服务后备份。恢复数据后，先运行 `doctor`，审阅 `repair --plan`，再启动服务。

不要误删 bind mount 或让它指向新的空目录：空目录会被有意视为创建新 VPN 实例的请求。

## 安全说明

- 默认设计将 CA 保持在持久化数据卷内，便于日常运维；数据卷被攻破可能导致 CA 泄露。
- 私钥和导出的 `.ovpn` profile 都是敏感凭据，应以严格权限保存并经受信任渠道交付。
- 容器只在自己的网络命名空间内修改转发和防火墙规则。主机防火墙、云安全组与端口转发仍由运维人员负责。
- 稳定版本发布前会校验源码校验和、runtime 版本、配置加载以及所需能力。

## 镜像、构建与发布

Docker Hub 稳定镜像仅使用 OpenVPN runtime 版本作为 tag：

```text
szcq/openvpn:<OPENVPN_VERSION>
```

生产环境请固定明确 tag，不要依赖会变化的 tag。GitHub Container Registry 还会接收用于项目发布管理的镜像版本 tag。

本地构建当前源码树：

```bash
scripts/docker-build.sh -t szcq/openvpn-server:dev .
OVPN_IMAGE=szcq/openvpn-server:dev docker compose up -d
```

GitHub Actions 会执行兼容性、容器、E2E、升级状态和多架构门禁。默认分支的 Candidate 会发布 GHCR 候选镜像；Candidate 成功后会自动触发 Release，创建稳定 GHCR tag 并发布 Docker Hub 的 OpenVPN 版本 tag。

维护者注意：若 `DOCKER_TOKEN` 过期，在 `Settings -> Secrets and variables -> Actions` 更新它，然后手动启动一个默认分支的 Candidate。Candidate 成功会排队一个新的 Release。更新仓库 secret 后，不要依赖重跑旧的 Release。

## 开发

版本与发布输入集中在 `versions.env`。CI 会校验 OpenVPN 版本、源码校验和、支持范围和项目镜像版本。

修改控制面或工作流代码前，可运行以下本地检查：

```bash
tests/check.sh
tests/cli-smoke.sh
tests/workflow-smoke.sh
```

容器与 E2E 测试使用 `OVPN_NETWORK=10.88.0.0/24`，避免常见的 `10.8.0.0/24` 测试冲突。部分检查需要 Docker、可访问的源码下载网络和 `/dev/net/tun`。

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

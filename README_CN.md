# OpenVPN 服务端 Docker 镜像

[English](README.md)

本镜像使用 Go 控制面和 SQLite schema 4 运行 OpenVPN Community Edition，适合在
Linux 主机上部署基于证书的 IPv4 TUN VPN，不提供 Web 管理界面。

## v4 提供的能力

- CLI、entrypoint、OpenVPN hook、进程监督器和 management broker 均为 Go
  二进制；镜像不再包含 Python 或旧的运行时 Shell 控制面。
- `/etc/openvpn/meta/state.db` 是配置、客户端、地址、artifact 元数据、审计和
  operation 状态的唯一结构化权威来源。
- Easy-RSA 仍是 PKI 签发权威。证书、私钥、CRL、tls-crypt、profile、CCD 和日志
  仍作为数据目录中的文件保存。
- 使用严格 YAML 声明期望配置，拒绝未知字段、重复字段、错误类型、null 和多文档。
- 支持 IPv4 TUN、静态/动态地址、NAT、路由与 DNS 推送以及客户端互访。
- 公网传输可使用 IPv4 或 IPv6 上的 UDP/TCP；IPv6 隧道地址和双栈 VPN 数据平面
  尚未实现。
- 从校验和固定的 OpenVPN 源码构建 `linux/amd64` 和 `linux/arm64` 镜像。

当前不提供 Web UI、TAP、LDAP/RADIUS/OIDC、Kubernetes、PostgreSQL/MySQL 或 HA。

## 快速开始

### 环境要求

- Docker Engine 和 Docker Compose plugin。
- Linux 主机提供 `/dev/net/tun`，并允许容器使用 `NET_ADMIN`。
- 客户端可访问的公网域名或 IP，主机与云防火墙开放所选 OpenVPN 端口。
- 一个不与服务器和客户端现有网络重叠的私有 IPv4 CIDR。

### 创建配置

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
```

创建 `openvpn-config/config.yaml`：

```yaml
version: 1

server:
  endpoint: vpn.example.com
  transport:
    protocol: udp
    family: auto
    port: 1194
  clientToClient: true

ipv4:
  network: 10.42.0.0/24
  dynamicPoolSize: 64
  nat:
    enabled: false
    interface: auto
  redirectGateway: false
  dns: []
  routes: []

logging:
  maxBytes: 10485760
  backups: 5
```

除 `version: 1` 外，只有 `server.endpoint` 与 `ipv4.network` 必填。其他字段使用
上述默认值；`dynamicPoolSize` 未设置时取可用客户端地址数的一半。

创建 `compose.yaml`：

```yaml
x-openvpn-data: &openvpn-data
  volumes:
    - ./openvpn-data:/etc/openvpn
    - ./openvpn-config:/etc/openvpn-config

services:
  openvpn:
    image: ${OVPN_IMAGE:-szcq/openvpn:2.7.5}
    container_name: openvpn
    restart: unless-stopped
    network_mode: host
    <<: *openvpn-data
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun

  openvpn-maintenance:
    image: ${OVPN_IMAGE:-szcq/openvpn:2.7.5}
    restart: "no"
    network_mode: host
    environment:
      OVPN_MAINTENANCE: "true"
    <<: *openvpn-data
    profiles:
      - maintenance
    entrypoint:
      - /usr/local/bin/ovpn
    command:
      - state
      - doctor
```

Docker Hub tag 使用镜像内 OpenVPN 版本。这里的项目镜像版本为 4.0.0，内含
OpenVPN 2.7.5；生产环境应固定明确 tag。

### 初始化并启动

```bash
docker compose up -d openvpn
docker compose logs -f openvpn
```

entrypoint 只会初始化空数据目录，新实例必须提供有效 YAML。初始化在 staging 中
创建 SQLite、PKI、服务端身份、CRL、tls-crypt 和派生文件，验证后统一安装。

YAML 是期望配置，SQLite 保存最近一次经操作员确认的 applied revision。之后 YAML
缺失或与 applied revision 不同时，`server run` 只告警并继续使用数据库快照，不会
自动应用配置。

### 创建并导出客户端

```bash
# 自动选择最低可用静态 IPv4
docker compose exec openvpn ovpn client create laptop --ipv4 auto

# 动态地址
docker compose exec openvpn ovpn client create phone --ipv4 dynamic

# 指定静态地址
docker compose exec openvpn ovpn client create tablet --ipv4 10.42.0.20

docker compose exec -T openvpn \
  ovpn client export --name laptop --output - > laptop.ovpn
```

将 profile 导入 OpenVPN 客户端。profile 内含私钥，必须按凭据安全传输和保存。

## 配置工作流

只验证或预览 YAML，不修改 applied 状态：

```bash
docker compose exec openvpn ovpn config validate
docker compose exec openvpn ovpn config plan
```

配置应用必须离线进行：

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance config plan
docker compose run --rm openvpn-maintenance config apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
```

plan 会列出重启、地址重映射、防火墙 reconcile、派生文件和 profile 重分发影响。
网段和动态池变化统一由 `config apply` 处理，不再存在独立的在线 network apply。

## 常用操作

```bash
docker compose exec openvpn ovpn client list --detail
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime logs --lines 100 --follow
docker compose exec openvpn ovpn runtime events --lines 100 --json

docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
docker compose run --rm openvpn-maintenance repair apply --yes
```

已有客户端必须用互斥的 `--name` 或 `--id` 选择。`--id` 接受至少八位且唯一的
UUID 十六进制前缀。可能删除或批量改写状态的命令需要交互确认或 `--yes`。

## schema 3 迁移

v4 镜像只直接迁移 schema 3。schema 1/2 必须先使用 `sh-ver` 镜像升级到 schema 3。

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance migrate plan
docker compose run --rm openvpn-maintenance migrate apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance \
  config export --output /etc/openvpn-config/config.yaml
docker compose up -d openvpn
```

迁移安装 schema 4 前会创建
`/etc/openvpn/repair/migrations/schema3-pre-v4.tar.gz` 及其 SHA-256 sidecar。
迁移成功后若要回滚，必须停止全部容器、校验并恢复完整快照，再运行 `sh-ver` 镜像；
只切换镜像不等于数据回滚。

## 备份与恢复

数据库和全部 PKI/artifact 文件是同一个恢复单元。不能只复制 `state.db`，也不能在
服务仍可能写入时复制数据库。操作员备份应先停止服务，同时归档两个挂载目录：

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-backup.tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

恢复时保持服务停止，将备份解压到空目标目录并保留所有者和权限，启动前先运行
`state doctor`。备份包含 CA 和客户端私钥，必须加密并限制访问。

## 文档

- [v4 命令参考](docs/cn/v4/commands.md)
- [v4 操作手册](docs/cn/v4/operations.md)
- [数据 schema 升级政策](docs/cn/data-schema-upgrade-policy.md)
- [镜像更新政策](docs/cn/image-update-policy.md)
- 历史版本：[v1](docs/cn/v1/commands.md)、[v2](docs/cn/v2/commands.md)、
  [v3](docs/cn/v3/commands.md)

## 开发

版本输入集中在 `versions.env`。Go 依赖使用 `GOPROXY=direct`，构建脚本把宿主机的
标准代理变量转发给 Docker。

```bash
scripts/verify-go-toolchain.sh
tests/smoke/shell/check.sh
tests/smoke/shell/workflow-smoke.sh
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

CI 覆盖 Go format、vet、unit/race、依赖许可证、保留的 Shell 契约、schema 3
迁移/回滚、真实 UDP/TCP 隧道以及 amd64/arm64 构建。

## 许可证

Copyright (C) 2026 yjrszcq。

项目原创源码和构建配置采用 [GPL-2.0-only](LICENSE)。[NOTICE](NOTICE) 记录第三方
边界；OpenVPN、Easy-RSA、Go modules 和系统包继续使用各自许可证。

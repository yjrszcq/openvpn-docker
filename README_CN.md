# OpenVPN 服务端 Docker 镜像

[English](README.md)

本镜像使用 Go 控制面和 SQLite schema 4 运行 OpenVPN Community Edition，适合在 Linux 主机上部署基于证书的 IPv4 TUN VPN，不提供 Web 管理界面。

## v4 提供的能力

- CLI、entrypoint、OpenVPN hook、进程监督器和 management broker 均为 Go 二进制；镜像不再包含 Python 或旧的运行时 Shell 控制面。
- `/etc/openvpn/meta/state.db` 是配置、客户端、地址、artifact 元数据、审计和 operation 状态的唯一结构化权威来源。
- Easy-RSA 仍是 PKI 签发权威。证书、私钥、CRL、tls-crypt、profile、CCD 和日志仍作为数据目录中的文件保存。
- 使用严格 YAML 声明期望配置，拒绝未知字段、重复字段、错误类型、null 和多文档。
- 支持 IPv4 TUN、静态/动态地址、NAT、路由与 DNS 推送以及客户端互访。
- 公网传输可使用 IPv4 或 IPv6 上的 UDP/TCP；IPv6 隧道地址和双栈 VPN 数据平面尚未实现。
- 从校验和固定的 OpenVPN 源码构建 `linux/amd64` 和 `linux/arm64` 镜像。

当前不提供 Web UI、TAP、LDAP/RADIUS/OIDC、Kubernetes、PostgreSQL/MySQL 或 HA。

## 快速开始

### 环境要求

- Docker Engine 和 Docker Compose plugin。
- Linux 主机提供 `/dev/net/tun`，并允许容器使用 `NET_ADMIN`。
- 客户端可访问的公网域名或 IP，主机与云防火墙开放所选 OpenVPN 端口。
- 一个不与服务器和客户端现有网络重叠的私有 IPv4 CIDR。

### 创建部署

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
```

创建 `compose.yaml`。以下版本可以独立使用，不需要 `.env` 文件。启动前必须替换 `vpn.example.com`，并选择不重叠的 IPv4 网段：

```yaml
x-openvpn-data: &openvpn-data
  volumes:
    - ./openvpn-data:/etc/openvpn
    - ./openvpn-config:/etc/openvpn-config

services:
  openvpn:
    image: szcq/openvpn:2.7.5
    container_name: openvpn
    restart: unless-stopped
    network_mode: host
    environment:
      OVPN_BOOTSTRAP_FROM_ENV: "true"
      OVPN_BOOTSTRAP_ENDPOINT: vpn.example.com
      OVPN_BOOTSTRAP_PROTOCOL: udp
      OVPN_BOOTSTRAP_FAMILY: auto
      OVPN_BOOTSTRAP_PORT: "1194"
      OVPN_BOOTSTRAP_CLIENT_TO_CLIENT: "true"
      OVPN_BOOTSTRAP_IPV4_NETWORK: 10.42.0.0/24
      OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE: "64"
      OVPN_BOOTSTRAP_NAT_ENABLED: "false"
      OVPN_BOOTSTRAP_NAT_INTERFACE: auto
      OVPN_BOOTSTRAP_REDIRECT_GATEWAY: "false"
      OVPN_BOOTSTRAP_DNS: ""
      OVPN_BOOTSTRAP_ROUTES: ""
      OVPN_BOOTSTRAP_LOG_MAX_BYTES: "10485760"
      OVPN_BOOTSTRAP_LOG_BACKUPS: "5"
    <<: *openvpn-data
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun

  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
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

Docker Hub tag 使用镜像内 OpenVPN 版本。这里的项目镜像版本为 4.0.0，内含 OpenVPN 2.7.5；生产环境应固定明确 tag。

该示例会在空的 `openvpn-config` 目录中生成第一份规范 YAML。首次启动成功后，将 `OVPN_BOOTSTRAP_FROM_ENV` 改为 `"false"`；之后 bootstrap 变量会被忽略，绝不会覆盖 YAML 或 SQLite。若希望从一开始就手动管理 YAML，请复制并编辑 [config.example.yaml](config.example.yaml)，将 bootstrap 开关设为 `"false"`，并删除其余 `OVPN_BOOTSTRAP_*` 项。除 `version: 1` 外，只有 `server.endpoint` 与 `ipv4.network` 必填。

### 初始化并启动

```bash
docker compose up -d openvpn
docker compose logs -f openvpn
```

entrypoint 只会初始化空数据目录，新实例必须提供有效 YAML 或启用 bootstrap 环境变量。环境初始化会先写入规范 YAML，再在 staging 中创建 SQLite、PKI、服务端身份、CRL、tls-crypt 和派生文件，验证后统一安装。

YAML 是期望配置，SQLite 保存最近一次经操作员确认的 applied revision。之后 YAML 缺失或与 applied revision 不同时，`server run` 只告警并继续使用数据库快照，不会自动应用配置。

### 创建并导出客户端

```bash
# 自动选择最低可用静态 IPv4，并直接输出 profile
docker compose exec -T openvpn \
  ovpn client create laptop --ipv4 --output - > laptop.ovpn

# 动态地址
docker compose exec openvpn ovpn client create phone --ipv4 dynamic

# 指定静态地址
docker compose exec openvpn ovpn client create tablet --ipv4 10.42.0.20

chmod 600 laptop.ovpn
```

将 profile 导入 OpenVPN 客户端。profile 内含私钥，必须按凭据安全传输和保存。

## 环境变量

服务端持久配置应写入声明式 YAML。环境变量只用于配置 Compose、文件系统路径、一次性初始化、maintenance 授权和开发覆盖，并不是第二套长期配置来源。

### 部署与运维

| 变量 | 运行时默认值 / Compose 回退值 | `.env.example` 值 | 说明 |
|---|---|---|---|
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | `szcq/openvpn:2.7.5` | Compose 使用的镜像。生产环境应固定已发布 tag。 |
| `OVPN_CONFIG_FILE` | `/etc/openvpn-config/config.yaml` | 未设置 | 期望状态声明式 YAML 的路径。 |
| `OVPN_DATA_DIR` | `/etc/openvpn` | 未设置 | 保存 SQLite、PKI、artifact、日志和锁的持久数据目录。 |
| `OVPN_RUNTIME_DIR` | `/run/openvpn-container` | 未设置 | 保存 runtime socket 和服务进程锁的临时目录。 |
| `OVPN_MAINTENANCE` | 未设置 | 未设置 | `migrate apply` 要求该值严格等于 `true`；Compose maintenance 服务会自动设置。 |
| `OVPN_EDITOR` | `EDITOR`，然后 `nano` | 未设置 | `client address edit` 使用的编辑器命令。 |
| `EDITOR` | `nano` | 未设置 | 未设置 `OVPN_EDITOR` 时使用的标准后备编辑器。 |

### 一次性环境变量初始化

仅当空的 schema 4 实例以 `OVPN_BOOTSTRAP_FROM_ENV=true` 初始化时才读取这些变量，并据此生成第一份规范 YAML。初始化完成后，程序只会告警并忽略这些变量，绝不会覆盖 YAML 或 SQLite；首次启动成功后应将开关设为 `false`。

| 变量 | 初始配置默认值 | `.env.example` 值 | 说明 |
|---|---|---|---|
| `OVPN_BOOTSTRAP_FROM_ENV` | `false` | `true` | 启用通过其余 bootstrap 变量生成初始 YAML。 |
| `OVPN_BOOTSTRAP_ENDPOINT` | 必填 | `vpn.example.com` | 写入客户端 profile 的公网域名或 IP，启动前必须替换示例值。 |
| `OVPN_BOOTSTRAP_PROTOCOL` | `udp` | `udp` | 公网传输协议：`udp` 或 `tcp`。 |
| `OVPN_BOOTSTRAP_FAMILY` | `auto` | `auto` | 公网传输地址族：`auto`、`ipv4` 或 `ipv6`；不会因此启用 IPv6 隧道地址。 |
| `OVPN_BOOTSTRAP_PORT` | `1194` | `1194` | OpenVPN 监听端口。 |
| `OVPN_BOOTSTRAP_CLIENT_TO_CLIENT` | `true` | `true` | 允许 VPN 客户端直接互访。 |
| `OVPN_BOOTSTRAP_IPV4_NETWORK` | 必填 | `10.42.0.0/24` | `/30` 至 `/0` 范围内规范且不重叠的 IPv4 隧道网段。 |
| `OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE` | 可用客户端地址数的一半 | `64` | 从可用地址尾部划出的动态池；`0` 表示禁用动态池。 |
| `OVPN_BOOTSTRAP_NAT_ENABLED` | `false` | `false` | 对离开 VPN 网络命名空间的客户端流量执行 NAT。 |
| `OVPN_BOOTSTRAP_NAT_INTERFACE` | `auto` | `auto` | NAT 出口接口；`auto` 表示从路由表解析。 |
| `OVPN_BOOTSTRAP_REDIRECT_GATEWAY` | `false` | `false` | 将客户端默认流量通过 VPN 转发。 |
| `OVPN_BOOTSTRAP_DNS` | 空 | 空 | 推送给客户端的逗号分隔 IPv4 DNS 服务器。 |
| `OVPN_BOOTSTRAP_ROUTES` | 空 | 空 | 推送给客户端的逗号分隔规范 IPv4 CIDR。 |
| `OVPN_BOOTSTRAP_LOG_MAX_BYTES` | `10485760` | `10485760` | 持久 OpenVPN 日志触发轮转前的最大字节数。 |
| `OVPN_BOOTSTRAP_LOG_BACKUPS` | `5` | `5` | 保留的轮转日志备份数量；`0` 表示不保留备份。 |

### 开发与测试覆盖

这些变量会替换可信文件、可执行文件或宿主网络接口。生产镜像已提供有效默认值，常规部署不应设置。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OVPN_COMPATIBILITY_FILE` | `/usr/local/share/openvpn-container/compatibility/contract.json` | 渲染、初始化和 repair 读取的兼容性契约。 |
| `OVPN_TEMPLATE_ROOT` | `/usr/local/share/openvpn-container/templates` | compatibility contract 所选模板族的根目录。 |
| `OVPN_OPENVPN_BIN` | `openvpn` | runtime 监督、PKI 校验和 capability 检查使用的 OpenVPN 可执行文件。 |
| `OVPN_BROKER_BIN` | `ovpn-broker` | `server run` 监督的 management broker 可执行文件。 |
| `OVPN_EASYRSA_BIN` | `/usr/share/easy-rsa/easyrsa`，否则 `easyrsa` | PKI 生命周期操作使用的 Easy-RSA 可执行文件。 |
| `OVPN_IP_BIN` | `ip` | 网络 reconcile 使用的 Linux `ip` 可执行文件。 |
| `OVPN_IPTABLES_BIN` | `iptables` | 防火墙 reconcile 使用的 Linux `iptables` 可执行文件。 |
| `OVPN_IP_FORWARD_FILE` | `/proc/sys/net/ipv4/ip_forward` | 网络 reconcile 使用的宿主机 IPv4 forwarding 控制文件。 |

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

plan 会列出重启、地址重映射、防火墙 reconcile、派生文件和 profile 重分发影响。网段和动态池变化统一由 `config apply` 处理，不再存在独立的在线 network apply。

## 常用操作

```bash
docker compose exec openvpn ovpn client list --detail
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime disconnect laptop
docker compose exec openvpn ovpn runtime logs --lines 100 --follow
docker compose exec openvpn ovpn runtime events --lines 100 --json

docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
docker compose run --rm openvpn-maintenance repair apply --yes
```

已有客户端可用位置参数 `NAME`、显式 `--name NAME` 或 `--id ID` 选择。未提供 `--name` 或 `--id` 时，位置参数默认按客户端名称处理。`--id` 接受至少八位且唯一的 UUID 十六进制前缀。可能删除或批量改写状态的命令需要交互确认或 `--yes`。

`ovpn client`、`ovpn state`、`ovpn runtime` 分别默认执行 `list`、`doctor`、`status`。所有客户端 mutation 支持 `--json`；create/reissue 可同时用 `--output` 返回新 profile。revoke、reissue、delete 和地址变更在持久化提交后会尝试断开受影响的在线 session。若出现 runtime warning，说明状态变更已经成功，可用 `runtime disconnect` 手动重试。

无需额外 CLI 框架即可生成 shell completion：

生成脚本补全的是名为 `ovpn` 的直接命令。可在服务容器的交互 shell 中使用，或在宿主机定义一个名为 `ovpn`、内部执行 `docker compose exec openvpn ovpn` 的 wrapper。若通过 Compose 生成脚本，把下面的 `ovpn completion` 换成 `docker compose exec -T openvpn ovpn completion`。动态 name/ID 补全同样依赖该直接命令或 wrapper。

```bash
mkdir -p ~/.local/share/bash-completion/completions ~/.zfunc \
  ~/.config/fish/completions
ovpn completion bash > ~/.local/share/bash-completion/completions/ovpn
ovpn completion zsh > ~/.zfunc/_ovpn
ovpn completion fish > ~/.config/fish/completions/ovpn.fish
```

## schema 3 迁移

v4 镜像只直接迁移 schema 3。schema 1/2 必须先使用 `sh-ver` 镜像升级到 schema 3。

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance migrate plan
docker compose run --rm openvpn-maintenance migrate apply --yes
docker compose run --rm openvpn-maintenance state doctor
umask 077
docker compose run --rm -T openvpn-maintenance \
  config export --output - > openvpn-config/config.yaml.new &&
  mv openvpn-config/config.yaml.new openvpn-config/config.yaml
docker compose up -d openvpn
```

迁移安装 schema 4 前会创建 `/etc/openvpn/repair/migrations/schema3-pre-v4.tar.gz` 及其 SHA-256 sidecar。迁移成功后若要回滚，必须停止全部容器、校验并恢复完整快照，再运行 `sh-ver` 镜像；只切换镜像不等于数据回滚。

## 备份与恢复

数据库和全部 PKI/artifact 文件是同一个恢复单元。不能只复制 `state.db`，也不能在服务仍可能写入时复制数据库。操作员备份应先停止服务，同时归档两个挂载目录：

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-backup.tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

恢复时保持服务停止，将备份解压到空目标目录并保留所有者和权限，启动前先运行 `state doctor`。备份包含 CA 和客户端私钥，必须加密并限制访问。

## 文档

- [v4 命令参考](docs/cn/v4/commands.md)
- [v4 操作手册](docs/cn/v4/operations.md)
- [数据 schema 升级政策](docs/cn/data-schema-upgrade-policy.md)
- [镜像更新政策](docs/cn/image-update-policy.md)
- 历史版本：[v1](docs/cn/v1/commands.md)、[v2](docs/cn/v2/commands.md)、[v3](docs/cn/v3/commands.md)

## 开发

版本输入集中在 `versions.env`。Go 依赖使用 `GOPROXY=direct`，构建脚本把宿主机的标准代理变量转发给 Docker。

```bash
scripts/verify-go-toolchain.sh
tests/smoke/shell/check.sh
tests/smoke/shell/workflow-smoke.sh
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

CI 覆盖 Go format、vet、unit/race、依赖许可证、保留的 Shell 契约、schema 3 迁移/回滚、真实 UDP/TCP 隧道以及 amd64/arm64 构建。

## 许可证

Copyright (C) 2026 yjrszcq。

项目原创源码和构建配置采用 [GPL-2.0-only](LICENSE)。[NOTICE](NOTICE) 记录第三方边界；OpenVPN、Easy-RSA、Go modules 和系统包继续使用各自许可证。

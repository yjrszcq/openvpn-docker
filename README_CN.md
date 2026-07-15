# OpenVPN 服务端 Docker 镜像

[English](README.md)

这是一个用于运行 OpenVPN Community Edition 服务端的 Docker 镜像，提供面向运维的 Shell 控制面。它适用于家庭实验室、小型团队和需要基于证书的 IPv4 TUN VPN 接入的 Linux 服务器，不提供 Web 管理界面。

## 功能

- 从校验和固定的源码构建 OpenVPN runtime，支持 `linux/amd64` 和 `linux/arm64`。
- 首次启动空持久化目录时，自动创建 PKI、服务端身份、CRL、tls-crypt 密钥和 OpenVPN 配置。
- 支持 UDP/TCP、IPv4 NAT、路由推送、全隧道、DNS 推送和客户端互访。
- 通过 `add-client`、`export-client`、`list-clients`、`revoke-client` 管理客户端证书与 profile。
- 使用统一 client-IP 清单管理静态分配和隔离的动态池；CCD 为派生状态，须显式应用，并支持安全回滚。
- 在启动前检测持久化状态不一致；只执行安全或字节等价的修复；关键状态默认拒绝启动。
- 提供低权限的 maintenance 服务，用于诊断和修复。

## 范围

本镜像支持 IPv4 TUN、双向证书认证、Easy-RSA、tls-crypt 和 CRL。暂不支持 Web UI、TAP、IPv6、外部或离线 CA 工作流、LDAP/RADIUS/OIDC，以及 Kubernetes 集成。

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
      - ./openvpn-runtime:/var/lib/openvpn
    environment:
      OVPN_ENDPOINT: vpn.example.com
      OVPN_PROTO: udp
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

仓库中的 `docker-compose.example.yaml` 也包含下文说明的可选
`openvpn-maintenance` 服务。默认示例使用 host 网络模式：OpenVPN 直接监听宿主机，
因此不使用 Docker 的 `ports:` 映射。启动前须在主机与云防火墙中开放 `1194/udp`。
host 网络模式会使 VPN 网关地址成为宿主机地址，`NET_ADMIN` 因而会影响宿主机网络，
仅应在受控的 Linux 主机上使用。启用 NAT、路由推送或全隧道路由时，服务还会在该主机
网络命名空间中修改 IPv4 转发和 iptables 规则；应审阅主机上的最终规则，且不要将这种
布局用于不受信任的工作负载。


将 `vpn.example.com` 替换为客户端实际连接的公网域名或 IP。请按部署环境选择未使用的网段；示例没有假定 `10.8.0.0/24` 一定可用。

启动服务：

```bash
docker compose up -d
docker compose logs -f openvpn
```

首次启动只会初始化空的 `./openvpn-data` 目录。初始化配置会写入 `config/project.env`；之后修改 bootstrap 环境变量不会重写已有实例。

host 网络模式下，OpenVPN 直接监听 `OVPN_PORT` 与 `OVPN_PROTO`。修改其中任一值后，
还必须在主机与云防火墙中开放同一端口和协议。

## 配置项

| 变量 | 运行时默认值 / Compose 回退值 | 快速开始值 | 说明 |
| --- | --- | --- | --- |
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | `szcq/openvpn:2.7.5` | Compose 使用的镜像，生产环境请固定到已发布的 OpenVPN 版本 tag。 |
| `OVPN_ENDPOINT` | 必填 | `vpn.example.com` | 首次初始化时写入客户端 profile 的公网域名或 IP。 |
| `OVPN_PROTO` | `udp` | `udp` | 传输协议：`udp` 或 `tcp`。 |
| `OVPN_PORT` | `1194` | `1194` | OpenVPN 监听端口。 |
| `OVPN_NETWORK` | `10.8.0.0/24` | `10.42.0.0/24` | IPv4 隧道网段，必须选择无重叠且规范的 CIDR。 |
| `OVPN_TOPOLOGY` | `subnet` | `subnet` | 必须使用的 IPv4 拓扑；不接受其他拓扑。 |
| `OVPN_DYNAMIC_POOL_SIZE` | 可用客户端地址数的一半 | `64` | 从可用地址尾部划出的动态池；0 和全部可用地址都是合法边界。 |
| `OVPN_NAT` | `false` | `false` | 对离开 VPN 网络命名空间的客户端流量执行 NAT。 |
| `OVPN_NAT_INTERFACE` | `auto` | `auto` | NAT 出口接口，也可指定 Linux 接口名。 |
| `OVPN_REDIRECT_GATEWAY` | `false` | `false` | 将客户端默认流量经 VPN 转发。 |
| `OVPN_CLIENT_TO_CLIENT` | `true` | `true` | 允许 VPN 客户端直接互访。 |
| `OVPN_DNS` | 空 | 空 | 推送给客户端的逗号分隔 IPv4 DNS 服务器。 |
| `OVPN_ROUTES` | 空 | 空 | 推送给客户端的逗号分隔 IPv4 CIDR。 |
| `OVPN_CRITICAL_MODE` | `exit` | `exit` | 仅在需要保留关键状态容器进行排障时使用 `maintenance`。 |

运行时默认值只在环境未提供相应值时生效。快速开始值是
`docker-compose.example.yaml` 与 `.env.example` 中有意选择的值，并不是另一套运行时默认值。

对于前缀长度为 `p` 的规范 CIDR，可用客户端地址数为 `2^(32-p)-3`：网络地址、
服务端地址（`网络地址 + 1`）和广播地址均被保留。例如，`10.42.0.0/24` 可提供 253 个
客户端地址（`10.42.0.2` 至 `10.42.0.254`），因此动态池可设为 `0` 至 `253`，未设置时
默认取 `floor(253 / 2) = 126`。最小 `/30` 网段恰好只提供一个客户端地址（`.2`）。

动态池始终占用可用地址区间末尾的一段连续地址，前面的剩余部分就是静态区。例如，
`10.42.0.0/24` 配合 `OVPN_DYNAMIC_POOL_SIZE=64` 时，动态客户端使用
`10.42.0.191` 至 `10.42.0.254`，静态分配可使用 `10.42.0.2` 至
`10.42.0.190`。静态容量等于可用客户端地址数减去动态池大小。

应明确选择路由方式：

- 私网路由：保持 `OVPN_NAT=false` 与 `OVPN_REDIRECT_GATEWAY=false`，并用
  `OVPN_ROUTES` 推送可访问的私网路由。每个目标网络都必须经 VPN 主机拥有到 VPN CIDR
  的返回路由。
- Internet 全隧道：设置 `OVPN_NAT=true` 与
  `OVPN_REDIRECT_GATEWAY=true`。除非主机存在多个候选出口接口，否则保持
  `OVPN_NAT_INTERFACE=auto`。

Bootstrap 配置是实例事实，而不是普通的运行时覆盖项。查看已持久化的值：

```bash
docker compose exec openvpn ovpn config print
```

### 更改已有配置并应用

修改运行中的实例时，先更新 Compose 配置。例如，将 OpenVPN 从 UDP 改为 TCP，
端口仍为 1194：

```yaml
environment:
  OVPN_PROTO: tcp
  OVPN_PORT: "1194"
```

host 网络模式没有 Docker `ports:` 映射；同时在主机和云防火墙中开放对应的 TCP
端口。随后将完整的当前 Compose 环境写入已有数据目录：

```bash
docker compose config --quiet
docker compose down # 不要使用 `-v`
docker compose run --rm openvpn ovpn config init
docker compose up -d openvpn
docker compose exec openvpn ovpn config print
```

`config init` 只会重写持久化配置，不会删除或重签发客户端证书、私钥和 profile。
它会写入 Compose 环境中的全部配置值，因此运行前必须确保所有 `OVPN_*` 值完整且正确。对于已初始化实例，不要用它修改 OVPN_NETWORK 或 OVPN_DYNAMIC_POOL_SIZE；必须使用下文迁移命令，才能原子重载并在失败时回滚。

当 `OVPN_ENDPOINT`、`OVPN_PROTO` 或 `OVPN_PORT` 变化时，需要为每个活动客户端
重新导出并分发 profile。临时文件可避免导出失败时覆盖本地已有 profile：

```bash
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn.tmp &&
mv laptop.ovpn.tmp laptop.ovpn
```

不要为已有名称再次执行 `add-client`。客户端证书仍有效，但客户端必须使用新渲染的
profile 才能连接到新的地址、协议或端口。

健康实例执行 `export-client` 时，会先原子刷新对应的
`clients/active/<name>.ovpn`，再将相同 profile 写入标准输出。若活动 profile 已被
删除，实例会正确进入 `DEGRADED_REPAIRABLE`；应先按当前持久化配置修复，再导出：

```bash
docker compose run --rm openvpn-maintenance repair
```

## 客户端管理

使用标准命令创建客户端证书并导出 profile：

`laptop` 仅是示例客户端名称。请在后续命令中一致地替换为唯一的设备名称，例如
`phone` 或 `nas`。

```bash
docker compose exec openvpn ovpn client create laptop
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn
```

`export-client` 的标准输出只包含 profile，因此重定向时不会混入状态文本。生命周期命令如下：

```bash
docker compose exec openvpn ovpn client list
docker compose exec openvpn ovpn client list --ip
docker compose exec openvpn ovpn client revoke laptop
docker compose exec openvpn ovpn client release-ip laptop
docker compose exec openvpn ovpn client revoke laptop --release-ip
docker compose exec openvpn ovpn client reissue laptop
docker compose exec openvpn ovpn client delete laptop
```

`client revoke` 会将证书写入 CRL、断开客户端，并将其 active profile 移出活动客户端集合；
默认保留 IP 分配。`client revoke --release-ip` 可在吊销时释放静态地址保留。若此前已吊销
但保留了地址，使用 `client release-ip <名称>`；它只接受仍持有静态地址保留的已吊销客户端，
并保留其已吊销 profile、私钥和审计历史。两种释放方式均要求动态池容量非零，才能使被吊销的
清单记录仍然合法。

`client reissue` 会吊销旧证书，为相同 client 名称生成新私钥和证书，并保留原有 IP 分配。
它会先确认镜像中的 Easy-RSA 支持同 CN 重签；不支持时会拒绝且不修改 PKI 索引。完成后须重新
导出并分发新的 profile。

`client delete` 会在必要时吊销活动客户端，然后删除其清单记录、生成的 profile 与私钥。
这应视为不可逆操作：若要恢复旧私钥，只能依赖安全备份。`add-client`、`list-clients` 和
`revoke-client` 仍作为对应标准命令的兼容别名保留。

`client list` 保持兼容的紧凑 `名称 状态` 输出。使用 `client list --ip` 可获得
固定宽度对齐的 `CLIENT`、`STATE`、`MODE`、`IP`、`IP STATE` 和 `CONNECTION` 列。
`CONNECTION=online` 表示本机 OpenVPN 管理 socket 报告该客户端有当前路由；`offline`
表示查询成功但没有该路由；`unknown` 表示 socket 不可用或查询失败。活动静态地址为
`configured`；已吊销但仍占用保留地址的静态客户端为 `retained`。动态地址只有在该路由
提供当前动态地址时才标记为 `connected`，否则从 `pool-persist.txt` 读取的记录标为
`last-known`，无当前或已保存租约时显示 `-` 和 `unavailable`。动态 IP 仅为状态信息，
绝不是地址保留。该视图读取最后已应用清单，直接编辑 CSV 后须待 `client-ip apply`
成功才会反映；兼容别名 `list-clients --ip` 也接受此选项。

## 客户端 IP 管理

data/client-ip.csv 是唯一的 IP 分配事实源：第二列非空表示静态地址，空值表示动态地址。
已应用版本保存在 meta/client-ip.applied.csv；CCD 与
/var/lib/openvpn/pool-persist.txt 都是派生状态，绝不能作为静态地址来源。

使用标准命令创建或调整分配：

~~~bash
docker compose exec openvpn ovpn client create phone
docker compose exec openvpn ovpn client create tablet --dynamic
docker compose exec openvpn ovpn client set-static phone
docker compose exec openvpn ovpn client set-static phone --ip 10.42.0.2
docker compose exec openvpn ovpn client set-dynamic phone
~~~

`client create <名称>` 默认创建静态分配。单个客户端执行
`client set-static <名称>` 而不带 `--ip` 时，会自动分配静态区内数值最小的空闲地址。
仅在必须指定地址时使用 `--ip <IPv4>`：

~~~bash
docker compose exec openvpn ovpn client set-static phone --ip 10.42.0.20
~~~

`--ip` 只能配合一个客户端名称使用，且不能与 `--all` 同用。地址必须位于静态区，
且未被其他客户端占用；冲突会在写入清单、CCD、租约或运行配置之前被拒绝。为同一客户端
重复指定其已有地址是允许的。要明确修改已有静态地址时应指定 `--ip`；省略它表示自动
分配，可能选择另一个空闲地址。

传入多个名称，或使用 `client set-static --all` 时，会通过配置的 `OVPN_EDITOR`
（或 `EDITOR`）打开临时 `client,ip` 清单。填入 `auto` 表示分配最小可用静态地址，
填入具体值表示指定静态 IP。对指定名称的批量修改，留空表示保持动态；`--all` 不允许
留下空 IP。

所有标准客户端分配命令都会在一个事务中立即应用变更，无需在其后再执行
`client-ip apply`。静态变更成功后会立刻更新清单快照和 CCD；如果 OpenVPN 管理 socket
可用，会断开受影响的在线客户端，客户端重连后取得新分配。服务未运行时，持久化分配会在
客户端下次连接时生效。应用或断线请求失败会回滚整个事务。

如需直接编辑，只编辑 ./openvpn-data/data/client-ip.csv，然后显式校验并应用：

~~~bash
docker compose exec openvpn ovpn client-ip validate
docker compose exec openvpn ovpn client-ip apply
~~~

validate 只读。apply 会在共享锁内校验 client 身份、重复或越界地址、静态区与动态池隔离、
容量和 PKI 状态。成功时按静态 IP 数值、动态 CN 字典序排序，重建 CCD，清理受影响动态
租约，并通过本地 root-only 管理 socket 踢出受影响在线 client。被拒绝或事务中途失败时，
草稿会从已应用快照精确恢复。client-ip sync 是 apply 的兼容别名；client-ip edit 只打开草稿。

存在未应用直接编辑时，启动和自动 repair 绝不会采用它；doctor 会报告等待显式应用。

## 网段与动态池迁移

修改隧道 CIDR 或动态池大小属于迁移，不能使用 config init。必须在正在运行的 openvpn
服务内执行（而非低权限 maintenance 容器），因为它需要该容器内的 root-only 本地管理 socket。
`--dry-run` 只生成只读计划，不会连接管理 socket，因此也可在 maintenance 容器中运行；
真正应用计划时才必须在正在运行的 openvpn 服务内执行：

~~~bash
docker compose exec openvpn ovpn network reconfigure \
  --network 10.43.0.0/24 --dynamic-pool-size 96 --dry-run
docker compose exec openvpn ovpn network reconfigure \
  --network 10.43.0.0/24 --dynamic-pool-size 96 --yes
~~~

预览会尽量保留合法静态 IP；冲突时优先保留主机位，再分配最小可用静态地址。确认后会快照
配置、清单、CCD、租约、渲染后的服务端配置和审计日志；通过 SIGHUP 重载 OpenVPN，并等待
管理 socket 与容器健康。重载或健康检查失败时，会恢复快照并重载旧配置。受影响客户端需要
重新连接。

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
      - ./openvpn-runtime:/var/lib/openvpn
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

一致性备份应是两个挂载目录整体，而不是手工挑选的子目录。先停止服务，再归档，才能确保
数据目录、渲染后的服务端配置、CCD、清单快照、审计数据、PKI、client profile、repair
日志与 `./openvpn-runtime/pool-persist.txt` 动态租约文件处于同一时间点：

```bash
docker compose stop openvpn
tar --numeric-owner -C . -czf openvpn-backup-YYYYMMDD.tar.gz \
  openvpn-data openvpn-runtime
```

备份文件必须加密并限制访问。不得把私钥复制到工单、日志或临时恢复记录。恢复时，将两个
目录恢复到原来的挂载路径。若使用最小快速开始 Compose 配置，应先添加上文的 maintenance
服务，再运行 `doctor` 和审阅 `repair --plan`，最后启动服务：

```bash
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance repair --plan
docker compose up -d openvpn
```

不要误删 bind mount 或让它指向新的空目录：空目录会被有意视为创建新 VPN 实例的请求。

## 安全说明

- 默认设计将 CA 保持在持久化数据卷内，便于日常运维；数据卷被攻破可能导致 CA 泄露。
- 私钥和导出的 `.ovpn` profile 都是敏感凭据，应以严格权限保存并经受信任渠道交付。
- 网络规则的作用域取决于所选网络模式。快速开始使用 host 网络模式，共享主机网络命名空间；
  启用 NAT、路由或全隧道时会修改主机的 IPv4 转发和 iptables 规则。隔离容器网络模式下，
  此类修改才留在容器网络命名空间。主机防火墙、云安全组与端口转发仍由运维人员负责。
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

每周运行（也可手动运行）的 Upstream Check 会检查官方 OpenVPN 是否有新版本。发现新版时，
它会推送 `automation/openvpn-<版本>` 分支，并创建指向 `dev` 的 PR。审阅并合并该 PR
到 `dev` 后会运行 PR 检查，但 Candidate 不会从 `dev` 发布；将已审阅的变更从 `dev`
提升到 `main` 才会触发 Candidate 和随后 Release。手动启动 Candidate 时也应选择
`main`。


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

# OpenVPN 操作手册

本文是面向运维人员的操作指南，按工作流组织命令组合用法。单个命令的完整语法和选项
请参阅 [v2 命令参考](commands.md)。

持久化格式变更遵循独立于命令版本的
[数据 schema 升级政策](../data-schema-upgrade-policy.md)。
管理代码与镜像的更新职责遵循长期有效的
[管理代码在线更新政策](../management-update-policy.md)。

## 运行环境约定

- **openvpn 容器**：运行中的在线服务，需要管理 socket。日常客户端操作、配置变更、
  网络迁移在此执行。
- **openvpn-maintenance 容器**：低权限离线容器，不申请 TUN / NET_ADMIN。诊断和
  修复在此执行。

```bash
# 在线容器
docker compose exec openvpn ovpn <command>

# maintenance 容器（一次性运行）
docker compose run --rm openvpn-maintenance <command>
```

`ovpn` 是 maintenance 容器的入口点，`<command>` 即 `ovpn` 之后的命令部分。例如
`state doctor` 相当于执行 `ovpn state doctor`。

如果 compose 文件中还没有 maintenance 服务，在 `services:` 下追加：

```yaml
  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
    restart: "no"
    network_mode: host
    volumes:
      - ./openvpn-data:/etc/openvpn
    environment:
      OVPN_MAINTENANCE: "true"
      HTTPS_PROXY: ${HTTPS_PROXY:-}
      NO_PROXY: ${NO_PROXY:-}
    profiles:
      - maintenance
    command:
      - doctor
    entrypoint:
      - /usr/local/bin/ovpn
```

它挂载同一份持久化数据，但不申请 TUN、`NET_ADMIN` 或公开端口。host 网络也允许
标准代理变量直接使用宿主机代理，包括 `http://127.0.0.1:7890`。

---

## 日常操作

### 创建和分发客户端

显示名称是管理标签和 profile 文件名。OpenVPN 使用生成 profile 注释中记录的不可变
UUID 作为证书 CN 和运行时身份。

```bash
# 创建客户端证书和 IP 分配（默认静态）
docker compose exec openvpn ovpn client create laptop

# 创建动态客户端
docker compose exec openvpn ovpn client create phone --dynamic

# 指定静态 IP 创建
docker compose exec openvpn ovpn client create tablet --ip 10.42.0.10

# 导出 profile
docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn
```

### 查看客户端状态

```bash
# 精简视图（名称 + 不可变 ID + 状态）
docker compose exec openvpn ovpn client list

# 详细视图（七列表格，含 ID、IP 和连接状态）
docker compose exec openvpn ovpn client list --detail
```

### 吊销和释放 IP

```bash
# 吊销证书，保留 IP
docker compose exec openvpn ovpn client revoke laptop

# 吊销证书，同时释放静态 IP
docker compose exec openvpn ovpn client revoke laptop --release-ip

# 吊销后单独释放静态 IP
docker compose exec openvpn ovpn client ip release laptop
```

吊销保留 IP 分配时，客户端状态变为 `revoked`，IP 状态显示为 `retained`。释放后
IP 回到可用池，但已吊销的 profile、私钥和审计历史均保留。

### 重签发证书

```bash
# 保留原 IP 重签
docker compose exec openvpn ovpn client reissue laptop

# 重签并转为动态分配
docker compose exec openvpn ovpn client reissue phone --dynamic

# 重签并指定新静态 IP
docker compose exec openvpn ovpn client reissue tablet --ip 10.42.0.30

# 导出新 profile
docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn
```

已有静态 IP 的客户端重签时保留原 IP；无 IP 的客户端重签时自动分配最低可用
静态地址。使用 `--dynamic` 可转为动态分配，`--ip <addr>` 可指定静态地址。
完成后须重新导出并分发 profile。

### 删除客户端

```bash
docker compose exec openvpn ovpn client delete laptop
```

不可逆。活跃客户端先吊销再删除，同时移除清单记录、profile 和私钥。

---

## IP 地址管理

### 单客户端操作

```bash
# 设为静态（自动分配最低可用地址）
docker compose exec openvpn ovpn client ip set phone

# 设为静态（指定地址）
docker compose exec openvpn ovpn client ip set phone --ip 10.42.0.20

# 设为动态
docker compose exec openvpn ovpn client ip set phone --dynamic
```

### 批量操作

多个客户端或 `--all` 时，命令打开编辑器显示 `client,ip` 清单：

```bash
# 指定客户端批量编辑
docker compose exec openvpn ovpn client ip set phone tablet laptop

# 全部活跃客户端
docker compose exec openvpn ovpn client ip set --all
```

编辑器每行格式为 `client,ip`，支持三种赋值：

```text
phone,auto               # 自动分配最低可用静态地址
tablet,10.42.0.20        # 显式指定静态地址
laptop,                  # 留空保留动态分配
```

编辑器选择顺序为 `OVPN_EDITOR` > `EDITOR` > `nano`。镜像预装 `nano` 和 `vim`。

---

## 配置变更

### 修改端点、协议或端口

以 UDP → TCP 为例：

1. 更新 Compose 环境变量：

   ```yaml
   environment:
     OVPN_PROTO: tcp
     OVPN_PORT: "1194"
   ```

2. 开放防火墙对应端口，然后应用：

   ```bash
   docker compose down          # 不要加 -v
   docker compose run --rm openvpn ovpn config apply
   docker compose up -d openvpn
   ```

3. 重新导出并分发所有活动客户端的 profile。使用临时文件可避免导出失败时覆盖
   本地已有 profile：

   ```bash
   docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn.tmp &&
   mv laptop.ovpn.tmp laptop.ovpn
   ```

> **注意**：`config apply` 只重写持久化配置，不会修改客户端证书、私钥或 IP 分配。
> 不要用它修改 `OVPN_NETWORK` 或 `OVPN_DYNAMIC_POOL_SIZE`——应使用网络迁移命令。

### 查看当前配置

```bash
docker compose exec openvpn ovpn config show
```

### 使用仅 IPv6 可达的公网端点

使用 IPv6 字面量时，`auto` 会在渲染阶段选择 IPv6 传输：

```yaml
environment:
  OVPN_ENDPOINT: 2001:db8::10
  OVPN_PROTO: udp
  OVPN_TRANSPORT_FAMILY: auto
```

使用域名时，发布所需的 A 和/或 AAAA 记录，并保持 `auto`：

```yaml
environment:
  OVPN_ENDPOINT: vpn6.example.com
  OVPN_PROTO: udp
  OVPN_TRANSPORT_FAMILY: auto
```

按上一节的配置变更流程执行 `ovpn config apply`、重启服务并重新导出客户端
profile。服务端使用双栈传输 socket，客户端在连接时解析并尝试 A/AAAA 记录；
`config apply` 不解析 DNS。由于未设置 `bind ipv6only`，服务端 socket 会通过
IPv4-mapped 地址接受 IPv4。只有需要拒绝 IPv4 传输时才改用 `ipv6`。该设置只影响
OpenVPN 外层连接，VPN 内网仍使用 `OVPN_NETWORK`
定义的 IPv4 TUN。若服务器没有 IPv4 出口，现有 IPv4 NAT 无法让客户端访问公网
IPv4；本镜像不提供 NAT64。客户端所在网络也必须能够访问公网 IPv6。

---

## 网络迁移

修改隧道网段或动态池大小必须使用迁移命令，**在在线容器中执行**（需要管理 socket）：

```bash
# 预览迁移计划（只读）
docker compose exec openvpn ovpn network plan \
  --network 10.43.0.0/24 --dynamic-pool-size 96

# 应用迁移
docker compose exec openvpn ovpn network apply \
  --network 10.43.0.0/24 --dynamic-pool-size 96 --yes
```

迁移流程：快照当前状态 → 更新配置和清单 → SIGHUP 重载 OpenVPN → 验证管理 socket
和容器健康。失败时自动回滚并重载旧配置。受影响客户端需重新连接。

> **重要：** 迁移成功后，请同步更新 `docker-compose.yaml` 或 `.env` 中的
> `OVPN_NETWORK`（以及 `OVPN_DYNAMIC_POOL_SIZE`，如有变更）。持久化的
> `project.env` 持有新值并决定重启后的行为，但后续 `ovpn config apply` 会读取
> 环境变量——旧值会导致网络被静默回退。

---

## 诊断与修复

### 查看实例状态

```bash
# 在线容器
docker compose exec openvpn ovpn state show

# maintenance 容器
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance state doctor --json
```

状态取值：`EMPTY`、`HEALTHY`、`DEGRADED_REPAIRABLE`、`DEGRADED_RECOVERABLE`、
`CRITICAL`、`UNRECOVERABLE`。

### 修复降级实例

```bash
# 预览可执行的修复（只读）
docker compose run --rm openvpn-maintenance repair plan

# 应用修复
docker compose run --rm openvpn-maintenance repair apply
```

`repair plan` 列出 `SAFE`（直接重建派生文件）和 `RECOVER`（从备份路径恢复证书/密钥）
操作。`repair apply` 暂存、快照并原子应用允许的修复。`CRITICAL` 状态默认拒绝修复
（退出码 78）；仅在排障需要保留现场时设 `OVPN_CRITICAL_MODE=maintenance`。

### 运行时检查

```bash
docker compose exec openvpn ovpn runtime status       # 运行时状态 JSON
docker compose exec openvpn ovpn runtime health       # 容器健康检查
docker compose exec openvpn ovpn runtime capabilities # 兼容性信息
docker compose exec openvpn ovpn runtime version      # 构建信息
```

---

## 备份与恢复

### 备份

`./openvpn-data` 保存 CA 私钥、服务端与客户端私钥、profile、tls-crypt 密钥、
实例元数据以及动态租约状态。备份此目录：

```bash
docker compose stop openvpn
tar --numeric-owner -C . -czf openvpn-backup-YYYYMMDD.tar.gz \
  openvpn-data
docker compose up -d openvpn
```

备份文件必须加密并限制访问。私钥不得复制到工单、日志或临时恢复记录。

### 恢复

将数据目录还原到原来的挂载路径，然后检查状态再启动：

```bash
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
docker compose up -d openvpn
```

> **警告**：不要将 bind mount 指向新的空目录——空目录会被视为创建新实例的请求。

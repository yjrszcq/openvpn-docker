# OpenVPN v1 操作手册

本文是面向运维人员的操作指南，按工作流组织命令组合用法。对应发布提交 `6619921e5257e604f5df2c63d2fa10505b680d84`。单个命令的完整语法和选项请参阅 [v1 命令参考](commands.md)。

## 运行环境约定

- **openvpn 容器**：运行中的在线服务。日常客户端操作、配置变更、运行时检查在此执行。
- **openvpn-maintenance 容器**：低权限离线容器，不申请 TUN / NET_ADMIN。诊断和修复在此执行。

```bash
# 在线容器
docker compose exec openvpn ovpn <command>

# maintenance 容器（一次性运行）
docker compose run --rm openvpn-maintenance <command>
```

`ovpn` 是 maintenance 容器的入口点，`<command>` 即 `ovpn` 之后的命令部分。例如 `state doctor` 相当于执行 `ovpn state doctor`。

如果 compose 文件中还没有 maintenance 服务，在 `services:` 下追加：

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

它挂载同一份持久化数据，但不申请 TUN、`NET_ADMIN` 或公开端口。

---

## 日常操作

### 创建和分发客户端

```bash
# 创建客户端证书和 profile
docker compose exec openvpn ovpn add-client laptop

# 将 profile 保存到本地
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn
```

`add-client` 一步完成证书、私钥和活跃 profile 的创建。`export-client` 会先原子刷新活跃 profile，再将其写入标准输出。

### 查看客户端状态

```bash
docker compose exec openvpn ovpn list-clients
```

从 Easy-RSA 索引中每行打印一条 `name state` 记录。有效证书状态为 `active`，已吊销证书状态为 `revoked`。

### 吊销客户端

```bash
docker compose exec openvpn ovpn revoke-client laptop
```

吊销证书、重新生成 CRL，并将 profile 从 `clients/active/` 移至 `clients/revoked/`。私钥和证书材料会保留。

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
   docker compose config --quiet
   docker compose down          # 不要加 -v
   docker compose run --rm openvpn ovpn config init
   docker compose up -d openvpn
   docker compose exec openvpn ovpn config print
   ```

3. 重新导出并分发所有活动客户端的 profile。使用临时文件可避免导出失败时覆盖本地已有 profile：

   ```bash
   docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn.tmp &&
   mv laptop.ovpn.tmp laptop.ovpn
   ```

> **注意**：`config init` 只重写持久化配置，不会修改客户端证书、私钥或 profile。不要为已有名称再次执行 `add-client`——重新导出 profile 即可。

### 查看当前配置

```bash
docker compose exec openvpn ovpn config print
```

---

## 诊断与修复

### 查看实例状态

```bash
# 在线容器
docker compose exec openvpn ovpn state

# maintenance 容器
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance doctor --json
```

`state` 打印单个状态名。`doctor` 列出检测到的问题及其严重程度和建议操作；`--json` 输出包含 `state` 和 `issues` 数组的对象。

状态取值：`EMPTY`、`HEALTHY`、`DEGRADED_REPAIRABLE`、`DEGRADED_RECOVERABLE`、`DEGRADED_REISSUABLE`、`CRITICAL`、`UNRECOVERABLE`。

### 修复降级实例

```bash
# 预览修复计划（只读）
docker compose run --rm openvpn-maintenance repair --plan

# 预览并输出 JSON
docker compose run --rm openvpn-maintenance repair --plan --json

# 应用修复
docker compose run --rm openvpn-maintenance repair
```

`repair --plan` 列出建议的 `SAFE` 和 `RECOVER` 操作，不修改任何文件。`repair` 仅在状态为 `HEALTHY`、`DEGRADED_REPAIRABLE` 或 `DEGRADED_RECOVERABLE` 时暂存、验证、快照并原子应用允许的修复。`CRITICAL` 和 `UNRECOVERABLE` 状态默认以退出码 `78` 拒绝修复；仅在排障需要保留现场时设 `OVPN_CRITICAL_MODE=maintenance`。

### 运行时检查

```bash
docker compose exec openvpn ovpn status        # 运行时状态 JSON
docker compose exec openvpn ovpn healthcheck    # 容器健康检查
docker compose exec openvpn ovpn capabilities   # 兼容性信息
docker compose exec openvpn ovpn version        # 构建信息
```

---

## 备份与恢复

### 备份

`./openvpn-data` 保存 CA 私钥、服务端与客户端私钥、profile、tls-crypt 密钥和实例元数据。停止服务后归档以获得一致快照：

```bash
docker compose stop openvpn
tar --numeric-owner -C . -czf openvpn-backup-YYYYMMDD.tar.gz openvpn-data
docker compose up -d openvpn
```

备份文件必须加密并限制访问。不得将私钥复制到工单、日志或临时恢复记录。

### 恢复

将数据目录恢复到原来的挂载路径，然后检查状态再启动：

```bash
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance repair --plan
docker compose up -d openvpn
```

> **警告**：不要将 bind mount 指向新的空目录——空目录会被视为创建新实例的请求。

# OpenVPN v4 操作手册

本文按运维工作流组织 schema 4 CLI。完整参数与退出码见
[v4 命令参考](commands.md)。持久化兼容性遵循
[数据 schema 升级政策](../data-schema-upgrade-policy.md)，镜像交付与回滚遵循
[镜像更新政策](../image-update-policy.md)。

## 容器角色

- `openvpn`：在线服务，拥有 `/dev/net/tun`、`NET_ADMIN`、management broker 和
  OpenVPN。
- `openvpn-maintenance`：挂载相同数据与 YAML 的 one-shot CLI，不请求 TUN 或
  `NET_ADMIN`，并设置 `OVPN_MAINTENANCE=true`。

```bash
# 在线查询或客户端操作
docker compose exec openvpn ovpn client list

# 离线状态、配置或迁移操作
docker compose run --rm openvpn-maintenance state doctor
```

两个服务必须使用相同目标镜像，并挂载同一个 `openvpn-data` 和
`openvpn-config`。

## 首次部署

1. 创建权限受限的数据与配置目录。
2. 编写 `openvpn-config/config.yaml`，确认 endpoint、IPv4 网段、路由和 NAT。
3. 启动服务；entrypoint 只初始化空数据目录。
4. 签发客户端前检查 state 和 runtime health。

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
$EDITOR openvpn-config/config.yaml

docker compose up -d openvpn
docker compose logs openvpn
docker compose exec openvpn ovpn state doctor
docker compose exec openvpn ovpn runtime health
```

YAML 缺失/错误、数据目录非空但无法识别、PKI 生成失败或 staging 状态验证失败时，
初始化都会 fail closed。

## 明确选择路由方式

只访问私网：

```yaml
ipv4:
  nat:
    enabled: false
    interface: auto
  redirectGateway: false
  routes:
    - 192.168.50.0/24
```

目标网络必须通过 OpenVPN 主机拥有返回 VPN CIDR 的路由。

Internet 全隧道：

```yaml
ipv4:
  nat:
    enabled: true
    interface: auto
  redirectGateway: true
  dns:
    - 1.1.1.1
    - 8.8.8.8
```

使用 `network_mode: host` 时，forwarding 和项目规则位于宿主机网络命名空间。
应检查主机防火墙与云安全组。runtime 只 reconcile 当前实例专属 chain/comment，
不会清空无关规则。

## 日常客户端生命周期

创建并导出：

```bash
docker compose exec openvpn ovpn client create laptop --ipv4
docker compose exec openvpn ovpn client create phone --ipv4 dynamic
docker compose exec openvpn ovpn client create tablet --ipv4 10.42.0.20

docker compose exec -T openvpn \
  ovpn client export laptop --output - > laptop.ovpn
chmod 600 laptop.ovpn
```

查看并使用不可变 ID：

```bash
docker compose exec openvpn ovpn client list --detail
docker compose exec openvpn ovpn client list --full-id
docker compose exec -T openvpn \
  ovpn client export --id 844854e4 --output - > laptop.ovpn
```

改名、吊销、重签和删除：

```bash
docker compose exec openvpn \
  ovpn client rename laptop office-laptop

docker compose exec openvpn \
  ovpn client revoke office-laptop

docker compose exec openvpn \
  ovpn client reissue office-laptop --ipv4
docker compose exec -T openvpn \
  ovpn client export office-laptop --output - > office-laptop.ovpn

docker compose exec openvpn \
  ovpn client delete office-laptop --yes
```

吊销、重签或地址变化后需要断开旧 session；重签后必须分发新 profile。删除保留 UUID
tombstone，但会删除本地凭据，需要恢复能力时必须保留备份。

## 地址管理

```bash
docker compose exec openvpn \
  ovpn client address set --name laptop --ipv4 dynamic
docker compose exec openvpn \
  ovpn client address set phone --ipv4
docker compose exec openvpn \
  ovpn client address set --name tablet --ipv4 10.42.0.30
```

批量变化需要交互编辑或 `--yes`：

```bash
docker compose exec openvpn \
  ovpn client address edit --name laptop --name phone --yes
```

文件中每个选中 active 客户端占一行 `client,ipv4`，值使用 `auto`、`dynamic` 或静态
地址。完整文件统一验证并原子提交。

释放 revoked 客户端保留的静态地址：

```bash
docker compose exec openvpn \
  ovpn client address release --name retired-device
```

## 声明式配置变更

修改 YAML 不会自动改变 applied 状态。先在线验证和预览：

```bash
docker compose exec openvpn ovpn config validate
docker compose exec openvpn ovpn config plan
```

然后停止 OpenVPN，通过 maintenance 服务应用：

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance config validate
docker compose run --rm openvpn-maintenance config plan
docker compose run --rm openvpn-maintenance config apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

apply 前必须阅读 plan。endpoint/transport 变化要求重分发 profile；网段/动态池变化可能
重映射静态地址并重建 CCD/server config；NAT、route 和 redirect-gateway 变化要求重启
后 reconcile 防火墙。

YAML 发生漂移但未 apply 时，重启仍使用旧 applied revision 并输出警告，避免意外文件
编辑直接改变运行服务。

从 applied 状态恢复完整 YAML：

```bash
umask 077
docker compose run --rm -T openvpn-maintenance \
  config export --output - > openvpn-config/config.yaml.new &&
  mv openvpn-config/config.yaml.new openvpn-config/config.yaml
```

## 状态诊断与修复

只读诊断可以在线运行；考虑 repair/restore 时，离线结果更稳定：

```bash
docker compose exec openvpn ovpn state show
docker compose exec openvpn ovpn state doctor --json

docker compose stop openvpn
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
```

只执行 plan 中允许的动作：

```bash
docker compose run --rm openvpn-maintenance repair apply --yes
docker compose run --rm openvpn-maintenance state doctor
```

repair 只能根据互相一致的证据重建派生文件或恢复 artifact，不会猜测重建缺失/损坏的
SQLite 权威库。数据库处于 `CRITICAL`/`UNRECOVERABLE` 时必须恢复可信备份。

## Runtime 检查

```bash
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime status --json
docker compose exec openvpn ovpn runtime capabilities --json
docker compose exec openvpn ovpn runtime logs --lines 200
docker compose exec openvpn ovpn runtime logs --lines 0 --follow
docker compose exec openvpn ovpn runtime logs --raw --full-id
docker compose exec openvpn ovpn runtime events --lines 200 --json
docker compose exec openvpn ovpn runtime events --lines 0 --follow
```

日志按 applied 配置持久化轮转；events 是面向用户的 JSONL。SQLite `audit_events` 是
权威业务审计，不能替代 runtime 日志。

## 从 schema 3 升级

迁移前：

- 确认源实例在 `sh-ver` 镜像下报告 schema 3。
- schema 1/2 先使用 `sh-ver` 升到 schema 3；v4 不读取 schema 1/2。
- 独立备份完整数据目录。
- 为两个 Compose 服务固定同一个目标 v4 镜像。

计划并执行：

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance migrate plan
docker compose run --rm openvpn-maintenance migrate plan --json
docker compose run --rm openvpn-maintenance migrate apply --yes
docker compose run --rm openvpn-maintenance state doctor
umask 077
docker compose run --rm -T openvpn-maintenance \
  config export --output - > openvpn-config/config.yaml.new &&
  mv openvpn-config/config.yaml.new openvpn-config/config.yaml
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

迁移保留 schema 3 的 UUID 证书身份，导入当前 client/address/audit/artifact 状态。成功后
删除 live legacy 结构化文件；原件只保留在迁移快照中。

## 回滚已成功的 schema 迁移

只切换镜像不够；必须恢复完整迁移快照，再运行 `sh-ver`：

```bash
docker compose stop openvpn

sudo cp openvpn-data/repair/migrations/schema3-pre-v4.tar.gz .
sudo cp openvpn-data/repair/migrations/schema3-pre-v4.tar.gz.sha256 .
sudo chown "$(id -u):$(id -g)" schema3-pre-v4.tar.gz schema3-pre-v4.tar.gz.sha256
sha256sum -c schema3-pre-v4.tar.gz.sha256

mkdir openvpn-data-schema3
chmod 750 openvpn-data-schema3
sudo tar --numeric-owner -xzf schema3-pre-v4.tar.gz -C openvpn-data-schema3

mv openvpn-data openvpn-data-schema4
mv openvpn-data-schema3 openvpn-data

# 启动前把 OVPN_IMAGE 指向稳定的 sh-ver 镜像。
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
```

验证完成前保留 `openvpn-data-schema4`。恢复后的 schema 3 数据树与 `sh-ver` 镜像必须
作为匹配单元使用。

## 离线备份与恢复

SQLite 和文件 artifact 始终必须一起备份和恢复：

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-$(date +%Y%m%d%H%M%S).tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

归档包含 CA、服务端/客户端私钥、tls-crypt、profile 和数据库，必须加密保存。

在没有容器运行时解压到空工作目录：

```bash
mkdir restore-work
sudo tar --numeric-owner -xzf openvpn-v4-backup.tar.gz -C restore-work

mv openvpn-data openvpn-data-before-restore
mv openvpn-config openvpn-config-before-restore
mv restore-work/openvpn-data ./openvpn-data
mv restore-work/openvpn-config ./openvpn-config

docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

不要把备份合并进现有目录，也不要只恢复 `state.db`。恢复实例通过诊断并完成至少一次
客户端连接前，保留原目录。

## 不变 schema 的镜像更新

新旧镜像都使用 schema 4 时：

```bash
docker compose stop openvpn
# 更新 OVPN_IMAGE 后：
docker compose pull openvpn openvpn-maintenance
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn version --json
docker compose exec openvpn ovpn runtime health
```

同 schema 镜像更新不要执行 `migrate apply`。

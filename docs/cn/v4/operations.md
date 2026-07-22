# OpenVPN 操作手册

本文按运维工作流说明部署、日常客户端管理、配置、诊断、迁移和恢复。完整语法、参数与退出码见[命令参考](commands.md)。持久化兼容性遵循[数据升级政策](../data-schema-upgrade-policy.md)，镜像交付与回滚遵循[镜像更新政策](../image-update-policy.md)。

## 运行环境约定

- `openvpn`：在线服务，拥有 `/dev/net/tun`、`NET_ADMIN`、management broker 和 OpenVPN。
- `openvpn-maintenance`：挂载相同数据与 YAML 的 one-shot CLI，不请求 TUN 或 `NET_ADMIN`，并设置 `OVPN_MAINTENANCE=true`。

```bash
# 在线查询或客户端操作
docker compose exec openvpn ovpn client list

# 离线状态、配置或迁移操作
docker compose run --rm openvpn-maintenance state doctor
```

两个服务必须使用相同目标镜像，并挂载同一个 `openvpn-data` 和 `openvpn-config`。

在 `docker-compose.yaml` 中将以下服务添加到 `openvpn` 旁边。它有意不配置 `devices`、`cap_add` 或端口映射：

```yaml
  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
    restart: "no"
    network_mode: host
    environment:
      OVPN_MAINTENANCE: "true"
    volumes:
      - ./openvpn-data:/etc/openvpn
      - ./openvpn-config:/etc/ovpn-conf
    profiles:
      - maintenance
    entrypoint:
      - /usr/local/bin/ovpn
    command:
      - state
      - doctor
```

镜像 tag 必须与在线服务固定为同一版本。仅在不指定命令启动该服务时才执行默认的 `state doctor`；`docker compose run --rm openvpn-maintenance config plan` 会用 `config plan` 替换默认命令。

---

## 首次部署

1. 创建权限受限的数据与配置目录。
2. 将仓库根目录的 `config.example.yaml` 复制为 `openvpn-config/config.yaml`，再确认 endpoint、IPv4 网段、路由和 NAT。
3. 启动服务；entrypoint 只初始化空数据目录。
4. 签发客户端前检查 state 和 runtime health。

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
cp config.example.yaml openvpn-config/config.yaml
$EDITOR openvpn-config/config.yaml

docker compose up -d openvpn
docker compose logs openvpn
docker compose exec openvpn ovpn state doctor
docker compose exec openvpn ovpn runtime health
```

YAML 缺失/错误、数据目录非空但无法识别、PKI 生成失败或 staging 状态验证失败时，初始化都会 fail closed。

首次部署若不想手动创建 YAML，可保持配置目录为空，并为 Compose 服务设置以下环境变量。entrypoint 会严格验证、写入 mode `0600` 的规范 YAML，再执行相同初始化流程：

```yaml
services:
  openvpn:
    environment:
      OVPN_BOOTSTRAP_FROM_ENV: "true"
      OVPN_BOOTSTRAP_ENDPOINT: vpn.example.com
      OVPN_BOOTSTRAP_IPV4_NETWORK: 10.42.0.0/24
```

首次启动成功后将 `OVPN_BOOTSTRAP_FROM_ENV=false`。继续保持 true 只会产生已忽略的 bootstrap warning，绝不会修改已初始化实例。可选字段见 [命令参考](commands.md#一次性环境变量初始化)。

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

使用 `network_mode: host` 时，forwarding 和项目规则位于宿主机网络命名空间。应检查主机防火墙与云安全组。runtime 只 reconcile 当前实例专属 chain/comment，不会清空无关规则。

## 日常操作

每个公共多字母参数都有单 token 短形式。以下示例交替使用长短形式；短参数应分开传入，不应聚合。

### 创建和分发客户端

需要把 profile 写到操作员当前目录时，可在同一命令中创建并导出：

```bash
docker compose exec -T openvpn \
  ovpn client create laptop -4 -o - > laptop.ovpn
chmod 600 laptop.ovpn
docker compose exec openvpn ovpn client create phone -4 dynamic
docker compose exec openvpn ovpn client create tablet -4 10.42.0.20
```

profile 内含私钥。导出文件应保持 mode `0600`，并通过安全渠道分发。

### 查看客户端状态

```bash
docker compose exec openvpn ovpn client list -d
docker compose exec openvpn ovpn client list -u
```

详细列的顺序为 `CLIENT ID`、`NAME`、`STATUS`、`CONNECTION`、`IPV4 MODE`、`IPV4 ADDRESS`、`IPV4 STATE`。`STATUS` 表示凭据生命周期状态；runtime broker 可用时 `CONNECTION` 为 `online` 或 `offline`，服务停止或无法查询时为 `unknown`。`IPV4 MODE` 为 `static`、`dynamic` 或 `none`；动态地址显示最近记录的 lease，首次连接前可能为 `-`。`IPV4 STATE` 表示 assignment 状态，例如 `active`、`retained` 或 `none`。使用 `--full-id/-u` 显示完整 UUID，自动化应使用 `--json/-j`。

默认显示的短 ID 可以直接用于 `--id/-i`。位置值是精确名称；`--name/-n` 是对应的显式形式：

```bash
docker compose exec -T openvpn \
  ovpn client export -i 844854e4 -o - > laptop.ovpn
docker compose exec -T openvpn \
  ovpn client export -n laptop -o - > laptop.ovpn
```

ID 前缀至少包含八位十六进制字符，并且只能匹配一个客户端。位置值绝不会被推断为 ID。

### 客户端改名

```bash
docker compose exec openvpn ovpn client rename laptop office-laptop
```

改名保留不可变 UUID、证书身份、地址分配和审计历史。只有用户需要新文件名或新的内嵌显示名称时才需要重新分发 profile。

### 吊销和释放地址

```bash
# 吊销并保留静态地址
docker compose exec openvpn ovpn client revoke office-laptop

# 吊销并立即释放静态地址
docker compose exec openvpn ovpn client revoke office-laptop --release-ipv4

# 之后再释放保留的地址
docker compose exec openvpn ovpn client address release office-laptop
```

吊销会重建 CRL，并在提交后尝试断开在线 session。释放地址不会删除客户端或证书历史。

### 重签凭据

```bash
# 保留当前地址意图
docker compose exec -T openvpn \
  ovpn client reissue laptop -o - > laptop.ovpn

# 重签并分配最低可用静态地址
docker compose exec -T openvpn \
  ovpn client reissue laptop -4 -o - > laptop.ovpn

# 重签并改为动态地址
docker compose exec -T openvpn \
  ovpn client reissue laptop -4 dynamic -o - > laptop.ovpn

chmod 600 laptop.ovpn
```

重签保留 UUID，但会替换证书与私钥。旧 session 会在提交后断开；必须重新分发替换后的 profile。

### 删除客户端

```bash
docker compose exec openvpn ovpn client delete office-laptop --yes
```

删除会移除本地凭据与地址状态，但保留 UUID tombstone。删除后的私钥只能从安全备份恢复。

revoke、reissue、delete 和地址变更会在持久化提交后尝试断开受影响 session。若 broker 不可用，mutation 仍已提交并报告 pending warning；runtime 恢复健康后用 `ovpn runtime disconnect NAME` 重试。

---

## 地址管理

### 单客户端操作

```bash
docker compose exec openvpn \
  ovpn client address set -n laptop -4 dynamic
docker compose exec openvpn \
  ovpn client address set phone -4
docker compose exec openvpn \
  ovpn client address set -n tablet -4 10.42.0.30
```

`-4` 后不带值表示 `auto`，即分配最低可用静态地址。

### 批量操作

批量变化始终会打开编辑器；`--yes` 只跳过打开编辑器前的确认提示：

```bash
docker compose exec openvpn \
  ovpn client address edit -n laptop -n phone -e vim -y
```

文件中每个选中 active 客户端占一行 `client,ipv4`，值使用 `auto`、`dynamic` 或静态地址。完整文件统一验证并原子提交。可用 `--editor/-e` 指定本次编辑器，`OVPN_EDITOR` 设置该命令的默认编辑器，`EDITOR` 设置通用默认编辑器。选择顺序为 `--editor/-e`、`OVPN_EDITOR`、`EDITOR`、镜像内已安装的 `nano`。镜像内置 `nano`、`vim` 和 `vi`；其他编辑器需要先安装或挂载到容器内。编辑器值只能是单个可执行文件名或路径，不能附带参数。

### 释放 revoked 客户端地址

释放 revoked 客户端保留的静态地址：

```bash
docker compose exec openvpn \
  ovpn client address release -n retired-device
```

## 声明式配置变更

### 验证并应用变更

修改 YAML 不会自动改变 applied 状态。常规在线容器流程为：

```bash
docker compose exec openvpn ovpn config plan
docker compose exec openvpn ovpn config apply --yes
```

`config plan` 和 `config apply` 都会验证 YAML。apply 随后执行状态预检，结果不是 `HEALTHY` 时拒绝；应先查看 `state doctor` 并修复实例。`--force/-f` 只用于确认过的预检误判，不会绕过其他安全检查。apply 期间 supervisor 会暂时停止 OpenVPN 和 management broker、释放共享 runtime lock，在独占锁下执行 staging 事务，然后重新读取已提交的 revision 并重启受管进程；容器保持运行，但现有 VPN session 会因 OpenVPN 的受控重启而断开。

当在线 runtime 异常，或管理员明确希望停服操作时，仍可使用离线 maintenance 流程：

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance config validate
docker compose run --rm openvpn-maintenance config plan
docker compose run --rm openvpn-maintenance config apply -y
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

maintenance 命令不会启动或重启 OpenVPN，之后由 `docker compose up -d openvpn` 完成激活。apply 前必须阅读 plan。endpoint/transport 变化要求重分发 profile；网段/动态池变化可能重映射静态地址并重建 CCD/server config；NAT、route 和 redirect-gateway 变化会在受控重启期间 reconcile。

YAML 发生漂移但未 apply 时，重启仍使用旧 applied revision 并输出警告，避免意外文件编辑直接改变运行服务。

### 导出 applied 配置

从 applied 状态恢复完整 YAML：

```bash
umask 077
docker compose run --rm -T openvpn-maintenance \
  config export -o - > openvpn-config/config.yaml.new &&
  mv openvpn-config/config.yaml.new openvpn-config/config.yaml
```

## 状态诊断与修复

### 查看实例状态

只读诊断可以在线运行；考虑 repair/restore 时，离线结果更稳定：

```bash
docker compose exec openvpn ovpn state show
docker compose exec openvpn ovpn state doctor -j

docker compose stop openvpn
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
```

### 修复降级实例

只执行 plan 中允许的动作：

```bash
docker compose run --rm openvpn-maintenance repair apply -y
docker compose run --rm openvpn-maintenance state doctor
```

repair 只能根据互相一致的证据重建派生文件或恢复 artifact，不会猜测重建缺失/损坏的 SQLite 权威库。数据库处于 `CRITICAL`/`UNRECOVERABLE` 时必须恢复可信备份。

## Runtime 检查

### 状态与 session 控制

```bash
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime status -j
docker compose exec openvpn ovpn runtime disconnect laptop
docker compose exec openvpn ovpn runtime disconnect -i 844854e4 -j
docker compose exec openvpn ovpn runtime capabilities -j
```

### 日志与事件

```bash
docker compose exec openvpn ovpn runtime logs -l 200
docker compose exec openvpn ovpn runtime logs -l 0 -f
docker compose exec openvpn ovpn runtime logs -r -u
docker compose exec openvpn ovpn runtime events -l 200 -j
docker compose exec openvpn ovpn runtime events -l 0 -f
```

日志按 applied 配置持久化轮转；events 是面向用户的 JSONL。SQLite `audit_events` 是权威业务审计，不能替代 runtime 日志。

## Shell completion

从与 `ovpn help` 相同的命令契约生成脚本：

这些脚本补全的是名为 `ovpn` 的直接命令。可在服务容器的交互 shell 中运行，或在宿主机定义同名 wrapper，内部调用 `docker compose exec openvpn ovpn`。通过 Compose 生成时，把下面的 `ovpn completion` 换成 `docker compose exec -T openvpn ovpn completion`。

```bash
mkdir -p ~/.local/share/bash-completion/completions ~/.zfunc \
  ~/.config/fish/completions
ovpn completion bash > ~/.local/share/bash-completion/completions/ovpn
ovpn completion zsh > ~/.zfunc/_ovpn
ovpn completion fish > ~/.config/fish/completions/ovpn.fish
```

安装后启动新 shell。只有在显式 selector 参数后补全 name/ID 时，脚本才通过同一命令/wrapper 执行只读 client list 查询。

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

迁移保留 schema 3 的 UUID 证书身份，导入当前 client/address/audit/artifact 状态。成功后删除 live legacy 结构化文件；原件只保留在迁移快照中。

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

验证完成前保留 `openvpn-data-schema4`。恢复后的 schema 3 数据树与 `sh-ver` 镜像必须作为匹配单元使用。

## 离线备份与恢复

SQLite 和文件 artifact 始终必须一起备份和恢复：

### 备份

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-$(date +%Y%m%d%H%M%S).tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

归档包含 CA、服务端/客户端私钥、tls-crypt、profile 和数据库，必须加密保存。

### 恢复

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

不要把备份合并进现有目录，也不要只恢复 `state.db`。恢复实例通过诊断并完成至少一次客户端连接前，保留原目录。

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

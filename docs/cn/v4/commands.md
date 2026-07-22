# OpenVPN CLI v4 命令参考

本文定义 Go + SQLite schema 4 控制面的命令契约。

## 调用方式与路径

在线命令通过服务容器执行：

```bash
docker compose exec openvpn ovpn <command>
```

离线维护命令通过 one-shot maintenance 服务执行：

```bash
docker compose run --rm openvpn-maintenance <command>
```

maintenance 服务的 entrypoint 已经是 `ovpn`，因此 `<command>` 直接从 `config`、`state`、`repair` 或 `migrate` 开始。

| 用途 | 默认路径 | 覆盖变量 |
|---|---|---|
| 期望 YAML | `/etc/openvpn-config/config.yaml` | `OVPN_CONFIG_FILE` |
| 持久化数据 | `/etc/openvpn` | `OVPN_DATA_DIR` |
| 协调锁 | `/etc/openvpn` | `OVPN_DATA_DIR` |
| runtime socket | `/run/openvpn-container` | `OVPN_RUNTIME_DIR` |
| SQLite 权威库 | `/etc/openvpn/meta/state.db` | 由 data dir 派生 |

`OVPN_MAINTENANCE=true` 授权离线迁移。批量地址编辑器依次取 `OVPN_EDITOR`、`EDITOR`，最后使用 `nano`。外部二进制与模板覆盖变量只用于测试和开发，不属于常规部署配置。

`ovpn client`、`ovpn state`、`ovpn runtime` 是 `client list`、`state doctor`、`runtime status` 的安全快捷方式。顶层 `-v` 只输出项目版本，`-V` 与 `--version` 输出完整版本报告。裸执行 `ovpn` 会显示包含全部 leaf usage 的精简命令树；`ovpn -h` 仍显示详细根帮助。

## 一次性环境变量初始化

全新空实例可以根据 Compose 环境变量生成第一份 YAML。这只是初始化输入，不是第二套长期配置模式。设置 `OVPN_BOOTSTRAP_FROM_ENV=true`，并提供两个必填值：

| 环境变量 | YAML 字段 | 必填 |
|---|---|---|
| `OVPN_BOOTSTRAP_ENDPOINT` | `server.endpoint` | 是 |
| `OVPN_BOOTSTRAP_IPV4_NETWORK` | `ipv4.network` | 是 |
| `OVPN_BOOTSTRAP_PROTOCOL` | `server.transport.protocol` | 否 |
| `OVPN_BOOTSTRAP_FAMILY` | `server.transport.family` | 否 |
| `OVPN_BOOTSTRAP_PORT` | `server.transport.port` | 否 |
| `OVPN_BOOTSTRAP_CLIENT_TO_CLIENT` | `server.clientToClient` | 否 |
| `OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE` | `ipv4.dynamicPoolSize` | 否 |
| `OVPN_BOOTSTRAP_NAT_ENABLED` | `ipv4.nat.enabled` | 否 |
| `OVPN_BOOTSTRAP_NAT_INTERFACE` | `ipv4.nat.interface` | 否 |
| `OVPN_BOOTSTRAP_REDIRECT_GATEWAY` | `ipv4.redirectGateway` | 否 |
| `OVPN_BOOTSTRAP_DNS` | `ipv4.dns`，逗号分隔 | 否 |
| `OVPN_BOOTSTRAP_ROUTES` | `ipv4.routes`，逗号分隔 | 否 |
| `OVPN_BOOTSTRAP_LOG_MAX_BYTES` | `logging.maxBytes` | 否 |
| `OVPN_BOOTSTRAP_LOG_BACKUPS` | `logging.backups` | 否 |

程序应用普通 YAML 的默认值和严格验证，然后在 `OVPN_CONFIG_FILE` 原子安装 mode `0600` 的规范 YAML。已有 YAML 只有在规范化后与环境配置完全一致时才接受，以便安全重试失败的初始化；内容冲突会被拒绝。

schema 4 创建后，bootstrap 变量只会被忽略并输出 warning。之后所有修改必须通过 YAML 的 `config validate`、`config plan` 和离线 `config apply`。首次初始化成功后应删除 bootstrap 开关或将其设为 `false`。

## 输出与退出码

查询和 plan 默认输出稳定的人类可读文本，并在标注处支持 `--json`。JSON 模式错误以结构化对象写入 stderr。`runtime events --json` 输出 JSONL；`runtime logs --raw` 保留 OpenVPN 原始文本。

人类模式客户端 UUID 默认显示 12 位十六进制字符；`--full-id/-u` 显示规范完整 UUID。JSON 始终使用完整 UUID。所有客户端 mutation JSON 都带版本、operation ID、客户端状态、profile 重分发影响和提交后的 runtime 收敛状态。

| 退出码 | 含义 |
|---|---|
| `0` | 成功 |
| `1` | runtime 或操作失败 |
| `64` | CLI 用法错误 |
| `65` | 输入或配置数据错误 |
| `69` | 外部依赖不可用 |
| `75` | 锁、busy 状态或临时资源冲突 |
| `78` | schema、状态、确认或安全策略拒绝 |

`migrate apply`、`config apply`、`repair apply`、`client delete` 和批量地址编辑必须交互确认或带 `--yes`；非 TTY 未带 `--yes` 时直接拒绝。

mutation 会先执行只读验证、目标选择和 plan，再请求确认。无效请求不会弹出确认；no-op plan 不需要 `--yes`，直接成功退出。人类模式错误在安全时给出下一步 hint，JSON 错误保持稳定对象格式。

每个公开多字母参数都有单 token 短参数。长短形式等价；同时指定会被视为重复并拒绝。不支持短参数聚合或将值直接粘在短参数后。`-6` 为未来 IPv6 行为保留。

| 长参数 | 短参数 |
|---|---|
| `--help` | `-h` |
| `--json` | `-j` |
| `--output` | `-o` |
| `--yes` | `-y` |
| `--name` | `-n` |
| `--id` | `-i` |
| `--ipv4` | `-4` |
| `--release-ipv4` | `client revoke` 中使用 `-4` |
| `--all` | `-a` |
| `--detail` | `-d` |
| `--full-id` | `-u` |
| `--lines` | `-l` |
| `--follow` | `-f` |
| `--raw` | `-r` |
| `--short` | `-s` |

## 命令树

```text
ovpn
├── server
│   ├── init
│   ├── run
│   └── render [--output|-o FILE|-]
├── config
│   ├── validate [--json|-j]
│   ├── show [--json|-j]
│   ├── export [--output|-o FILE|-]
│   ├── plan [--json|-j]
│   └── apply [--yes|-y] [--json|-j]
├── client
│   ├── create NAME [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]
│   ├── list [--detail|-d] [--full-id|-u] [--json|-j]
│   ├── export (NAME|--name|-n NAME|--id|-i ID) [--output|-o FILE|-]
│   ├── rename (NAME|--name|-n NAME|--id|-i ID) NEW_NAME [--full-id|-u] [--json|-j]
│   ├── revoke (NAME|--name|-n NAME|--id|-i ID) [--release-ipv4|-4] [--full-id|-u] [--json|-j]
│   ├── reissue (NAME|--name|-n NAME|--id|-i ID) [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]
│   ├── delete (NAME|--name|-n NAME|--id|-i ID) [--yes|-y] [--full-id|-u] [--json|-j]
│   └── address
│       ├── set (NAME|--name|-n NAME|--id|-i ID) --ipv4|-4 [auto|dynamic|ADDRESS] [--full-id|-u] [--json|-j]
│       ├── edit (--all|-a|NAME...|--name|-n NAME...|--id|-i ID...) [--yes|-y] [--json|-j]
│       └── release (NAME|--name|-n NAME|--id|-i ID) [--full-id|-u] [--json|-j]
├── state
│   ├── show [--json|-j]
│   └── doctor [--json|-j]
├── repair
│   ├── plan [--json|-j]
│   └── apply [--yes|-y] [--json|-j]
├── migrate
│   ├── plan [--json|-j]
│   └── apply [--yes|-y] [--json|-j]
├── runtime
│   ├── status [--json|-j] [--full-id|-u]
│   ├── disconnect (NAME|--name|-n NAME|--id|-i ID) [--json|-j] [--full-id|-u]
│   ├── health
│   ├── capabilities [--json|-j]
│   ├── logs [--lines|-l N] [--follow|-f] [--raw|-r] [--full-id|-u]
│   └── events [--lines|-l N] [--follow|-f] [--json|-j] [--full-id|-u]
├── completion (bash|zsh|fish)
└── version [--short|-s|--json|-j]
```

所有命令组和 leaf command 都接受 `--help` 或 `-h`。

内部独立二进制 `ovpn-broker` 使用自己的别名空间：`--help/-h`、`--version/-v`、`--listen/-l`、`--backend/-b`、`--raw-log/-r`、`--max-bytes/-m`、`--backups/-B` 和 `--timeout/-t`。

## Server 命令

### `ovpn server init`

从有效 YAML 初始化空的 schema 4 数据目录。在 staging 中创建并验证 SQLite、PKI、服务端凭据、CRL、tls-crypt、配置和派生文件后统一安装。非空目录或不支持的旧格式会被拒绝。

镜像 entrypoint 只在挂载数据目录为空时自动调用初始化；显式 `server run` 不会为缺失状态执行初始化。

### `ovpn server run`

加载 applied SQLite 快照，恢复中断 operation，reconcile IPv4 forwarding/防火墙，启动 Go broker 与 OpenVPN，监督两个进程并转发 TERM、INT、HUP。

YAML 缺失或发生漂移只会产生警告；runtime 继续使用最近 applied revision，启动时绝不会自动 apply。

### `ovpn server render [--output FILE|-]`

根据 applied SQLite 状态渲染服务端配置。默认写 stdout；`--output -` 显式表示 stdout，文件目标必须满足安全输出规则。

## 配置命令

YAML v1 要求 `server.endpoint` 和 `ipv4.network`。解析严格拒绝未知/重复字段、null、多文档、类型错误、非规范网段、不支持值和 IPv6 隧道状态。

### `ovpn config validate [--json]`

只验证并规范化期望 YAML，不打开 SQLite，也不比较或修改 applied 状态。

### `ovpn config show [--json]`

显示 SQLite 中规范化的 applied 配置、revision 和 SHA-256 digest，不读取期望 YAML。

### `ovpn config export [--output FILE|-]`

从 applied SQLite 快照输出完整 YAML v1。schema 3 迁移实例若没有 v4 YAML，迁移后必须执行该命令。文件输出以 `0600` 新建且绝不覆盖已有文件；替换现有 YAML 时应输出到 stdout，经宿主临时文件再改名。

### `ovpn config plan [--json]`

比较期望 YAML 与 applied revision，列出字段变化以及重启、地址重映射、防火墙 reconcile、派生 artifact 和 profile 重分发影响。

### `ovpn config apply [--yes] [--json]`

要求 OpenVPN 已停止并获取独占 runtime lock。普通配置与 IPv4 网段/动态池变化在同一 staging operation 中完成，统一更新 SQLite、重映射地址、生成派生文件并报告重启和重分发要求；不会在线 reload。

## 客户端选择与身份

每个客户端拥有不可变 UUID 和当前显示名称。操作已有客户端时必须且只能使用一种选择形式：

```text
NAME
--name NAME
--id ID
```

未提供 `--name` 或 `--id` 时，位置参数默认按名称处理。名称区分大小写并精确匹配。ID 接受标准 UUID，或至少八位且唯一的十六进制 UUID 前缀。active/revoked 名称唯一；deleted 保留 UUID tombstone，但旧名称可以由新 UUID 复用。

IPv4 意图统一表示为：

- `auto`：分配最低可用静态地址。
- `dynamic`：使用配置的动态池。
- `ADDRESS`：使用指定且空闲的静态 IPv4。

出现 `--ipv4` 但未提供值时等同于 `--ipv4 auto`。完全省略该选项时，保留各命令文档中说明的原有默认行为。

### `ovpn client create NAME [--ipv4 ...] [--output FILE|-] [--json]`

创建 UUID、Easy-RSA 证书/私钥、profile、地址 assignment、artifact metadata、operation 和 audit event。默认 IPv4 意图为 `auto`。`--output` 可在同一命令中导出已提交的 profile；文件以 `0600` 创建且不覆盖已有文件。`--output -` 只向 stdout 写 profile，不能与 JSON 同时使用。

### `ovpn client list [--detail] [--full-id] [--json]`

列出当前客户端。`--detail` 增加 assignment 与 lease；文本模式默认缩短 ID，`--full-id` 显示完整 UUID；JSON 使用稳定对象。

### `ovpn client export SELECTOR [--output FILE|-]`

导出 active 客户端的当前 profile，默认写 stdout。输出属于私密凭据。

### `ovpn client rename SELECTOR NEW_NAME [--json]`

修改显示名称和 profile 文件名，不改变 UUID、证书身份、地址 assignment 或审计历史。

### `ovpn client revoke SELECTOR [--release-ipv4] [--json]`

吊销证书、重建 CRL，并将 profile 标记为 revoked。静态 IPv4 默认保留，带 `--release-ipv4` 时释放。持久化提交后命令会尝试断开现有 session；runtime 失败只报告 warning/pending，不会回滚已完成的吊销。

### `ovpn client reissue SELECTOR [--ipv4 ...] [--output FILE|-] [--json]`

为同一 UUID 签发新私钥/证书，更新 CRL/profile，并可改变地址意图。省略 `--ipv4` 时保留当前 assignment 意图；只写该参数时改为 `auto`。提交后会断开旧 session，`--output` 可直接返回替换 profile；新 profile 必须重新分发。

### `ovpn client delete SELECTOR [--yes]`

必要时先吊销 active 证书，再删除本地凭据和 assignment，保留 UUID tombstone。active 或之前已 revoked 的客户端都会尝试清理残留 session。已删除私钥只能从安全备份恢复。

### `ovpn client address set SELECTOR --ipv4 ...`

原子修改一个 active 客户端的 assignment 并同步 CCD。`--ipv4` 不带值时使用 `auto`。提交后命令会断开当前 session，使新地址生效。

### `ovpn client address edit TARGETS [--yes]`

选择全部 active 客户端或重复指定 name/ID，然后打开私有 CSV：

```text
# client,ipv4
laptop,auto
phone,dynamic
tablet,10.42.0.20
```

每个选中客户端必须恰好出现一次，值只能是 `auto`、`dynamic` 或静态 IPv4。位置名称、`--name`、`--id` 与 `--all` 不能混用。完整集合统一验证并原子提交，因此支持地址交换。编辑器依次取 `OVPN_EDITOR`、`EDITOR`、已安装的 `nano`；提交后会断开选中的在线 session。

### `ovpn client address release SELECTOR`

释放 revoked 客户端保留的静态 assignment，不删除客户端、profile 历史、证书证据或 tombstone。

## 状态与修复

### `ovpn state show [--json]`

输出聚合状态：`HEALTHY`、`DEGRADED_REPAIRABLE`、`DEGRADED_RECOVERABLE`、`DEGRADED_REISSUABLE`、`CRITICAL` 或 `UNRECOVERABLE`。初始化流程会单独识别空目录。

### `ovpn state doctor [--json]`

检查 SQLite integrity/约束、applied 配置、PKI、artifact metadata、证书/私钥、CRL、profile、CCD 和中断 operation，输出 issue ID、证据、严重性和建议动作。缺失或损坏的权威数据库不会被猜测重建。

### `ovpn repair plan [--json]`

根据 state report 生成只读计划，将安全派生文件重建、可信证据恢复、blocker 和 deferred work 分开列出。

### `ovpn repair apply [--yes] [--json]`

在独占锁下通过 staging、operation journal、验证和回滚执行允许的动作。SQLite 权威缺失/损坏或安全证据冲突仍要求恢复备份。

## 迁移命令

### `ovpn migrate plan [--json]`

只读解析 schema 3，报告客户端、assignment、lease、audit event、artifact、规范化 repair、profile 影响、快照路径、YAML export 和回滚说明。schema 1/2 会要求先用 `sh-ver` 升到 schema 3；更新、未知或损坏来源也会拒绝。

### `ovpn migrate apply [--yes] [--json]`

要求 `OVPN_MAINTENANCE=true`、服务停止、显式确认和独占锁。命令先创建并校验完整 schema 3 快照，再在 staging 中构建 schema 4，通过 `state doctor` 后原子安装；中断 transaction 可确定完成或回滚。

成功后必须 export YAML。回滚必须恢复完整且校验通过的快照，再运行 `sh-ver` 镜像。

## Runtime 命令

### `ovpn runtime status [--json]`

通过 management broker 查询 daemon、management、在线客户端、虚拟地址和远端地址，要求服务正在运行。

### `ovpn runtime disconnect SELECTOR [--json] [--full-id]`

通过 management broker 按不可变 client UUID 断开当前 session。客户端已离线时是成功 no-op；此前 runtime 不可用时，也可按 ID 选择 deleted tombstone 重试清理。该命令不会吊销凭据，也不会阻止客户端重新连接。

### `ovpn runtime health`

仅在 broker 与 OpenVPN 健康时返回成功并输出 `healthy`；镜像 healthcheck 使用该命令。

### `ovpn runtime capabilities [--json]`

报告严格 compatibility contract 和实际探测到的 OpenVPN feature。

### `ovpn runtime logs [options]`

读取持久化 OpenVPN 日志。`--lines` 默认 100，`--follow` 跟随轮转，`--raw` 禁止 UUID 到名称翻译，`--full-id` 在翻译输出中保留完整 UUID。

### `ovpn runtime events [options]`

读取 `events.jsonl`。文本模式面向人，`--json` 保持 JSONL，`--follow` 跟随新增事件，`--full-id` 保留完整 UUID。

## Completion 命令

### `ovpn completion (bash|zsh|fish)`

向 stdout 输出 shell completion 脚本。命令和参数来自与 help 相同的静态命令树；只有显式补全 `--name/-n` 或 `--id/-i` 的值时才读取当前 client list，不读取私密 artifact。安装示例：

脚本补全的是名为 `ovpn` 的直接命令。可在容器 shell 中使用，或在宿主机提供同名 wrapper，内部调用 `docker compose exec openvpn ovpn`。通过 Compose 生成时，把下面的 `ovpn completion` 换成 `docker compose exec -T openvpn ovpn completion`；动态 selector 也会调用同一个直接命令或 wrapper。

```bash
mkdir -p ~/.local/share/bash-completion/completions ~/.zfunc \
  ~/.config/fish/completions
ovpn completion bash > ~/.local/share/bash-completion/completions/ovpn
ovpn completion zsh > ~/.zfunc/_ovpn
ovpn completion fish > ~/.config/fish/completions/ovpn.fish
```

## 版本命令

### `ovpn version [--short|--json]`

输出项目 runtime/image 版本、data schema、Go 版本、VCS revision、构建时间和固定 Go module 版本。`--short` 只输出项目版本，`--json` 输出稳定版本对象。

# OpenVPN CLI v3 参考手册

本文是本源码树当前 CLI 的完整命令参考。

## 范围与约定

- 通过镜像入口点以 `ovpn <command>` 形式调用命令。在 Docker Compose 中，通常为 `docker compose exec openvpn ovpn <command>`。
- 持久化实例数据默认位于 `/etc/openvpn`；`OVPN_DATA_DIR` 可修改该位置。运行时状态文件默认位于 `/run/openvpn-container/state.json`。
- 客户端名称必须匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`。
- 标记为只读的命令不会修改持久化数据。修改数据的命令使用共享数据锁，并执行事务或要求显式确认。
- 配置、客户端和网络操作均作用于持久化实例。请针对持有已挂载数据目录的服务运行这些命令。

## 命令树

每个命令和子命令都支持 `--help` / `-h`。命令树如下（叶子节点的解释等价于其 `--help` 输出）：

```
ovpn
├── init                初始化空的 OpenVPN 数据目录。
├── start               扫描状态并启动 OpenVPN。
├── config
│   ├── show            打印持久化的项目配置。
│   └── apply           验证环境变量并写入持久化项目配置。
├── client
│   ├── create          创建客户端证书、profile 和 IP 分配。
│   ├── export          将活跃客户端 profile 写入标准输出。
│   ├── list            列出客户端证书状态和可选的详细 IP 分配信息。
│   ├── revoke          吊销客户端证书，可选释放静态 IP。
│   ├── reissue         为已有客户端签发新证书，可选调整 IP 分配。
│   ├── rename          修改客户端显示名称而不改变 UUID。
│   ├── delete          删除客户端及其本地凭据。
│   └── ip
│       ├── release     释放已吊销客户端的保留静态 IP。
│       └── set         分配客户端 IP 地址。
├── network
│   ├── plan            预览隧道网络迁移计划。
│   └── apply           应用隧道网络迁移。
├── repair
│   ├── plan            检查符合条件的修复操作。
│   └── apply           应用符合条件的修复操作。
├── state
│   ├── show            打印检测到的实例状态。
│   └── doctor          打印检测到的问题和建议操作。
├── render
│   ├── server          渲染服务端配置。
│   └── client          渲染客户端 profile。
├── runtime
│   ├── status          打印运行时状态 JSON。
│   ├── health          仅当容器健康时返回成功。
│   ├── capabilities    打印兼容性和特性信息。
│   ├── version         打印镜像和运行时构建信息。
│   ├── logs            读取已翻译或原始的持久化 OpenVPN 日志。
│   └── events          读取结构化运行时和生命周期事件。
├── migrate             规划或执行离线数据 schema 迁移。
└── help                打印此帮助。
```

## 帮助与生命周期

### `ovpn help`

语法：

```text
ovpn help
ovpn -h
ovpn --help
```

打印顶层命令树。此命令不会检查或修改实例数据。

### `ovpn -v` / `ovpn -V` / `ovpn --version`

语法：

```text
ovpn -v
ovpn -V
ovpn --version
```

`-v` 保留已发布的精简语义，仅打印镜像版本（如 `3.1.0`）。`-V` 与 `--version` 输出相同的四项面向运维者的版本信息：

```text
image:           3.0.0
openvpn:         2.7.5
easy-rsa:        3.2.2
data schema:     3
```

完整的构建信息 JSON 可通过 `ovpn runtime version` 获取。

### 公共参数短形式

每个公共多字母参数都有单字母形式。短形式只在所属子命令内解释，因此 `client list` 中的 `-d` 表示 `--detail`，分配命令中的 `-d` 表示 `--dynamic`；同理，客户端选择器中的 `-n` 表示 `--name`，network 命令中的 `-n` 表示 `--network`。小写 `-i` 继续表示客户端 ID，因此 IP 分配使用大写 `-I`。短参数必须分开传入（如 `-d -t`），接口不承诺支持 `-dt` 这类聚合写法。

| 长参数 | 短参数 |
|---|---|
| `--all` | `-a` |
| `--detail` | `-d` |
| `--dynamic` | `-d` |
| `--dynamic-pool-size` | `-p` |
| `--follow` | `-f` |
| `--help` | `-h` |
| `--id` | `-i` |
| `--ip` | `-I` |
| `--json` | `-j` |
| `--lines` | `-l` |
| `--name` | `-n` |
| `--network` | `-n` |
| `--no-trunc` | `-t` |
| `--output` | `-o` |
| `--raw` | `-r` |
| `--release-ip` | `-r` |
| `--stdout` | `-s` |
| `--version` | `-V` |
| `--yes` | `-y` |

### `ovpn init`

语法：

```text
ovpn init
```

在一个事务中初始化空的持久化数据目录。它会创建项目配置、PKI 与 CA、服务端身份、CRL、tls-crypt 密钥、元数据、服务端配置、client-IP 清单、生成状态目录以及修复目录。它拒绝初始化非空数据目录。

此命令用于显式供应数据。当数据目录为空时，`ovpn start` 会自动执行相同的初始化。

### `ovpn start`

语法：

```text
ovpn start
```

容器入口点操作。它会初始化空实例、扫描状态、对符合条件的自动修复进行操作、验证运行时兼容性、配置网络、渲染服务端配置、记录健康的运行时状态，并在前台启动 OpenVPN。

不安全的实例状态不会被启动。当设置 `OVPN_CRITICAL_MODE=maintenance` 时，关键或不可恢复状态将进入维护模式以供排障，而非立即终止容器。

## 持久化配置

### `ovpn config show`

语法：

```text
ovpn config show
```

只读。加载持久化配置，对所有配置项打印一行 `KEY=VALUE`：schema 版本、端点、传输协议、公网传输地址族、端口、网络、拓扑、动态池大小、NAT 设置、网关重定向、客户端互访设置、DNS 服务器以及推送路由。

### `ovpn config apply`

语法：

```text
ovpn config apply
```

验证当前 `OVPN_*` 环境变量，并以 mode `0600` 原子替换 `config/project.env` 和 `config/schema-version`。它会写入数据 schema 版本 `3`，且不会签发、撤销、删除或重新签发客户端证书。

`OVPN_ENDPOINT` 必须为有效的主机名或 IP 字符串；`OVPN_PROTO` 为 `udp` 或 `tcp`；`OVPN_TRANSPORT_FAMILY` 为 `auto`、`ipv4` 或 `ipv6`。持久化的 `auto` 值保持不变：渲染时会识别 IPv4 和 IPv6 字面量；域名则选择双栈服务端传输和地址族中立的客户端传输。`config apply` 不进行 DNS 解析。`OVPN_TOPOLOGY` 为 `subnet`；布尔类型字段为 `true` 或 `false`；`OVPN_DNS` 和 `OVPN_ROUTES` 为逗号分隔的 IPv4 值；网络与动态池大小必须构成合法的 IPAM 布局。`OVPN_LOG_MAX_BYTES` 必须为正整数，`OVPN_LOG_BACKUPS` 必须为非负整数。apply 会写入当前环境中所有的配置值。

## 客户端生命周期

每个客户端都有不可变的 UUID 身份。证书 CN、Easy-RSA 实体、CCD 文件名、动态租约文件名和 OpenVPN management 身份均使用该 UUID；客户端名称仍作为面向人的管理标签和 profile 文件名。生成的 profile 包含 `ovpn-client-id` 与 `ovpn-client-name` 注释，因此无需改变 OpenVPN 语法也能恢复两种身份。除 `create` 外，选择客户端的命令均接受 `<selector>`：位置参数 `<name>`、`--id <ID>`/`-i <ID>`，或者 `--name <NAME>`/`-n <NAME>`。位置参数和显式名称均进行区分大小写的精确名称匹配。ID 选择器可使用标准 UUID，或至少 8 位、不区分大小写的紧凑十六进制前缀，且必须唯一匹配 active/revoked 客户端。位置参数永远不会推断为 ID，UUID 形式也不能用作显示名称。

### `ovpn client create`

语法：

```text
ovpn client create <name> [--dynamic|-d|--ip|-I <IPv4>]
```

在一个事务中创建由唯一 UUID 标识的客户端证书、私钥、活跃 profile、清单记录以及 IP 分配。不带选项时，客户端获取最低的可用静态地址。`--dynamic` 创建动态分配，要求动态池容量非零。`--ip <IPv4>` 请求静态区内的特定未使用地址。`--dynamic` 与 `--ip` 不可同时使用。

### `ovpn client export`

语法：

```text
ovpn client export <selector>
```

需要一个健康的活跃客户端。原子重新生成 `clients/active/<name>.ovpn`，然后将同一 profile 写入标准输出。将标准输出重定向即可保存客户端 profile。

### `ovpn client list`

语法：

```text
ovpn client list [--detail|-d] [--no-trunc|-t]
```

不带 `--detail` 时，依次打印对齐的 `CLIENT ID`、`NAME` 和 `STATE` 列。ID 默认显示去掉连字符后 UUID 的前 12 位，可直接复制给 `--id`/`-i`；`--no-trunc` 显示完整标准 UUID。带 `--detail` 时，额外打印 `MODE`、`IP`、`IP STATE` 和 `CONNECTION`。

在 IP 视图下，静态分配为 `configured`，或撤销后为 `retained`。动态地址在有当前租约时显示为 `connected`，在有缓存租约记录时显示为 `last-known`，否则为 `unavailable`。`CONNECTION` 根据管理套接字可用性和当前路由显示为 `online`、`offline` 或 `unknown`。该视图读取权威 IP 清单，并结合当前连接与租约缓存生成状态。

### `ovpn client rename`

语法：

```text
ovpn client rename <selector> <new-name>
```

原子修改面向人的显示名称，同时保持 UUID、证书、私钥、IP 分配、CCD、租约以及当前 OpenVPN 连接不变。身份目录、IP 清单、profile 文件名及其中的名称注释会一同更新。源客户端可通过位置参数使用当前名称；按 UUID 选择时必须使用 `--id`/`-i`。新名称必须合法且未被当前客户端占用。客户端改名或删除后，旧名称可由新的 UUID 复用；已删除 UUID 的 tombstone 仍作为权威历史保留。

### `ovpn client revoke`

语法：

```text
ovpn client revoke <selector> [--release-ip|-r]
```

撤销活跃证书，重新生成 CRL，将其活跃 profile 移至 `clients/revoked/`，记录该客户端为已撤销状态，并在管理套接字可用时断开其连接。默认情况下，静态分配会保持保留。`--release-ip` 作为同一操作的一部分释放该静态保留。

### `ovpn client reissue`

语法：

```text
ovpn client reissue <selector> [--dynamic|-d|--ip|-I <IPv4>]
```

为已有的客户端名称签发新密钥和新证书。对于活跃客户端，它首先撤销旧证书并将其 profile 移至已撤销集合。在修改线上 PKI 之前，会探测所搭载的 Easy-RSA 运行时是否支持同一 CN 重新签发，因此校验失败的请求不会造成任何变更。

已有静态 IP 的客户端在重签时默认保留原有分配。无 IP 的客户端（曾释放或原本动态）在重签时默认分配最低可用静态地址；静态区无空余容量则拒绝。选项：

- `--dynamic` → 重签后使用动态分配，要求动态池容量非零。
- `--ip <IPv4>` → 重签后使用指定的静态地址，必须在静态区域内且未被占用。

### `ovpn client delete`

语法：

```text
ovpn client delete <selector>
```

不可逆地移除客户端。活跃客户端会先被撤销；然后命令移除其 IP 记录、活跃或已撤销 profile、私钥、已签发证书和请求文件，同时在身份目录中保留 UUID tombstone。已删除客户端的显示名称可供新的 UUID 复用。旧私钥仅能从安全备份中恢复。

## 客户端 IP 管理

权威清单为 `meta/client-ip.csv`。它必须以 `# id,name,ip` 为首行，后续使用 `id,name,ip` 行，其中 `id` 是客户端不可变的 UUID。非空 IP 为静态分配；空 IP 为动态分配。UUID、名称和静态地址必须唯一，静态地址必须落在静态区域内，且清单必须包含权威身份目录中的每一个活跃或已撤销客户端。动态地址的最后已知租约缓存在 `cache/client-leases/`，不属于配置的 IP 分配。

### `ovpn client ip release`

语法：

```text
ovpn client ip release <selector>
```

释放已撤销客户端的保留静态分配。客户端必须已撤销且仍持有静态保留。已撤销的 profile、私钥、证书历史和审计历史均会保留。

### `ovpn client ip set`

语法：

```text
ovpn client ip set <name...|(--id|-i <ID>)...|(--name|-n <NAME>)...|--all|-a> [--dynamic|-d|--ip|-I <IPv4>]
```

将活跃客户端设为指定 IP 分配并立即应用事务。

单个客户端模式：
- 不带标志 → 自动分配最低可用静态地址
- `--ip <IPv4>` → 显式指定静态地址
- `--dynamic` → 设为动态分配

多个位置名称、重复的 `--id`、重复的 `--name` 或 `--all` 会打开编辑器显示 `client,ip` 行。显式 ID、显式名称、位置名称和 `--all` 不能互相混用。编辑器支持三种赋值：

- 输入 `auto` 分配最低可用静态地址
- 输入显式 IPv4 指定静态地址
- IP 留空保留动态分配

```text
laptop,auto               # 自动分配
phone,10.88.0.20          # 显式指定
desktop,                  # 保留动态
```

编辑器选择顺序为 `OVPN_EDITOR`，其次 `EDITOR`，最后 `nano`（镜像预装 `nano` 和 `vim`）。

## 隧道网络迁移

### `ovpn network plan`

语法：

```text
ovpn network plan [--network|-n <CIDR>] [--dynamic-pool-size|-p <N>]
```

只读。根据请求的值构建并打印迁移计划，未指定的选项使用当前值。该计划在可能的情况下保留有效的静态分配，否则在合法时保留主机段，或分配最低可用静态地址。它不会连接管理套接字。

### `ovpn network apply`

语法：

```text
ovpn network apply [--network|-n <CIDR>] [--dynamic-pool-size|-p <N>] [--yes|-y]
```

构建并打印同一计划，然后在交互式终端上请求确认，除非传入了 `--yes`。它会快照配置、清单、CCD、租约、渲染后的服务端配置和审计状态；更新配置和清单；重载 OpenVPN；并检查管理套接字和容器健康状态。重载或健康检查失败时，恢复快照并重载旧配置。

请在实时 OpenVPN 服务上运行此命令，因为执行需要其本地管理套接字。迁移成功后，请同步更新 `docker-compose.yaml` 中的 `OVPN_NETWORK`（以及 `OVPN_DYNAMIC_POOL_SIZE`，如有变更）——持久化的 `project.env` 虽然持有新值，但 `ovpn config apply` 读取的是环境变量，旧值会导致网络被静默回退。

## 状态与修复

### `ovpn repair plan`

语法：

```text
ovpn repair plan [--json|-j]
```

只读。扫描实例并打印符合条件的自动操作与受阻问题。文本报告中以 `SAFE` 或 `RECOVER` 标记操作；`--json` 将状态、操作和受阻条目以 JSON 输出。关键和不可恢复状态在报告后以退出状态 `78` 退出。

符合条件的操作包括：恢复派生配置或元数据、重新生成 CRL、渲染缺失的活跃 profile、恢复已验证的证书或密钥副本、恢复当前客户端身份/IP 清单，以及创建运行时目录。

当 `meta/client-state.csv` 缺失或无效时，恢复以当前 PKI 中 UUID 形式的客户端条目为起点。显示名称只有在当前格式的 IP 清单、profile 身份注释及最后一条适用的 rename 审计记录相互一致时才会采用。证据冲突会进入 `CRITICAL`，需要使用备份或人工检查。若名称证据全部丢失，repair 会分配确定性的临时名称 `client-<去掉连字符的 UUID>`；修复后应将其改名。当前 runtime 不解析历史清单或历史审计格式。由于 PKI 只能记录证书处于 active 还是 revoked，权威身份清单丢失后无法区分 deleted tombstone 与 revoked 客户端；repair 会让该 UUID 保持 revoked，不会将其恢复为 active。

### `ovpn repair apply`

语法：

```text
ovpn repair apply
```

构建计划并在数据锁下执行符合条件的操作。它会暂存并验证结果，快照受影响的持久化文件，原子安装更改，写入修复日志，并在事务失败时恢复快照。不安全的状态会被拒绝。

### `ovpn state show`

语法：

```text
ovpn state show
```

只读。打印检测到的实例状态，包括 `EMPTY`、`HEALTHY`、可修复或可恢复的降级状态，以及关键或不可恢复状态。

### `ovpn state doctor`

语法：

```text
ovpn state doctor [--json|-j]
```

只读。打印检测到的状态及每个问题的严重程度与建议操作。`--json` 输出包含 `state` 和 `issues` 的对象。关键和不可恢复状态在输出后以退出状态 `78` 退出。

## 渲染

### `ovpn render server`

语法：

```text
ovpn render server [--stdout|-s|--output|-o <path>]
```

根据持久化配置、传输地址族、IPAM 布局、PKI 路径和兼容的模板族渲染服务端配置。`auto` 会从 IP 字面量推断 `ipv4` 或 `ipv6`。对于域名，服务端渲染 IPv6 双栈 socket（`udp6` 或 `tcp6-server`，不设置 `bind ipv6only`），客户端 profile 保持地址族中立的 `udp`/`tcp`，并在连接时解析 A/AAAA 记录。该 socket 同时接受原生 IPv6 和 IPv4-mapped 对端。显式 `ipv6` 会增加 `bind ipv6only`；显式 `ipv4` 使用的 IPv4 socket 本身无法接受 IPv6，因此不需要对应的 bind 选项。无输出选项时，原子更新 `server/server.conf`；`--stdout` 将结果写入标准输出；`--output <path>` 在指定路径写入 mode-`0600` 文件。

### `ovpn render client`

语法：

```text
ovpn render client <selector> [--stdout|-s|--output|-o <path>]
```

根据已配置的端点、CA 证书、指定客户端证书和私钥以及 tls-crypt 密钥构建客户端 `.ovpn` profile。输出默认为标准输出；`--output` 写入一个原子替换的 mode-`0600` 文件。

## 持久化数据迁移

### `ovpn migrate`

语法：

```text
ovpn migrate plan [--json|-j]
ovpn migrate apply [--yes|-y]
```

此命令只能通过已停止服务的 `openvpn-maintenance` 使用。`plan` 为只读操作，报告源/目标 schema、有序迁移链、客户端数量、阻塞项、凭据影响和 profile 重分发需求。`apply` 要求交互确认；非 TTY 必须传 `--yes`。已经是当前 schema 时幂等成功。

迁移始终使用当前 maintenance 镜像内置代码且不访问网络。apply 获取独占运行锁、创建持久化快照、只迁移 staging 副本，验证 schema、PKI、清单、profile 和配置后原子提交数据。失败或中断时恢复原数据。schema 1 执行 `1→2→3`，schema 2 执行 `2→3`；两者都会把名称 CN 凭据替换为 UUID CN 凭据，成功后报告的所有活跃 profile 都必须重新分发。

读取数据的命令对旧、冲突、非法或更新 schema 返回 `78`；只有 `migrate` 可以读取历史格式。不解析实例数据的 help、version 和 capabilities 仍可使用。快照和报告保存在 `repair/migrations` 下。数据迁移后若要运行旧镜像，必须恢复与其匹配的迁移前快照；仅回滚镜像不等于回滚数据。

## 运行时检查

### `ovpn runtime status`

语法：

```text
ovpn runtime status
```

打印运行时状态 JSON。当运行时状态文件不可用时，返回一个合成对象，其中检测到的实例状态作为 state，`daemon` 设为 `unknown`，`maintenance` 设为 `false`。

### `ovpn runtime health`

语法：

```text
ovpn runtime health
```

仅当运行时状态报告为健康、正在运行、非维护模式，且 `/dev/net/tun` 和 OpenVPN 进程同时存在时返回成功。失败时向标准错误输出原因并返回非零值。

### `ovpn runtime capabilities`

语法：

```text
ovpn runtime capabilities
```

打印 JSON，描述检测到的 OpenVPN 版本、精确版本支持判定、已验证版本列表、选定的兼容适配器以及必需的运行时特性。当版本、适配器或任何必需特性不受支持时，返回非零值。

### `ovpn runtime version`

语法：

```text
ovpn runtime version
```

打印 `/usr/local/share/openvpn-container/build-info.json` 中的构建信息 JSON，其中 Easy-RSA 版本为运行时检测值。输出包含镜像版本、数据 schema、OpenVPN、Easy-RSA、runtime/构建来源和自动化使用的 OpenVPN 候选范围。候选范围不是 runtime 兼容性声明。若构建信息缺失，则检测 Easy-RSA，其余不可用字段打印 `unknown`。

### `ovpn runtime logs`

语法：

```text
ovpn runtime logs [--lines|-l N] [--follow|-f] [--raw|-r] [--no-trunc|-t]
```

读取持久化轮转的 OpenVPN 日志，默认最近 100 行。已知 UUID 显示为 `名称 [短ID]`，使用与 `client list` 相同的 12 位 ID；未知身份保持完整且不变。`--no-trunc` 显示已知客户端的完整 UUID；`--raw` 同时禁用翻译和截断。`--follow` 可跨追加、轮转和原子替换持续跟随，且不会占用或阻塞 OpenVPN management socket。

### `ovpn runtime events`

语法：

```text
ovpn runtime events [--lines|-l N] [--follow|-f] [--json|-j] [--no-trunc|-t]
```

读取最近 100 条结构化连接、断开、客户端生命周期、IP、rename、网络迁移和数据迁移事件。默认文本使用 12 位客户端 ID；`--no-trunc` 显示完整 UUID。`--json` 每行输出磁盘中保存的完整 JSON 对象，不受 `--no-trunc` 影响。`--follow` 持续输出新记录且不阻塞 management 命令；启动时读取的历史记录或随后追加的记录若格式损坏，会在标准错误输出警告并跳过，使跟随继续运行。不使用 `--follow` 时，选中范围内的损坏记录仍是致命读取错误。

该命令读取面向用户可观察性的 `logs/events.jsonl`，不会读取 `meta/audit.jsonl`。后者是由数据 schema 管理的严格内部审计，记录 IP 应用、客户端生命周期变更、rename 和网络迁移等关键持久化修改。状态检查会校验其格式；repair 可将最后一条 rename 记录用作身份恢复证据；数据迁移会保留或转换它。请勿手动编辑、截断或删除 `meta/audit.jsonl`。

## 示例

```bash
# 创建并导出一个静态客户端 profile。
docker compose exec openvpn ovpn client create laptop
docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn

# 预览网络变更而不修改持久化数据。
docker compose exec openvpn ovpn network plan --network 10.43.0.0/24 --dynamic-pool-size 96

# 从 maintenance 服务诊断实例。
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
```

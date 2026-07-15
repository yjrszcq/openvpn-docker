# OpenVPN CLI v1 参考手册

本文是针对发布提交 `6619921e5257e604f5df2c63d2fa10505b680d84` 的命令参考，描述了该版本中实现的命令表面。

## 范围与约定

- 通过镜像入口点以 `ovpn <command>` 形式调用命令。在 Docker Compose 中，通常为 `docker compose exec openvpn ovpn <command>`。
- 持久化实例数据默认位于 `/etc/openvpn`；`OVPN_DATA_DIR` 可修改该位置。运行时状态文件默认位于 `/run/openvpn-container/state.json`。
- 客户端名称必须匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`。
- 修改持久化状态的命令使用数据锁。请勿在命令执行期间并发编辑 PKI 或已生成的文件。
- 除非所属章节另有说明，状态敏感操作需要一个健康的实例。`CRITICAL` 和 `UNRECOVERABLE` 诊断结果以状态码 `78` 退出。

## 命令树

```text
ovpn help | -h | --help
ovpn version | --version
ovpn init
ovpn start
ovpn config [print|init]
ovpn render <server|client> ...
ovpn add-client <name>
ovpn export-client <name>
ovpn list-clients
ovpn revoke-client <name>
ovpn state
ovpn doctor [--json]
ovpn repair
ovpn repair --plan [--json]
ovpn status
ovpn healthcheck
ovpn capabilities
ovpn recover
```

## 帮助与构建信息

### `ovpn help`

语法：

```text
ovpn help
ovpn -h
ovpn --help
```

打印顶层命令列表。此命令不会检查或修改实例数据。

### `ovpn version`

语法：

```text
ovpn version
ovpn --version
```

打印 `/usr/local/share/openvpn-container/build-info.json` 中的构建信息 JSON。如果该文件缺失，则对于 image、runtime、Easy-RSA 和 support-range 字段打印 `unknown` 值。

## 实例生命周期

### `ovpn init`

语法：

```text
ovpn init
```

在一个事务中初始化空的持久化数据目录。它会创建项目配置、PKI 与 CA、服务端身份、CRL、tls-crypt 密钥、元数据、服务端配置、客户端 profile 目录以及修复目录。它拒绝非空目录。

此命令用于显式供应数据。当数据目录为空时，`ovpn start` 会自动执行相同的初始化。

### `ovpn start`

语法：

```text
ovpn start
```

容器入口点操作。它会初始化空目录、扫描实例状态、对可修复或可恢复状态应用符合条件的自动修复、验证运行时兼容性、配置网络、渲染服务端配置、记录运行时状态，并在前台启动 OpenVPN。

它拒绝启动不安全的状态。当设置 `OVPN_CRITICAL_MODE=maintenance` 时，关键或不可恢复状态将进入维护模式，而非立即终止容器。

## 持久化配置

### `ovpn config`

语法：

```text
ovpn config
ovpn config print
ovpn config init
```

不带子命令时，`config` 默认为 `print`。

`print` 加载持久化项目配置，每个支持的设置打印一行 `KEY=VALUE`：配置版本、端点、传输协议、端口、隧道网络、NAT 设置、网关重定向、客户端互访、DNS 服务器以及推送路由。

`init` 验证当前 `OVPN_*` 环境变量，并以 mode `0600` 原子写入 `config/project.env` 和 `config/schema-version`。它不会签发、撤销或删除客户端证书。接受的配置键为：`OVPN_CONFIG_VERSION`、`OVPN_ENDPOINT`、`OVPN_PROTO`、`OVPN_PORT`、`OVPN_NETWORK`、`OVPN_NAT`、`OVPN_NAT_INTERFACE`、`OVPN_REDIRECT_GATEWAY`、`OVPN_CLIENT_TO_CLIENT`、`OVPN_DNS` 和 `OVPN_ROUTES`。

## 渲染

### `ovpn render server`

语法：

```text
ovpn render server [--stdout|--output <path>]
```

根据持久化配置、PKI 路径和兼容的 OpenVPN 模板族渲染服务端配置。无输出选项时，原子更新 `server/server.conf`；`--stdout` 将结果写入标准输出；`--output <path>` 在指定路径写入 mode-`0600` 文件。

### `ovpn render client`

语法：

```text
ovpn render client <name> [--stdout|--output <path>]
```

通过嵌入 CA 证书、指定客户端证书和私钥以及 tls-crypt 密钥来构建客户端 `.ovpn` profile。端点必须已配置，所有必需的 PKI 文件必须存在。输出默认为标准输出；`--output` 写入一个原子替换的 mode-`0600` 文件。

## 客户端证书与 profile

### `ovpn add-client`

语法：

```text
ovpn add-client <name>
```

需要一个健康的实例和一个唯一的有效名称。签发无密码客户端证书和私钥，将 profile 渲染到 `clients/active/<name>.ovpn`，并在标准错误中报告新客户端。

### `ovpn export-client`

语法：

```text
ovpn export-client <name>
```

需要一个健康的实例和一个活跃客户端。它会原子性地重新生成活跃 profile，然后将同一 profile 写入标准输出。将标准输出重定向即可将 profile 保存到本地。

### `ovpn list-clients`

语法：

```text
ovpn list-clients
```

需要一个健康的实例。根据 Easy-RSA 索引，每行打印一个 `name state` 记录。有效证书状态为 `active`，已撤销证书状态为 `revoked`；服务端身份不在列表中。

### `ovpn revoke-client`

语法：

```text
ovpn revoke-client <name>
```

需要一个健康的活跃客户端。撤销证书，重新生成 CRL，若存在则将其 profile 从 `clients/active/` 移至 `clients/revoked/`。此命令不会删除私钥或证书材料。

## 状态与修复

### `ovpn state`

语法：

```text
ovpn state
```

扫描持久化数据并打印一个状态名：`EMPTY`、`HEALTHY`、`DEGRADED_REPAIRABLE`、`DEGRADED_RECOVERABLE`、`DEGRADED_REISSUABLE`、`CRITICAL` 或 `UNRECOVERABLE`。此命令是只读的。

### `ovpn doctor`

语法：

```text
ovpn doctor [--json]
```

执行同样的状态扫描并列出检测到的问题及其严重程度和建议操作。不带 `--json` 时，输出为可读的状态与问题报告。`--json` 会输出一个包含 `state` 与 `issues` 数组的对象。关键和不可恢复状态在打印后以退出状态 `78` 退出。

### `ovpn repair`

语法：

```text
ovpn repair
ovpn repair --plan [--json]
```

不带参数时，repair 构建自动修复计划，并在状态为 `HEALTHY`、`DEGRADED_REPAIRABLE` 或 `DEGRADED_RECOVERABLE` 时在数据锁下执行。它会暂存更改、验证暂存后的实例、快照受影响的持久化文件、原子安装结果并写入修复日志。失败的事务会恢复其快照。

`--plan` 是只读的。它列出建议的 `SAFE` 和 `RECOVER` 操作、已阻止的问题以及当前状态；`--json` 将该报告转为 JSON。计划器可提议写入 schema-version、渲染元数据或配置、重新生成 CRL、渲染缺失的 profile、恢复已验证的证书或密钥副本，以及创建运行时目录。它不会执行已阻止或关键的恢复操作。

### `ovpn recover`

语法：

```text
ovpn recover
```

该命令名在此版本中可识别但尚未实现。它打印错误并以状态码 `2` 退出；不执行任何恢复操作。

## 运行时检查

### `ovpn status`

语法：

```text
ovpn status
```

打印运行时状态 JSON。当运行时状态文件不可用时，返回一个合成对象，其中检测到的实例状态作为 state，`daemon` 设为 `unknown`，`maintenance` 设为 `false`。

### `ovpn healthcheck`

语法：

```text
ovpn healthcheck
```

仅当运行时状态报告为健康、正在运行、非维护模式，且 `/dev/net/tun` 和 OpenVPN 进程同时存在时返回成功。失败时向标准错误输出原因并返回非零值。

### `ovpn capabilities`

语法：

```text
ovpn capabilities
```

打印 JSON，描述检测到的 OpenVPN 版本、是否在支持范围内、选定的兼容适配器以及每个必需的运行时特性。当版本、适配器或任何必需特性不受支持时，返回非零值。

## 示例

```bash
# 检查已有实例。
docker compose run --rm openvpn-maintenance doctor

# 创建客户端并保存其 profile。
docker compose exec openvpn ovpn add-client laptop
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn

# 查看自动修复建议而不修改持久化数据。
docker compose run --rm openvpn-maintenance repair --plan
```

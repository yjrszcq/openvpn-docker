# Schema 3 Shell 运行时契约

[English](README.md)

本目录冻结 Go 重写所依据的 Shell/Python 3.2.0 运行时可观察基线。它是实现输入，并不承诺经过有意重新设计的 v4 CLI 或存储布局与旧版本逐字节兼容。

`behavior.json` 记录命令、默认值、状态分类、持久化文件、渲染用例、迁移 fixture 以及已变更接口的处置方式。`test-inventory.json` 对当时已有的全部测试进行分类，以便运行时切换时保留外部行为证据，并有计划地替换与实现细节耦合的断言。

## 兼容性规则

每项 v3 行为采用以下一种处置方式：

- `preserve`：在 v4 中保留用户可见的语义行为。
- `redesign`：保留使用场景，但验证已记录的 v4 接口，并说明输出或存储差异。
- `fold`：将该行为合并到另一个 v4 工作流中。
- `retire`：有意移除仅属于 v3 实现的接口。

在对应的 Go 契约建立前，v3 命令手册和当前 smoke 测试仍是详细事实来源。Phase 2 的渲染/IPAM 测试必须与本目录记录的用例比较；Phase 9 的迁移测试必须使用真实 schema 3 镜像或 fixture，不能根据 schema 4 的假设反向构造 schema 3。

## 已批准的 v4 非兼容变更

- 环境变量驱动的持久配置改为严格声明式 YAML。
- `init`、`start` 和服务端配置渲染移至 `ovpn server`。
- 在线 `network plan/apply` 合并到离线 `config plan/apply`。
- 已有客户端可显式通过 `--name` 或 `--id` 选择；两者均未提供时，位置选择器默认按名称处理。
- `--dynamic`/`--ip` 改为 `--ipv4 auto|dynamic|ADDRESS`。
- `client ip` 改为 `client address`。
- `runtime version` 移至顶层 `version`。
- schema 3 的 CSV/JSONL 权威状态改为 SQLite schema 4；PKI 和 artifact 仍为文件。
- 一般无效输入以及依赖或锁失败不再统一返回 v3 的状态码 1，而是使用稳定的 sysexits 风格退出码。

实现过程中发现的其他偏差，必须在提交依赖该偏差的阶段前记录到 checkpoint 路线图中。

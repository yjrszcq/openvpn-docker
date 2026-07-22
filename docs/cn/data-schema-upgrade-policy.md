# 数据 Schema 升级政策

本文定义独立于命令手册和镜像发布的持久化兼容规则。

## 独立版本轴

项目镜像版本、OpenVPN runtime 版本和整数 data schema 相互独立。只有持久化解释发生不兼容变化时才递增 schema。多个项目/OpenVPN 版本可以共用同一 schema，并且必须以完全相同的方式解释该 schema。

迁移调度只依据数据目录中的证据，不能按镜像 tag、源码 revision、发布日期或 OpenVPN 版本猜测格式。

## 当前 schema 4 权威边界

schema 4 将全部结构化权威状态保存在 `/etc/openvpn/meta/state.db`。PKI、私钥、CRL、tls-crypt、profile、CCD、派生 server config 和日志仍为文件。数据库与这些文件必须作为同一个备份/恢复单元。

runtime 不得维持 CSV/SQLite 双权威。runtime lease 明确属于可丢失 cache；业务状态与 audit 必须在同一 SQLite transaction 中提交。

## 严格 runtime gate

普通 runtime 只接受当前 schema。历史格式只能由显式 migration package 读取；config、client、state、repair、recovery、hook 和 server startup 不得解析旧 registry。

旧、更新、未知、冲突或损坏的 schema 证据以退出码 `78` 拒绝。help、version、capabilities 和不解释当前业务状态的 migration plan 可以继续使用。

禁止启动时迁移、repair 顺带迁移和自动改写格式。

## 支持的迁移入口

v4 镜像只直接支持 schema 3 → 4。schema 1/2 必须先使用稳定 `sh-ver` 镜像升级到 schema 3。这样可以限制 Go runtime 的历史解析面，把更旧的迁移逻辑留在创建这些格式的维护分支中。

未来 schema 变化必须明确支持的入口政策。优先提供直接 `N` → `N+1`，但如果保留全部历史 parser 会扩大可信 runtime 或削弱验证，项目可以要求先经过中间稳定镜像。不支持的路径必须给出可执行提示，绝不能猜测。

## Maintenance 事务

破坏性迁移要求 OpenVPN 已停止，并通过目标镜像的 `openvpn-maintenance` 服务执行。`migrate plan` 只读；`migrate apply` 要求显式确认、`OVPN_MAINTENANCE=true`、独占 runtime lock、持久化完整快照及 digest、staging、目标验证和原子安装。

数据库 audit 与业务状态在同一 transaction 提交。跨文件与 SQLite 的变化使用 operation journal 和 staging，使中断可以确定完成或回滚。

最终报告必须列出 snapshot、digest、导入数量、state doctor 结果、YAML export 步骤和 profile 重分发影响。

## 回滚

迁移后的目录不能交给不支持其 schema 的镜像。schema 3 → 4 成功后回滚时，必须校验并恢复完整迁移快照，再运行 `sh-ver` 镜像；只切换镜像不受支持。

普通 schema 4 备份必须停止全部 writer，并归档完整数据和配置目录。禁止只恢复 `state.db`，也禁止把备份合并到 live artifact 中。

## 完成定义

schema 变化至少必须包含：

- schema 递增与权威模型定义；
- 明确支持和拒绝的源 schema；
- 健康、损坏、冲突、大量数据和中断 fixture；
- staging、snapshot、digest、rollback 和 crash recovery 测试；
- 目标 `state doctor` 与真实 runtime handoff 验证；
- 更新命令、操作、备份、迁移和回滚文档。

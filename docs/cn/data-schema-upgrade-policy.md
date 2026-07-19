# 数据 Schema 升级政策

本文定义独立于版本的持久化数据兼容契约；命令手册换版或镜像发布后仍持续生效。

## 版本独立

镜像版本、OpenVPN runtime 版本和持久化数据 schema 相互独立。schema 使用单调递增的正整数，只在持久化格式发生不兼容变化时递增。多个镜像和 OpenVPN 版本可以共用同一 schema，所有使用该 schema 的镜像都必须以相同方式解释数据。

迁移调度只依据数据目录中记录的 schema 证据，不得按镜像版本、Release tag、源码提交或 OpenVPN 版本选择持久化格式。

## 严格 runtime gate

普通 runtime 代码只接受当前 schema。历史 schema 只能由 `ovpn migrate` 延迟加载的 migration 模块读取；普通 config、client、IP、state、repair、recovery 和服务启动路径不得 source 或解析历史格式。

旧 schema 会使服务启动和读取数据的命令以状态 `78` 拒绝。help、version、capabilities 以及不解析当前格式状态的迁移计划仍可使用。schema 证据冲突、非法、未知或高于当前版本时必须拒绝，不得猜测。

禁止启动时迁移、repair 顺带迁移或 config 补写旧格式。`state doctor` 只诊断当前 schema；历史数据应使用 `migrate plan`。

## 连续迁移

每次 schema 变化必须提供独立的 `N-to-N+1` migration；跨多个 schema 时按顺序连续执行。历史 migration 只能由 migrate dispatcher 加载，普通 runtime 不得 source。

只要仍有足够的已发布格式证据，当前镜像就应保留从每个历史 schema 到当前 schema 的完整迁移链。缺少迁移步骤或证据不足、冲突时必须阻止迁移。

## Maintenance 事务

所有破坏性迁移必须停止 OpenVPN，并通过当前镜像的 `openvpn-maintenance` 服务执行。`migrate plan` 只读；`migrate apply` 必须显式确认、获取独占运行锁、创建持久化快照和事务标记、只迁移 staging 副本、验证目标 schema 与状态并原子提交。失败或中断时恢复原数据。

迁移只使用 maintenance 镜像内置代码，不查询或下载项目 Release。最终报告必须明确列出已替换并需要重新分发的凭据或 profile。

## 回滚

迁移后的数据不能交给不支持其 schema 的旧镜像。迁移后回滚镜像时，必须恢复匹配的迁移前快照；只重建旧容器而不恢复数据不是受支持的回滚方式。

## 完成定义

schema 更新若缺少 schema 递增、连续 migration、代表性源格式夹具、目标验证、失败与恢复测试，或未更新本文和当前操作文档，则不算完成。CI 必须验证所有保留源 schema 到当前 schema 的迁移、当前 schema 幂等以及非法或更新 schema 的拒绝行为。

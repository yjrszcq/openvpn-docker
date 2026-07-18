# 数据 Schema 升级政策

本政策约束所有管理代码版本的持久化数据兼容性。它独立于命令手册版本；旧版命令文档
归档后，本文仍持续有效。

## 相互独立的版本

管理代码版本、镜像版本、OpenVPN runtime 版本和持久化数据 schema 相互独立。
数据 schema 使用单调递增整数，只在持久化状态发生不兼容变化时递增；多个管理代码
和镜像版本可以共用同一个 schema。

所有已发布管理代码、精确源码提交、schema、分发类型、platform API 范围及经过精确
验证的 OpenVPN 版本记录在 `compatibility/data-schema-releases.jsonl`，每次发布都
必须登记。历史
`legacy-image` 条目明确不声明在线 platform 范围；支持在线更新的版本使用
`signed-bundle`。
清单采用每行一个严格 JSON 对象的格式；未知字段和错误类型都会被拒绝，因此清单格式
变化必须显式更新校验器。

```json
{"management_version":"3.0.0","commit":"<40 位 commit>","data_schema":3,"distribution":"signed-bundle","platform_api":{"min":2,"max":2},"openvpn":{"supported":["2.7.5"]}}
```

`legacy-image` 条目的 `platform_api` 使用 `null`。

## 运行边界

管理代码只运行当前 schema。历史 schema 只能由 `ovpn migrate` 加载的 migration 模块
读取；普通 config、client、network、state、repair、render 和运行时数据命令不得
包含历史兼容分支。

旧 schema 必须阻止服务启动。禁止启动时自动迁移、repair 顺带迁移以及
`config apply` 补写旧格式。不读取实例数据的帮助、版本和 runtime 能力检查可以继续
使用。支持旧版本是指支持其迁移，不是允许旧版数据未经迁移直接运行。

## Migration 要求

每次 schema 更新必须提供独立的 `N-to-N+1` migration；跨越多个 schema 时按注册
顺序连续执行。历史 migration 只能由 migrate 命令加载，普通运行时不得 source。

只要已发布 schema 仍有足够证据，最新管理代码就应保留完整迁移链。schema 未知、
高于当前版本、内部不一致或缺少关键证据时，migrate 必须拒绝猜测。

所有破坏性迁移必须离线并通过 `openvpn-maintenance` 执行。migrate 必须提供只读
plan、apply 显式确认、持久化快照、staging、目标完整验证、中断恢复，以及凭据更换、
profile 失效和不可恢复历史报告。

迁移后的数据目录不能交给不支持其 schema 的旧管理代码运行；回滚代码或镜像时，
若其中的代码不支持当前 schema，必须同时恢复匹配的迁移前快照。

## 发布门禁

缺少 migration、夹具、文档或测试的 schema 更新不算完成。CI 必须覆盖清单中每个
已发布版本到当前 schema 的迁移。即使 schema 不变，新管理代码发布仍必须登记版本、
精确源码提交、schema、分发类型、platform API 范围和经过精确验证的 OpenVPN 版本。

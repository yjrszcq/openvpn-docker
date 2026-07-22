# 镜像更新政策

本文是项目代码和 runtime 交付的长期政策。

## 版本模型

`versions.env` 记录独立的发布轴：

- `IMAGE_VERSION`：项目控制面和容器版本。
- `DATA_SCHEMA`：持久化结构化状态格式。
- `OPENVPN_VERSION`：镜像内构建的 OpenVPN Community Edition 版本。
- `GO_RUNTIME_VERSION`：Go 二进制报告的版本；在 `versions.env` 中由 `IMAGE_VERSION` 派生。

构建输入还固定 Go builder 镜像和 OpenVPN 源码 SHA-256。`compatibility/contract.json` 列出该镜像实际验证过的 OpenVPN 精确版本与 feature；`OPENVPN_CANDIDATE_RANGE` 只限制自动化，不代表兼容性承诺。

## 交付边界

镜像是唯一项目代码交付单元。CLI、hook、supervisor、broker、template、migration code 和 compatibility contract 全部内置。runtime 不下载代码、不查询项目 Release，也不选择在线 management bundle。

镜像更新可以改变项目代码、OpenVPN、基础系统或构建依赖，但不能仅因容器启动就静默 apply YAML、重写凭据、迁移数据或修改结构化状态。

## Schema 边界

共用 data schema 的镜像必须以相同方式解释数据。同 schema 更新应停止旧服务、选择新镜像、运行只读诊断并启动新服务，不执行 migration。

目标需要更新 schema 时，普通 runtime 会拒绝旧状态。操作员必须停止 OpenVPN，并按 [数据 schema 升级政策](data-schema-upgrade-policy.md)通过目标镜像执行显式 maintenance 迁移。

## Tag 政策

GHCR 接收项目镜像 tag（`4.0.2`、`4.0`、`4`、`latest`）以及验证过的 OpenVPN tag。Docker Hub 继续提供现有部署使用的 OpenVPN 版本 tag。生产环境必须固定明确 tag，并在更新后检查 `ovpn version --json`。

每个候选镜像都必须先测试再发布。候选范围可以阻止 stable 提升，但不会阻止已测试的候选镜像发布。prerelease `IMAGE_VERSION` 不允许稳定提升；OpenVPN 跨 minor 更新需要经过配置的审批边界。

## 回滚边界

同 schema 镜像可通过旧镜像重建服务回滚。发生 schema 迁移后，只回滚镜像无效：必须恢复匹配的完整迁移前快照，并使用支持该恢复 schema 的镜像。

## 发布要求

每个稳定版本必须通过：

- Go format、vet、unit、race、build、module 和依赖许可证；
- workflow、Shell 和官方源码完整性检查；
- 严格 YAML、SQLite、PKI、生命周期、repair、recovery 和 migration 测试；
- 从支持的上一格式执行真实 schema handoff 和 rollback；
- 真实 UDP/TCP 客户端连接；
- amd64/arm64 镜像构建和动态依赖审计；
- 证明镜像不含旧 runtime Shell/Python 的内容检查；
- 完整中英文命令、操作、迁移、备份和回滚文档。

镜像/schema 版本在发布准备阶段修改。项目镜像版本应通过 `scripts/update-image-version.sh X.Y.Z` 更新；脚本会同步已跟踪的版本权威、Go fallback 版本、测试断言和当前 tag 示例，并验证 release metadata。被忽略的发布草稿有意不在脚本处理范围内。任何门禁、review finding 或 release metadata 不一致未解决时，都必须阻止稳定发布。

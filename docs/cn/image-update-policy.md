# 镜像更新政策

本文是项目代码与 runtime 更新的长期政策；即使命令手册按版本归档，本文仍持续生效。

## 版本模型

项目保留三套相互独立的版本：

- `IMAGE_VERSION` 表示项目代码与容器镜像版本。
- `OPENVPN_VERSION` 表示镜像内构建的 OpenVPN runtime 版本。
- 整数数据 schema 表示持久化 `/etc/openvpn` 格式。

管理脚本、hook、模板、broker 以及 Web/API 代码都属于镜像。容器内不存在代码下载器、
管理 bundle、active 代码选择器或在线 rollback 机制。

## 交付边界

镜像是唯一代码交付单元。普通更新通过拉取或构建新镜像并重建容器完成。镜像可以更新
项目代码、OpenVPN、系统依赖或基础操作系统；运行时命令不会安装代码或软件包，也不会
查询项目 Release。

镜像更新不得隐式改写配置、凭据或其他持久化状态。新模板只能由正常、显式的配置或
生命周期命令应用。

## 数据 schema 边界

新旧镜像使用相同数据 schema 时，持久化格式必须完全一致；代码不得按镜像版本分支
解释该格式。运维人员可停止旧容器后使用新镜像重建。

新镜像要求更高 schema 时，普通 runtime 会以退出状态 `78` 拒绝旧数据。必须停止
OpenVPN，并通过新镜像的 `openvpn-maintenance` 服务执行 `ovpn migrate`。迁移只使用
该 maintenance 镜像内置代码，不访问网络。

任何不兼容的持久化格式变更都必须递增 schema 并增加下一段连续 migration；具体规则
遵循[数据 schema 升级政策](data-schema-upgrade-policy.md)。

## 回滚边界

同 schema 镜像可通过使用旧镜像重建容器来回滚。数据迁移后，仅回滚镜像不等于回滚
数据；在运行仍要求旧 schema 的镜像前，必须恢复与该次迁移匹配的迁移前快照。

## 发布要求

每个镜像版本必须通过静态检查、schema gate、代表性迁移、持久化状态接续、runtime
生命周期测试和相关网络 E2E 矩阵。`compatibility/contract.env` 记录该镜像实际验证过
的 OpenVPN 精确版本；`OPENVPN_CANDIDATE_RANGE` 只限制自动化可提出的上游版本。

镜像版本在独立 release commit 中修改。持久化格式变更若缺少 schema 递增、migration、
夹具、政策更新或迁移测试，则不算完成。

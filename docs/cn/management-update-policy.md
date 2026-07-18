# 管理代码在线更新政策

本政策独立于命令手册版本，用于分离管理代码更新、运行镜像、OpenVPN 内核和持久化
数据迁移。

## 版本边界

`MANAGEMENT_VERSION`、`IMAGE_VERSION`、`OPENVPN_VERSION` 和整数数据 schema
相互独立。GitHub 稳定版 `vX.Y.Z` Release 表示管理代码版本；镜像提供操作系统环境、
OpenVPN 内核以及管理 bundle 使用的 platform API。

每个管理版本必须声明支持的 platform API、OpenVPN 版本范围和精确数据 schema。
在线更新遇到不兼容目标时必须拒绝；platform 或 OpenVPN 不满足要求时，应提示用户
先升级镜像。

发布登记表区分不能在线安装的历史 `legacy-image` 版本和 `signed-bundle` 版本。
由于源码提交无法包含自身的提交哈希，签名发布先形成候选源码提交，再由默认分支上的
后续提交登记其精确哈希，最后给候选源码提交打 tag。发布 workflow 从默认分支读取
登记表；未登记 tag 或 schema、platform、OpenVPN 任一不匹配时都必须拒绝。

## 在线更新边界

`ovpn upgrade` 只能替换与当前实例 schema 相同的已签名管理代码。它不得替换
OpenVPN、安装系统包、迁移数据、改写配置或证书、重载 OpenVPN 配置或断开客户端。

Release 资产解包前必须通过镜像内置公钥签名和 SHA-256 校验。已验证资产及
active/previous 指针持久化在 `/etc/openvpn/repair/.scripts`，实际执行副本另行还原。
draft、prerelease、降级、未签名资产及不兼容版本必须拒绝。

镜像内置的 CLI 与 hook launcher 在每次调用时解析 active bundle。因此切换 active
指针后，新管理命令和后续 hook 事件立即使用新代码，无需向 OpenVPN 进程发送信号、
重载配置或替换进程。更新器的网络配置只使用标准 HTTP 代理变量和可选的只读 GitHub
token。

## 数据迁移与镜像更新

改变 schema 的目标只能在停止服务后，通过 `openvpn-maintenance` 中的
`ovpn migrate` 安装。迁移必须共同 staging 目标代码和数据，验证后一起提交，失败时
恢复二者。

OpenVPN、操作系统依赖、不可变更新 bootstrap 或 platform API 变化必须升级镜像。
更新镜像不得隐式迁移持久化数据。

## 发布门禁

稳定管理版本应发布可复现 bundle、严格清单、SHA-256 和 Ed25519 签名，不要求同步
发布稳定镜像。CI 必须验证兼容矩阵、签名、schema 登记、在线更新不中断，以及清单中
所有历史基线到当前 schema 的迁移。

# 安全策略 / Security Policy

## 支持版本

| 版本 | 支持状态 |
|---|---|
| latest release | ✅ |
| 旧版本 | ❌ |

## 报告漏洞

**请不要在公开 Issue 中描述安全漏洞。**

本项目部署时会持有 Yearning 系统库凭据并读写其工单/记录表，以下情况请按安全问题对待：

- 可导致未经授权读写 Yearning 库或被审计数据源的问题
- RPC 接口未经验证即可调用造成的影响（本引擎默认监听 `127.0.0.1`，请注意不要暴露到公网）
- 敏感信息（凭据/密钥）泄漏到日志或错误信息

报告方式：在 GitHub 上对本仓库发起 [Security Advisory（私有报告）](https://github.com/jiaquanchou/juno-mac/security/advisories/new)，或先开一个不含细节的 Issue 约定沟通渠道。确认后会尽快修复并发布补丁版本。

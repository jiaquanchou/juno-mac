# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式，版本号遵循 [SemVer](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.1.1] - 2026-09-27

### Added

- 规则支持矩阵生成器 [tools/gen-rules-matrix](./tools/gen-rules-matrix/main.go)：从 `AuditRole` 结构体自动生成中/英 README 矩阵，`--check` 供 CI 强制校验两端与代码一致
- 新增 Windows（amd64/arm64）平台构建

### Changed

- Release 流水线迁移到 [goreleaser](https://goreleaser.com)：发布产物改为 tar.gz/zip 压缩包（内含 README/LICENSE/CHANGELOG）并附 `checksums.txt` SHA256 校验和
- README 新增 `go install` 安装方式与 Go Report Card 徽章

## [0.1.0] - 2026-09-27

首个公开版本。

### Added

- 实现与 Yearning `v3.1.x` web 端对应的审核引擎 RPC 契约：`Engine.Check` / `Engine.Exec` / `Engine.Query` / `Engine.StopDelay` / `Engine.MergeAlterTables`
- 规则字段与官方 `engine.AuditRole` 一一对应，覆盖 DML/DDL 常用规则子集（详见 README 规则支持矩阵）
- 工单执行：执行前按工单规则重审、失败即中断、执行记录与工单状态回写
- 回滚语句生成：单表 UPDATE/DELETE 快照反推、自增主键表 INSERT 按主键水位
- 影响行数估算：单表 UPDATE/DELETE 用同条件 `COUNT(*)`（兼容新版 MySQL 无 rows 列的 EXPLAIN）
- 与 Yearning 共用 `conf.toml` 与系统库，`RpcAddr` 为空时回落 `127.0.0.1:50001`
- `-selftest` 内置自测、`--version` 版本输出
- 单元测试（规则/回滚字面量/配置解析）与 GitHub Actions CI

[Unreleased]: https://github.com/jiaquanchou/juno-mac/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/jiaquanchou/juno-mac/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/jiaquanchou/juno-mac/releases/tag/v0.1.0

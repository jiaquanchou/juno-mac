# 贡献指南 / Contributing

感谢关注 juno-mac！欢迎 Issue 与 PR。

## 提 Issue

- 提交前先搜索已有 Issue，避免重复
- 提「规则判定」相关问题请附上：Yearning 版本、当时的规则开关（管理后台截图或 JSON）、原始 SQL 与预期/实际结果

## 提 PR

1. Fork 后从 `main` 拉分支
2. 提交前确保：

   ```bash
   gofmt -w .
   go vet ./...
   go test ./...
   ```

   CI 会跑同样三项检查。

3. 新增/修改审核规则请：
   - 在 `internal/audit/rules.go` 实现对应字段
   - 在 `internal/audit/check_test.go` 补表驱动用例
   - 同步更新 `README.md` 与 `README_EN.md` 的规则支持矩阵

## 约定

- 提交信息用中文或英文均可，一行概括 + 必要的正文说明
- 与官方 Yearning 的兼容性改动（如 RPC 结构体字段）必须引用对应的 Yearning 源码位置（文件 + 版本）

## 本地跑通联调

参考 README「快速开始」：需要一个 Yearning web 端（`./Yearning run`）+ 一个被审计的 MySQL，本引擎 `./juno-mac -config conf.toml` 后在 UI 提工单即可联调。

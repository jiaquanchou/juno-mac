# juno-mac

Yearning SQL 审计平台「审核引擎」的本地实现（macOS 原生可跑，无需 Docker）。

A local reimplementation of the [Yearning](https://github.com/cookieY/Yearning) audit engine, making the web platform fully functional on macOS without Docker.

> **非官方项目** / Not affiliated with Yearning. 本项目按 Yearning 公开源码中的引擎 RPC 契约独立实现，供本地学习与测试环境使用；审核判定语义与官方引擎（Juno）可能存在差异。

## 背景

Yearning v3 的架构是两个进程：

```text
┌─────────────────┐   net/rpc (HTTP)   ┌──────────────────┐
│  Yearning web   │ ─────────────────▶ │   审核引擎        │
│  (开源, AGPL)    │   Engine.Check     │   官方 Juno       │
│  :8000          │   Engine.Exec      │   闭源, 仅 Linux  │
│                 │   Engine.Query …   │                  │
└─────────────────┘                    └──────────────────┘
```

官方引擎 **Juno 不开源且只提供 Linux 二进制**，官方文档对 macOS 用户的建议是用 Docker 运行。本项目在 macOS（Apple Silicon 原生）实现同一套 RPC 契约，让 Yearning 在无 Docker 的 Mac 上跑通完整的「检测 → 工单 → 执行 → 回滚」链路，也可用于 Linux/macOS 的本地学习环境。

## 实现的 RPC 契约

与 Yearning web 端（`v3.1.x` 源码）的调用点一一对应：

| 方法 | 对应 Yearning 功能 |
|---|---|
| `Engine.Check` | 工单提交前 SQL 规则检测（`PUT /api/v2/fetch/test`） |
| `Engine.Exec` | 工单审批通过后的执行（含执行前重审、回滚语句生成、执行记录/工单状态回写） |
| `Engine.Query` | 查询工单预检（仅放行 SELECT 并自动补 LIMIT） |
| `Engine.StopDelay` | 延时执行取消（本地实现为 no-op） |
| `Engine.MergeAlterTables` | 多条 ALTER 语句合并 |

规则字段与官方 `engine.AuditRole` 结构一一对应——在 Yearning「管理后台 → 审核规则」里改动规则，本引擎即时生效。已实现的主要规则族：

- DML：WHERE 必带（DMLWhere）、limit 禁用、order by 检查、insert 列名/行数上限/null 值、DML 中禁 SELECT
- DDL：drop table/database 禁用、主键/自增/表前缀/表名长度/必须字段、表与列注释、NOT NULL、默认值、char 长度上限、索引数量与索引列数、外键/分区/视图禁用、charset/collation 白名单、单 alter 多操作限制、多条 DDL 限制、保留字检查、float/double 建议 decimal
- 影响行数：UPDATE/DELETE 按同条件 `SELECT COUNT(*)` 精确估算（新版 MySQL 的 EXPLAIN 已无 rows 列），超过 `MaxAffectRows` 报错

## 快速开始

```bash
# 构建本引擎（与 Yearning web 端共用一份 conf.toml）
go build -o juno-mac .

# conf.toml 与 Yearning 共用，追加/确认 [General] 段：
#   RpcAddr = "127.0.0.1:50001"   # 本引擎监听地址
#   Lang    = "zh_CN"
# [Mysql] 段指向 Yearning 系统库（引擎用它回写执行记录与回滚语句）

./juno-mac -config /path/to/conf.toml     # 默认监听 127.0.0.1:50001
./juno-mac -selftest                       # 内置自测：对样例 SQL 跑一遍规则
```

Yearning 侧（`v3.1.9.1` 实测）：

1. `./Yearning install` 初始化（需先建好 utf8mb4 的系统库）
2. `./Yearning run` 启动 web（默认 :8000，账号 admin/Yearning_admin）
3. 管理后台添加数据源（指向被审计的 MySQL），流程模板至少两步（提交 → 审核）
4. 提交工单即触发 `Engine.Check` 检测；审批通过即触发 `Engine.Exec` 执行

## 已知限制

- 审核判定为独立实现，与官方闭源引擎 Juno 的判定细节可能不同（规则开关语义一致，文案与部分边界行为可能不同）
- 回滚语句生成覆盖：单表 UPDATE/DELETE（按执行前快照反推）、自增主键表的 INSERT（按主键水位删除）；多表更新、无主键表 INSERT 等场景不生成
- 延时执行（delay）未实现调度，`StopDelay` 为空操作；OSC（pt-online-schema-change）相关规则未实现
- 引擎与 Yearning web 共库共配置（官方约定），因此会直接读写 Yearning 系统库的工单/记录表

## License

AGPL-3.0（与 Yearning 一致——本项目的参数结构镜像自其 AGPL 源码）。

Yearning 由 [cookieY](https://github.com/cookieY) 开发，感谢其优秀的开源工作。

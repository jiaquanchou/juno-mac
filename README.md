# juno-mac

[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![CI](https://github.com/jiaquanchou/juno-mac/actions/workflows/ci.yml/badge.svg)](https://github.com/jiaquanchou/juno-mac/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/jiaquanchou/juno-mac)](https://github.com/jiaquanchou/juno-mac/releases)

[简体中文](./README.md) | [English](./README_EN.md)

**Yearning SQL 审计平台「审核引擎」的本地实现**——让 [Yearning](https://github.com/cookieY/Yearning) 在 macOS（以及任意无 Docker 的环境）上跑通完整的 SQL 审核 / 执行 / 回滚链路。

> **非官方项目**：按 Yearning 公开源码中的引擎 RPC 契约独立实现。官方审核引擎 Juno 闭源且仅提供 Linux 二进制（官方建议 macOS 用户用 Docker 运行）；本项目让无 Docker 的环境也能原生运行。审核判定语义与官方引擎可能存在差异，更适合本地学习与测试环境。

## 背景

Yearning v3 是「web 平台 + 独立审核引擎」的双进程架构：

```text
┌──────────────────┐    net/rpc (HTTP)    ┌────────────────────────┐
│   Yearning web   │ ───────────────────▶ │       审核引擎          │
│   (开源, AGPL)    │    Engine.Check      │  官方 Juno: 闭源,       │
│   :8000          │    Engine.Exec       │  仅提供 Linux 二进制    │
│                  │    Engine.Query …    │  ← 本项目填补的位置 →   │
└──────────────────┘                      └────────────────────────┘
```

web 端源码里只有引擎的调用契约，没有引擎实现。`juno-mac` 按同一契约（`Engine.Check / Exec / Query / StopDelay / MergeAlterTables`）提供本地引擎，规则字段与官方 `AuditRole` 一一对应——在 Yearning「管理后台 → 审核规则」里改动规则，本引擎即时生效。

## 特性

- **规则检测**：基于 `pingcap/tidb/parser` 真实解析 SQL，覆盖官方 `AuditRole` 规则的常用子集（WHERE 必带、DROP 禁用、行数上限、注释/默认值/主键规范、字符集白名单等，完整清单见下方规则支持矩阵）
- **工单执行**：审批通过后执行 DDL/DML，执行前按工单规则重审（error 级直接拒绝）
- **自动回滚**：单表 UPDATE/DELETE 按执行前快照生成反向语句，自增主键表的 INSERT 按主键水位生成 DELETE，回滚语句写入 Yearning 的 `core_rollbacks`
- **查询审计**：查询工单预检（仅放行 SELECT 并自动补 LIMIT）、多条 ALTER 合并
- **共库共配置**：与 Yearning 共用同一份 `conf.toml` 和 MySQL 库（官方约定），执行记录与工单状态自动回写

## 快速开始

### 1. 构建

```bash
go build -o juno-mac ./cmd/juno-mac
./juno-mac --version
```

或从 [Releases](https://github.com/jiaquanchou/juno-mac/releases) 直接下载对应平台的二进制（darwin/linux，amd64/arm64）。

### 2. 配置

编辑 Yearning 的 `conf.toml`（与 web 端共用同一份）：

```toml
[Mysql]
Db = "yearning"
Host = "127.0.0.1"
Port = "3306"
Password = "…"
User = "yearning_sys"

[General]
SecretKey = "…"                 # 与 web 端一致（16 位）
RpcAddr = "127.0.0.1:50001"     # 本引擎监听地址
Lang = "zh_CN"
```

### 3. 初始化与启动

```bash
# 先在 MySQL 建好 Yearning 系统库（utf8mb4），然后：
./Yearning install   # web 端初始化
./Yearning run       # web 端启动（:8000）

./juno-mac -config conf.toml   # 本引擎启动（默认 :50001）
```

Yearning 侧随后添加数据源（指向被审计的 MySQL）与流程模板（至少两步：提交 → 审核），提交工单即触发 `Engine.Check` 检测，审批通过即触发 `Engine.Exec` 执行。

### 4. 内置自测

```bash
./juno-mac -config conf.toml -selftest
```

```text
===== 默认规则（Yearning install 初始值，无目标库连接） =====
--- [kind=0] DROP TABLE users;
    level=0 status=错误 affect=0 table=users schema=yearning_demo error="drop table语句已被当前审核规则禁止执行"
--- [kind=1] UPDATE users SET status = 0;
    level=2 status=通过 affect=0 table=users schema=yearning_demo error=""
--- [kind=1] UPDATE users SET status = 0 WHERE id = 1 LIMIT 1;
    level=0 status=错误 affect=0 table=users schema=yearning_demo error="UPDATE语句不允许使用limit关键字"
…
```

## 规则支持矩阵

`✅` 已实现（按官方规则开关生效）｜ `⚠️` 部分实现 ｜ `❌` 未实现（对应规则开关不生效）

### DML 规则

| 规则字段 | 状态 | 说明 |
|---|---|---|
| `DMLWhere` | ✅ | UPDATE/DELETE 必须携带 WHERE（**官方默认关闭**） |
| `DMLAllowLimitSTMT` | ✅ | 是否允许 DML 使用 LIMIT |
| `DMLInsertColumns` | ✅ | INSERT 必须显式声明列名 |
| `DMLMaxInsertRows` | ✅ | 单条 INSERT 最大行数 |
| `DMLWhereExprValueIsNull` | ✅ | WHERE 与 NULL 比较告警 |
| `DMLOrder` | ✅ | DML 中的 ORDER BY 告警 |
| `DMLSelect` | ✅ | DML 工单禁含 SELECT |
| `DMLAllowInsertNull` | ✅ | INSERT 含 NULL 告警 |
| `DMLInsertMustExplicitly` | ⚠️ | 与 `DMLInsertColumns` 同路径 |
| `DMLTransaction` | ❌ | 事务化执行未实现（当前逐条自动提交） |

### DDL 规则

| 规则字段 | 状态 | 说明 |
|---|---|---|
| `DDLEnablePrimaryKey` | ✅ | 表必须有主键 |
| `DDLEnableAutoIncrement` | ✅ | 主键建议自增 |
| `DDLEnableAutoincrementUnsigned` | ✅ | 自增列建议 unsigned |
| `DDLEnableDropTable` | ✅ | DROP/TRUNCATE 禁用 |
| `DDLEnableDropDatabase` | ✅ | DROP DATABASE 禁用 |
| `DDLCheckTableComment` | ✅ | 表注释告警 |
| `DDlCheckColumnComment` | ✅ | 列注释告警 |
| `DDLCheckColumnNullable` | ✅ | NOT NULL 建议 |
| `DDLCheckColumnDefault` | ✅ | 默认值告警 |
| `DDLCheckFloatDouble` | ✅ | float/double 建议 decimal |
| `DDLMaxCharLength` | ✅ | char/varchar 长度上限 |
| `DDLMaxKey` / `DDLMaxKeyParts` | ✅ | 索引数量 / 单索引字段数上限 |
| `MaxTableNameLen` | ✅ | 表名长度上限 |
| `MaxAffectRows` | ✅ | 影响行数上限（UPDATE/DELETE 精确 COUNT，INSERT 按 VALUES 数） |
| `SupportCharset` / `SupportCollation` | ✅ | 字符集/排序规则白名单 |
| `CheckIdentifier` | ✅ | 保留字检查（内置保留字表） |
| `MustHaveColumns` | ✅ | 建表必须字段 |
| `DDLMultiToCommit` | ✅ | 单工单多条 DDL 限制 |
| `DDLAllowMultiAlter` | ✅ | 单条 ALTER 多操作限制 |
| `DDLAllowColumnType` | ✅ | 禁改列类型 |
| `DDLAllowChangeColumnPosition` | ✅ | AFTER/FIRST 位置告警 |
| `DDLEnableForeignKey` | ✅ | 外键禁用 |
| `AllowCreateView` / `AllowCreatePartition` | ✅ | 视图/分区禁用 |
| `DDLPrimaryKeyMust` | ✅ | 主键名必须为 id |
| `DDLEnableNullIndexName` | ✅ | 空索引名检查 |
| `DDLIndexNameSpec` | ❌ | 索引命名规范 |
| `DDLEnableAcrossDBRename` | ❌ | 跨库表迁移 |
| `DDLEnableAutoincrementInit` | ❌ | 自增初始值 |
| `MaxDDLAffectRows` | ❌ | DDL 影响行数上限 |
| `DDLImplicitTypeConversion` | ❌ | 隐式类型转换 |
| `DDLAllowPRINotInt` | ❌ | 主键非整型 |
| `DDLColumnsMustHaveIndex` | ❌ | 指定列必须有索引 |
| `AllowCrateViewWithSelectStar` | ❌ | CREATE VIEW SELECT * |
| `AllowSpecialType` | ❌ | 特殊类型 |
| `IsOSC` / `OSCExpr` / `OscSize` | ❌ | pt-OSC 相关 |
| `PRIRollBack` | ❌ | 主键回滚 |

## RPC 契约

与 Yearning `v3.1.x` web 端调用点一一对应（`net/rpc` gob 编码，`RpcAddr` 走 HTTP-RPC）：

| 方法 | Yearning 调用点 | 说明 |
|---|---|---|
| `Engine.Check` | `PUT /api/v2/fetch/test` | 工单检测；`Record.Level`：0=错误（阻断提交）、1=警告、2=通过 |
| `Engine.Exec` | 审批链最后一步 | 执行 + 回滚 + 记录回写（`core_sql_records` / `core_rollbacks` / `core_sql_orders`） |
| `Engine.Query` | 查询工单预检 | 仅放行 SELECT 并自动补 LIMIT |
| `Engine.MergeAlterTables` | 工单编辑页 | 多条 ALTER 按表合并 |
| `Engine.StopDelay` | 延时工单取消 | 本地实现为 no-op（不支持延时调度） |

## 已知限制

- 审核判定为独立实现，与官方闭源引擎 Juno 的判定细节可能不同（规则开关语义一致，文案与部分边界行为不同）
- 回滚生成覆盖：单表 UPDATE/DELETE、自增主键表 INSERT；多表更新、无主键表 INSERT 等不生成
- 延时执行（delay）与 pt-OSC 未实现
- 引擎与 web 共库共配置（官方约定），会直接读写 Yearning 系统库的工单/记录表

## FAQ

**Q: 与官方 Juno 什么关系？**
Juno 未开源且仅提供 Linux 二进制。本项目是按 Yearning 开源码中可见的调用契约做的独立实现，用于填补 macOS / 无 Docker 环境的空白，与官方无隶属关系。

**Q: 生产环境能用吗？**
不建议。定位是本地学习与测试环境；生产请使用官方发行版。

**Q: 为什么有些规则默认不起作用？**
规则开关来自 Yearning 管理后台（如 `DMLWhere` 官方默认关闭）——这与官方行为一致，到「管理后台 → 审核规则」里开启即可。

## 本地开发

```bash
make test      # go test ./...
make vet       # go vet ./...
make fmt       # gofmt -w .
make build     # go build -o juno-mac ./cmd/juno-mac
```

欢迎 Issue 与 PR；提交前请确保 `go test ./...` 与 `gofmt` 通过（CI 会检查）。

## 致谢与许可

- [cookieY/Yearning](https://github.com/cookieY/Yearning) —— 本项目对接的平台，其开源的调用契约是本项目的实现依据
- [pingcap/tidb/parser](https://github.com/pingcap/tidb) —— SQL 解析

本项目采用 **AGPL-3.0** 许可（与 Yearning 一致，参数结构镜像自其 AGPL 源码）。

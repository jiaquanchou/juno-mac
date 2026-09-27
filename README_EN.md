# juno-mac

[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![CI](https://github.com/jiaquanchou/juno-mac/actions/workflows/ci.yml/badge.svg)](https://github.com/jiaquanchou/juno-mac/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/jiaquanchou/juno-mac)](https://github.com/jiaquanchou/juno-mac/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/jiaquanchou/juno-mac)](https://goreportcard.com/report/github.com/jiaquanchou/juno-mac)

[简体中文](./README.md) | [English](./README_EN.md)

**A local implementation of the audit engine for the [Yearning](https://github.com/cookieY/Yearning) SQL audit platform** — it makes Yearning work end-to-end (rule check → order execution → rollback) on macOS and any environment without Docker.

> **Unofficial project**: an independent implementation of the engine RPC contract exposed by Yearning's open-source code. The official audit engine ("Juno") is closed-source and only ships Linux binaries (the official docs suggest running it via Docker on macOS). Audit verdicts come from this implementation and may differ from the official engine — best suited for local learning and testing environments.

## Background

Yearning v3 runs as two processes: a web platform and a separate audit engine.

```text
┌──────────────────┐    net/rpc (HTTP)    ┌─────────────────────────────┐
│   Yearning web   │ ───────────────────▶ │        Audit engine         │
│   (open, AGPL)   │    Engine.Check      │  Official Juno: closed-     │
│   :8000          │    Engine.Exec       │  source, Linux binaries     │
│                  │    Engine.Query …    │  ← this project fills in →  │
└──────────────────┘                      └─────────────────────────────┘
```

The web platform's source only contains the *client side* of the engine contract — no engine implementation. `juno-mac` implements the same contract (`Engine.Check / Exec / Query / StopDelay / MergeAlterTables`). Rule fields mirror the official `AuditRole` one-to-one, so toggling rules in Yearning's admin UI takes effect immediately.

## Features

- **Rule check**: real SQL parsing via `pingcap/tidb/parser`; implements the common subset of official `AuditRole` rules (mandatory WHERE, DROP ban, affected-rows limits, comment/default/PK conventions, charset whitelist… — full matrix below)
- **Order execution**: executes audited DDL/DML after approval, re-checks rules before executing (error-level statements are refused)
- **Automatic rollback**: reverse statements for single-table UPDATE/DELETE from a pre-execution snapshot, DELETE by PK watermark for INSERT into auto-increment tables; rollbacks are written into Yearning's `core_rollbacks`
- **Query audit**: query-order pre-check (SELECT-only with automatic LIMIT), multi-ALTER merging
- **Shared DB & config**: shares the same `conf.toml` and MySQL schema with Yearning (official convention); execution records and order statuses are written back automatically

## Quick start

### 1. Build

```bash
go build -o juno-mac ./cmd/juno-mac
./juno-mac --version
```

或安装到 GOPATH（需要 Go 1.22+）：

```bash
go install github.com/jiaquanchou/juno-mac/cmd/juno-mac@latest
```

也可以从 [Releases](https://github.com/jiaquanchou/juno-mac/releases) 下载对应平台的压缩包（含 SHA256 校验和，darwin/linux/windows，amd64/arm64）。


### 2. Configure

Edit Yearning's `conf.toml` (shared with the web side):

```toml
[Mysql]
Db = "yearning"
Host = "127.0.0.1"
Port = "3306"
Password = "…"
User = "yearning_sys"

[General]
SecretKey = "…"                 # must match the web side (16 chars)
RpcAddr = "127.0.0.1:50001"     # this engine's listen address
Lang = "zh_CN"
```

### 3. Initialize & run

```bash
# Create the Yearning system database (utf8mb4) in MySQL first, then:
./Yearning install   # web-side initialization
./Yearning run       # web side (:8000)

./juno-mac -config conf.toml   # this engine (default :50001)
```

Then, in the Yearning UI, add a data source pointing to the audited MySQL and a workflow template with at least two steps (submit → audit). Submitting an order triggers `Engine.Check`; approving the final step triggers `Engine.Exec`.

### 4. Self test

```bash
./juno-mac -config conf.toml -selftest
```

## Rule support matrix

`✅` implemented (follows the official rule switch) ｜ `⚠️` partial ｜ `❌` not implemented
Generated from the `AuditRole` struct by [tools/gen-rules-matrix](./tools/gen-rules-matrix/main.go); CI enforces sync with the code.

<!-- rules-matrix:en:start -->
| Rule | Category | Status | Notes |
|---|---|---|---|
| `DMLTransaction` | DML | ❌ | Transactional execution not implemented (autocommit per statement) |
| `DMLAllowLimitSTMT` | DML | ✅ | Allow LIMIT in DML |
| `DMLInsertColumns` | DML | ✅ | INSERT must declare columns |
| `DMLMaxInsertRows` | DML | ✅ | Max rows per INSERT |
| `DMLWhere` | DML | ✅ | UPDATE/DELETE require a WHERE clause (off by default in Yearning) |
| `DMLWhereExprValueIsNull` | DML | ✅ | Warn on NULL comparisons in WHERE |
| `DMLOrder` | DML | ✅ | Warn on ORDER BY in DML |
| `DMLSelect` | DML | ✅ | Forbid SELECT inside DML orders |
| `DMLAllowInsertNull` | DML | ✅ | Warn on NULL inserts |
| `DMLInsertMustExplicitly` | DML | ⚠️ | Same code path as DMLInsertColumns |
| `DDLEnablePrimaryKey` | DDL | ✅ | Table must have a primary key |
| `DDLCheckTableComment` | DDL | ✅ | Warn when table comment missing |
| `DDlCheckColumnComment` | General | ✅ | Warn when column comment missing |
| `DDLCheckColumnNullable` | DDL | ✅ | Suggest NOT NULL |
| `DDLCheckColumnDefault` | DDL | ✅ | Warn when default value missing |
| `DDLEnableAcrossDBRename` | DDL | ❌ | Cross-database rename |
| `DDLEnableAutoincrementInit` | DDL | ❌ | AUTO_INCREMENT initial value |
| `DDLEnableAutoIncrement` | DDL | ✅ | Warn when PK is not AUTO_INCREMENT |
| `DDLEnableAutoincrementUnsigned` | DDL | ✅ | Warn on signed auto-increment columns |
| `DDLEnableDropTable` | DDL | ✅ | Forbid DROP/TRUNCATE |
| `DDLEnableDropDatabase` | DDL | ✅ | Forbid DROP DATABASE |
| `DDLEnableNullIndexName` | DDL | ✅ | Forbid empty index names |
| `DDLIndexNameSpec` | DDL | ❌ | Index naming convention |
| `DDLMaxKeyParts` | DDL | ✅ | Index parts limit |
| `DDLMaxKey` | DDL | ✅ | Index count limit |
| `DDLMaxCharLength` | DDL | ✅ | char/varchar length limit |
| `MaxTableNameLen` | General | ✅ | Table name length limit |
| `MaxAffectRows` | General | ✅ | Affected-rows limit (exact COUNT for UPDATE/DELETE, VALUES count for INSERT) |
| `MaxDDLAffectRows` | General | ❌ | DDL affected-rows limit |
| `SupportCharset` | General | ✅ | Charset whitelist |
| `SupportCollation` | General | ✅ | Collation whitelist |
| `CheckIdentifier` | General | ✅ | Reserved-word check (built-in list) |
| `MustHaveColumns` | General | ✅ | Required columns on CREATE TABLE |
| `DDLMultiToCommit` | DDL | ✅ | One DDL statement per order |
| `DDLPrimaryKeyMust` | DDL | ✅ | PK must be named id |
| `DDLAllowColumnType` | DDL | ✅ | Forbid column type changes |
| `DDLImplicitTypeConversion` | DDL | ❌ | Implicit type conversion |
| `DDLAllowPRINotInt` | DDL | ❌ | Non-int primary key |
| `DDLAllowMultiAlter` | DDL | ✅ | Forbid multi-spec single ALTER |
| `DDLEnableForeignKey` | DDL | ✅ | Forbid foreign keys |
| `DDLTablePrefix` | DDL | ✅ | Table name prefix |
| `DDLColumnsMustHaveIndex` | DDL | ❌ | Columns that must be indexed |
| `DDLAllowChangeColumnPosition` | DDL | ✅ | Warn on AFTER/FIRST |
| `DDLCheckFloatDouble` | DDL | ✅ | Suggest DECIMAL over FLOAT/DOUBLE |
| `IsOSC` | OSC | ❌ | pt-online-schema-change |
| `OSCExpr` | OSC | ❌ | pt-online-schema-change |
| `OscSize` | OSC | ❌ | pt-online-schema-change |
| `AllowCreateView` | General | ✅ | Forbid views |
| `AllowCrateViewWithSelectStar` | General | ❌ | CREATE VIEW with SELECT * |
| `AllowCreatePartition` | General | ✅ | Forbid partitioned tables |
| `AllowSpecialType` | General | ❌ | Special column types |
| `PRIRollBack` | General | ❌ | PK rollback |
<!-- rules-matrix:en:end -->

## RPC contract

One-to-one with Yearning `v3.1.x` call sites (`net/rpc` gob over HTTP-RPC on `RpcAddr`):

| Method | Yearning call site | Notes |
|---|---|---|
| `Engine.Check` | `PUT /api/v2/fetch/test` | Order check; `Record.Level`: 0=error (blocks submission), 1=warning, 2=pass |
| `Engine.Exec` | final workflow approval | Execute + rollback + record write-back (`core_sql_records` / `core_rollbacks` / `core_sql_orders`) |
| `Engine.Query` | query-order pre-check | SELECT-only, automatic LIMIT |
| `Engine.MergeAlterTables` | order editor | Merge ALTERs per table |
| `Engine.StopDelay` | delayed-order kill | no-op locally (no delayed scheduling) |

## Known limitations

- Verdicts come from this independent implementation and may differ in detail from the official closed-source engine (rule-switch semantics match; wording and edge cases may not)
- Rollback generation covers single-table UPDATE/DELETE and auto-increment INSERT; multi-table updates and non-PK INSERTs produce no rollback
- Delayed execution and pt-OSC are not implemented
- The engine shares the Yearning database and config (official convention) and writes directly to its order/record tables

## FAQ

**Q: Relationship with the official Juno?**
Juno is closed-source with Linux-only binaries. This project is an independent implementation of the contract visible in Yearning's open-source code, filling the gap on macOS / Docker-less environments. Not affiliated with the official project.

**Q: Can I use it in production?**
Not recommended — it targets local learning and testing. Use official releases in production.

**Q: Why do some rules seem inactive by default?**
Rule switches come from Yearning's admin UI (e.g. `DMLWhere` is off by default) — same as official behavior. Enable them under "审核规则" in the admin panel.

## Development

```bash
make test      # go test ./...
make vet       # go vet ./...
make fmt       # gofmt -w .
make build     # go build -o juno-mac ./cmd/juno-mac
```

Issues and PRs are welcome; make sure `go test ./...` and `gofmt` pass before submitting (CI enforces it).

## Credits & license

- [cookieY/Yearning](https://github.com/cookieY/Yearning) — the platform this integrates with; its open-sourced call contract is the basis of this implementation
- [pingcap/tidb/parser](https://github.com/pingcap/tidb) — SQL parsing

Licensed under **AGPL-3.0** (same as Yearning; the parameter structs mirror its AGPL source).

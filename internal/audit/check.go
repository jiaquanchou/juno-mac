// Package audit 实现与官方 Yearning 引擎一致的 SQL 审核逻辑：
// 基于 pingcap/tidb/parser 解析语句，按 AuditRole 规则产出 Record（Level 0 错误 / 1 警告 / 2 通过）。
package audit

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/jiaquanchou/juno-mac/internal/protocol"
)

// CheckSQL 对一段多语句 SQL 做规则审核，返回逐语句 Record。
//
//	target 为被审计数据源的连接（用于影响行数估算），可为 nil（此时不估算 DML 行数）。
//	kind 与 Yearning 工单一致：0 = DDL 工单，1 = DML 工单。
func CheckSQL(sqlText, schema string, kind int, lang string, rule protocol.AuditRole,
	target *sql.DB) []protocol.Record {

	T := translator(lang)

	stmts, err := Parse(sqlText)
	if err != nil {
		return []protocol.Record{{
			SQL: sqlText, Schema: schema,
			Level: protocol.LevelError,
			Error: T("SQL语法错误: ", "SQL syntax error: ") + err.Error(),
		}}
	}

	ddlCount := 0
	for _, st := range stmts {
		if isDDLStmt(st) {
			ddlCount++
		}
	}

	records := make([]protocol.Record, 0, len(stmts))
	for _, st := range stmts {
		text := strings.TrimSpace(st.Text())
		tbl, schemaName := extractTableName(st, schema)
		rec := protocol.Record{
			SQL:        text,
			Table:      tbl,
			Schema:     schemaName,
			AffectRows: estimateAffectRows(st, schema, target),
		}

		var fs []finding
		stmtIsDDL := isDDLStmt(st)

		// 工单类型与语句类型必须匹配
		if (kind == 0 && !stmtIsDDL) || (kind == 1 && stmtIsDDL) {
			orderKind := "DDL"
			if kind == 1 {
				orderKind = "DML"
			}
			fs = append(fs, finding{protocol.LevelError,
				T(orderKind+"工单不允许包含", orderKind+" order does not allow ") +
					stmtTypeName(st) + T(" 语句", " statements")})
		}

		// 多条 DDL 限制
		if stmtIsDDL && ddlCount > 1 && !rule.DDLMultiToCommit {
			fs = append(fs, finding{protocol.LevelError,
				T("工单包含多条DDL语句，当前规则只允许单条DDL提交",
					"Order contains multiple DDL statements; only one is allowed")})
		}

		fs = append(fs, checkStatement(st, rule, target, T)...)

		// 影响行数上限（官方 MaxAffectRows 规则）
		if rule.MaxAffectRows > 0 && rec.AffectRows > rule.MaxAffectRows &&
			!isErrorIn(fs) {
			fs = append(fs, finding{protocol.LevelError,
				T(fmt.Sprintf("预计影响行数(%d)超过最大限制(%d)", rec.AffectRows, rule.MaxAffectRows),
					fmt.Sprintf("estimated affected rows(%d) exceed max(%d)", rec.AffectRows, rule.MaxAffectRows))})
		}

		rec.Level = protocol.LevelPass
		var msgs []string
		for _, f := range fs {
			msgs = append(msgs, f.msg)
			if f.level < rec.Level {
				rec.Level = f.level
			}
		}
		rec.Error = strings.Join(msgs, "\n")
		switch rec.Level {
		case protocol.LevelError:
			rec.Status = T("错误", "ERROR")
		case protocol.LevelWarning:
			rec.Status = T("警告", "WARNING")
		default:
			rec.Status = T("通过", "PASS")
		}
		records = append(records, rec)
	}
	return records
}

func isErrorIn(fs []finding) bool {
	for _, f := range fs {
		if f.level == protocol.LevelError {
			return true
		}
	}
	return false
}

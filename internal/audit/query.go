package audit

import (
	"fmt"
	"strings"

	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/format"

	"github.com/jiaquanchou/juno-mac/internal/protocol"
)

// QueryRewrite 查询工单预检：仅放行 SELECT，按 limit 自动补全，透传脱敏词表。
func QueryRewrite(sqlText string, limit uint64, insulateWordList string) []protocol.Record {
	var out []protocol.Record
	list := parseInsulateWords(insulateWordList)

	stmts, err := Parse(sqlText)
	if err != nil {
		out = append(out, protocol.Record{SQL: sqlText, Level: protocol.LevelError,
			Error: "SQL语法错误: " + err.Error()})
		return out
	}
	for _, st := range stmts {
		text := strings.TrimSpace(st.Text())
		switch st.(type) {
		case *ast.SelectStmt, *ast.SetOprStmt:
			if _, hasLimit := selectHasLimit(st); !hasLimit && limit > 0 {
				text = fmt.Sprintf("%s LIMIT %d", strings.TrimSuffix(text, ";"), limit)
			}
			out = append(out, protocol.Record{SQL: text, Level: protocol.LevelPass,
				Status: "通过", InsulateWordList: list})
		default:
			out = append(out, protocol.Record{SQL: text, Level: protocol.LevelError,
				Error: "查询工单仅允许SELECT语句"})
		}
	}
	return out
}

// MergeAlterTables 把同一张表的多条 ALTER 语句合并为单条（官方 SOAR 合并能力）。
// 语法解析失败时返回错误，由 web 端展示为合并失败。
func MergeAlterTables(sqls string) (string, error) {
	stmts, err := Parse(sqls)
	if err != nil {
		return "", err
	}
	type alterGroup struct {
		table string
		specs []string
	}
	groups := map[string]*alterGroup{}
	var order []string
	for _, st := range stmts {
		at, ok := st.(*ast.AlterTableStmt)
		if !ok {
			continue
		}
		tbl := at.Table.Name.O
		g, exists := groups[tbl]
		if !exists {
			g = &alterGroup{table: tbl}
			groups[tbl] = g
			order = append(order, tbl)
		}
		for _, spec := range at.Specs {
			var sb strings.Builder
			ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)
			if err := spec.Restore(ctx); err == nil && sb.String() != "" {
				g.specs = append(g.specs, sb.String())
			}
		}
	}
	var merged []string
	for _, tbl := range order {
		g := groups[tbl]
		merged = append(merged, fmt.Sprintf("ALTER TABLE %s %s;", tbl, strings.Join(g.specs, ", ")))
	}
	return strings.Join(merged, "\n"), nil
}

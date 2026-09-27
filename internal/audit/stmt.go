package audit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pingcap/tidb/parser"
	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/format"
	// parser 独立使用必须注册 driver
	_ "github.com/pingcap/tidb/parser/test_driver"
)

// Parse 将多语句 SQL 文本解析为语句节点；语法错误时返回错误。
func Parse(sqlText string) ([]ast.StmtNode, error) {
	stmts, _, err := newParser().Parse(sqlText, "", "")
	return stmts, err
}

func newParser() *parser.Parser {
	return parser.New()
}

func restoreNode(n ast.Node) string {
	var sb strings.Builder
	ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)
	_ = n.Restore(ctx)
	return sb.String()
}

func restoreExpr(n ast.ExprNode) string {
	if n == nil {
		return ""
	}
	return restoreNode(n)
}

func isDDLStmt(st ast.StmtNode) bool {
	switch st.(type) {
	case *ast.CreateTableStmt, *ast.AlterTableStmt, *ast.DropTableStmt,
		*ast.DropDatabaseStmt, *ast.TruncateTableStmt, *ast.CreateIndexStmt,
		*ast.DropIndexStmt, *ast.RenameTableStmt, *ast.CreateViewStmt:
		return true
	}
	return false
}

func stmtTypeName(st ast.StmtNode) string {
	switch st.(type) {
	case *ast.InsertStmt:
		return "INSERT"
	case *ast.UpdateStmt:
		return "UPDATE"
	case *ast.DeleteStmt:
		return "DELETE"
	case *ast.SelectStmt, *ast.SetOprStmt:
		return "SELECT"
	case *ast.CreateTableStmt:
		return "CREATE TABLE"
	case *ast.AlterTableStmt:
		return "ALTER TABLE"
	case *ast.DropTableStmt:
		return "DROP TABLE"
	case *ast.DropDatabaseStmt:
		return "DROP DATABASE"
	case *ast.TruncateTableStmt:
		return "TRUNCATE"
	case *ast.CreateIndexStmt:
		return "CREATE INDEX"
	case *ast.DropIndexStmt:
		return "DROP INDEX"
	case *ast.CreateViewStmt:
		return "CREATE VIEW"
	}
	return fmt.Sprintf("%T", st)
}

// extractTableName 提取语句的主表名与库名（多表/子查询等复杂语句返回空表名）。
func extractTableName(st ast.StmtNode, defaultSchema string) (table, schema string) {
	var name *ast.TableName
	switch n := st.(type) {
	case *ast.CreateTableStmt:
		name = n.Table
	case *ast.AlterTableStmt:
		name = n.Table
	case *ast.DropTableStmt:
		if len(n.Tables) > 0 {
			name = n.Tables[0]
		}
	case *ast.TruncateTableStmt:
		name = n.Table
	case *ast.DropDatabaseStmt:
		return n.Name.O, ""
	case *ast.InsertStmt:
		name = tableSourceName(n.Table)
	case *ast.UpdateStmt:
		name = joinTableName(n.TableRefs.TableRefs)
	case *ast.DeleteStmt:
		name = joinTableName(n.TableRefs.TableRefs)
	case *ast.SelectStmt:
		if n.From != nil {
			name = joinTableName(n.From.TableRefs)
		}
	}
	if name == nil {
		return "", defaultSchema
	}
	s := name.Schema.O
	if s == "" {
		s = defaultSchema
	}
	return name.Name.O, s
}

func tableSourceName(refs *ast.TableRefsClause) *ast.TableName {
	if refs == nil {
		return nil
	}
	return joinTableName(refs.TableRefs)
}

func joinTableName(j *ast.Join) *ast.TableName {
	if j == nil {
		return nil
	}
	if ts, ok := j.Left.(*ast.TableSource); ok {
		if t, ok := ts.Source.(*ast.TableName); ok {
			return t
		}
	}
	return nil
}

func selectHasLimit(st ast.StmtNode) (uint64, bool) {
	switch n := st.(type) {
	case *ast.SelectStmt:
		if n.Limit != nil && n.Limit.Count != nil {
			if v, ok := n.Limit.Count.(ast.ValueExpr); ok {
				if i, ok := v.GetValue().(int64); ok {
					return uint64(i), true
				}
			}
			return 0, true
		}
	case *ast.SetOprStmt:
		return 0, true
	}
	return 0, false
}

func parseInsulateWords(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			return list
		}
	}
	var list []string
	for _, w := range strings.Split(raw, ",") {
		if w = strings.TrimSpace(w); w != "" {
			list = append(list, w)
		}
	}
	return list
}

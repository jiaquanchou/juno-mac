package audit

import (
	"database/sql"
	"fmt"

	"github.com/pingcap/tidb/parser/ast"
)

// 影响行数估算：
//   - INSERT 按 VALUES 元组数计；
//   - 单表 UPDATE/DELETE 用同条件 SELECT COUNT(*)（与实际影响行数一致）。
//     不用 EXPLAIN 的原因：MySQL 8/26 新版 EXPLAIN 对 UPDATE/DELETE 输出
//     iterator 树形计划，没有 rows 列。
//   - 复杂语句（多表、无表名）返回 0。

func estimateAffectRows(st ast.StmtNode, schema string, target *sql.DB) uint {
	switch n := st.(type) {
	case *ast.UpdateStmt:
		tbl := joinTableName(n.TableRefs.TableRefs)
		if tbl == nil || target == nil {
			return 0
		}
		return countRows(target, pickSchema(tbl.Schema.O, schema), tbl.Name.O, restoreExpr(n.Where))
	case *ast.DeleteStmt:
		tbl := joinTableName(n.TableRefs.TableRefs)
		if tbl == nil || target == nil {
			return 0
		}
		return countRows(target, pickSchema(tbl.Schema.O, schema), tbl.Name.O, restoreExpr(n.Where))
	case *ast.InsertStmt:
		return uint(len(n.Lists))
	}
	return 0
}

func pickSchema(astSchema, defaultSchema string) string {
	if astSchema != "" {
		return astSchema
	}
	return defaultSchema
}

func countRows(db *sql.DB, schema, table, where string) uint {
	if where == "" {
		where = "TRUE"
	}
	var n int64
	q := fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`%s` WHERE %s", schema, table, where)
	if err := db.QueryRow(q).Scan(&n); err != nil {
		return 0
	}
	return uint(n)
}

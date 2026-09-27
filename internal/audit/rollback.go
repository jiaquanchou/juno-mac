package audit

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pingcap/tidb/parser/ast"
)

// CaptureRollback 在语句执行前捕获回滚语句。
//
// UPDATE/DELETE：按相同条件查回将被影响的行，生成反向语句（pre，执行前返回）。
// INSERT：记录自增主键水位，执行后生成按主键范围的 DELETE（post，执行后返回）。
// 复杂场景（多表更新、无主键表 INSERT）不生成回滚。
func CaptureRollback(target *sql.DB, st ast.StmtNode, schema string) (pre, post []string) {
	switch n := st.(type) {
	case *ast.UpdateStmt:
		tbl := joinTableName(n.TableRefs.TableRefs)
		if tbl == nil {
			return nil, nil
		}
		pre = rollbackForSelect(target, schema, tbl.Name.O, restoreExpr(n.Where), "UPDATE")
	case *ast.DeleteStmt:
		tbl := joinTableName(n.TableRefs.TableRefs)
		if tbl == nil {
			return nil, nil
		}
		pre = rollbackForSelect(target, schema, tbl.Name.O, restoreExpr(n.Where), "DELETE")
	case *ast.InsertStmt:
		tbl := tableSourceName(n.Table)
		if tbl == nil {
			return nil, nil
		}
		if pk := primaryKeyCols(target, schema, tbl.Name.O); len(pk) == 1 {
			var maxBefore sql.NullString
			q := fmt.Sprintf("SELECT MAX(`%s`) FROM `%s`.`%s`", pk[0], schema, tbl.Name.O)
			if err := target.QueryRow(q).Scan(&maxBefore); err == nil && maxBefore.Valid {
				post = []string{fmt.Sprintf("DELETE FROM `%s` WHERE `%s` > %s;",
					tbl.Name.O, pk[0], quoteValue([]byte(maxBefore.String)))}
			}
		}
	}
	return pre, post
}

// rollbackForSelect 按条件查回将被 UPDATE/DELETE 影响的行，生成反向语句。
func rollbackForSelect(target *sql.DB, schema, table, where, verb string) []string {
	if where == "" {
		where = "TRUE"
	}
	sel := fmt.Sprintf("SELECT * FROM `%s`.`%s` WHERE %s", schema, table, where)
	rows, err := target.Query(sel)
	if err != nil {
		log.Printf("[rollback] 回滚捕获查询失败: %v", err)
		return nil
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil
	}
	pk := primaryKeyCols(target, schema, table)

	var out []string
	for rows.Next() {
		raw := make([]sql.RawBytes, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil
		}
		vals := make([]string, len(cols))
		for i, b := range raw {
			var v []byte
			if b != nil {
				v = b
			}
			vals[i] = quoteValue(v)
		}
		switch verb {
		case "UPDATE":
			// 反向：把所有非主键列改回旧值
			var sets []string
			for i, c := range cols {
				if containsStr(pk, c) {
					continue
				}
				sets = append(sets, fmt.Sprintf("`%s`=%s", c, vals[i]))
			}
			if len(sets) == 0 {
				continue
			}
			out = append(out, fmt.Sprintf("UPDATE `%s` SET %s WHERE %s;",
				table, strings.Join(sets, ", "), pkWhere(pk, cols, raw)))
		case "DELETE":
			// 反向：插回整行
			var colList, valList []string
			for i, c := range cols {
				colList = append(colList, fmt.Sprintf("`%s`", c))
				valList = append(valList, vals[i])
			}
			out = append(out, fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s);",
				table, strings.Join(colList, ", "), strings.Join(valList, ", ")))
		}
	}
	return out
}

func pkWhere(pk, cols []string, raw []sql.RawBytes) string {
	var conds []string
	for _, k := range pk {
		for i, c := range cols {
			if c == k {
				var v []byte
				if raw[i] != nil {
					v = raw[i]
				}
				conds = append(conds, fmt.Sprintf("`%s`=%s", k, quoteValue(v)))
			}
		}
	}
	return strings.Join(conds, " AND ")
}

func containsStr(list []string, s string) bool {
	for _, i := range list {
		if i == s {
			return true
		}
	}
	return false
}

func primaryKeyCols(target *sql.DB, schema, table string) []string {
	rows, err := target.Query(
		"SELECT column_name FROM information_schema.key_column_usage "+
			"WHERE table_schema=? AND table_name=? AND constraint_name='PRIMARY' "+
			"ORDER BY ordinal_position", schema, table)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// quoteValue 将行值转为可读且可执行的字面量：
// NULL / 数值原样；日期时间与普通文本用单引号转义；二进制等不可打印值用十六进制。
func quoteValue(b []byte) string {
	if b == nil {
		return "NULL"
	}
	s := string(b)
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return s
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if utf8.Valid(b) && !containsNUL(b) {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "''") + "'"
	}
	return "0x" + hex.EncodeToString(b)
}

// containsNUL 检查是否存在无法安全放进 SQL 文本的字节。
func containsNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

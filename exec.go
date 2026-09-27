package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pingcap/tidb/parser/ast"
)

func buildDSN(ip string, port int, user, pass, schema string) string {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pass
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", ip, port)
	if schema != "" {
		cfg.DBName = schema
	}
	cfg.Timeout = 3 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 60 * time.Second
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	return cfg.FormatDSN()
}

func openYearning() *sql.DB {
	port, _ := strconv.Atoi(C.Mysql.Port)
	db, err := sql.Open("mysql", buildDSN(C.Mysql.Host, port, C.Mysql.User, C.Mysql.Password, C.Mysql.Db))
	if err != nil {
		log.Printf("连接 Yearning 库失败: %v", err)
		return nil
	}
	return db
}

func jsonUnmarshal(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

// 工单状态：0=已驳回 2=待执行 3=完成 4=执行中 5=失败 6=撤销
const (
	orderStatusSuccess = 3
	orderStatusFailed  = 5
)

// ==================== Engine.Exec ====================

func (e *Engine) Exec(args *ExecArgs, reply *bool) error {
	*reply = true
	order := args.Order
	if order == nil || order.WorkId == "" {
		return nil
	}

	ydb := openYearning()
	if ydb != nil {
		defer ydb.Close()
	}

	target := openTarget(args.IP, args.Port, args.Username, args.Password, order.DataBase)
	if target == nil {
		failOrder(ydb, order.WorkId, "无法连接目标数据源 "+fmt.Sprintf("%s:%d", args.IP, args.Port))
		*reply = false
		return nil
	}
	defer target.Close()

	stmts, _, err := newParser().Parse(order.SQL, "", "")
	if err != nil {
		msg := "SQL语法错误: " + err.Error()
		writeRecord(ydb, order.WorkId, order.SQL, "执行失败", 0, msg)
		failOrder(ydb, order.WorkId, msg)
		*reply = false
		return nil
	}

	// 执行前按工单规则重审：存在 error 级语句直接拒绝（与官方「执行前校验」一致）
	kind := order.Type // 0=DDL 1=DML
	pre := checkSQL(order.SQL, order.DataBase, kind, C.General.Lang, args.Rules, args.IP, args.Port, args.Username, args.Password)
	for _, r := range pre {
		if r.Level == LevelError {
			msg := "执行被审核规则拦截: " + r.Error
			writeRecord(ydb, order.WorkId, r.SQL, "执行失败", 0, msg)
			failOrder(ydb, order.WorkId, msg)
			*reply = false
			return nil
		}
	}

	generateRollback := order.Type == 1 && order.Backup == 1
	var firstErr string
	for _, st := range stmts {
		text := strings.TrimSpace(st.Text())

		var preRollback, postRollback []string
		if generateRollback {
			preRollback, postRollback = captureRollback(target, st, order.DataBase)
		}

		res, err := target.Exec(text)
		if err != nil {
			writeRecord(ydb, order.WorkId, text, "执行失败", 0, err.Error())
			if firstErr == "" {
				firstErr = err.Error()
			}
			break // 官方行为：执行失败即中断
		}
		affect := uint(0)
		if res != nil {
			if n, e := res.RowsAffected(); e == nil {
				affect = uint(n)
			}
		}
		writeRecord(ydb, order.WorkId, text, "已执行", affect, "")
		if affect > 0 {
			for _, rb := range append(preRollback, postRollback...) {
				insertRollback(ydb, order.WorkId, rb)
			}
		}
	}

	if firstErr != "" {
		failOrder(ydb, order.WorkId, firstErr)
		*reply = false
		return nil
	}
	succeedOrder(ydb, order.WorkId)
	return nil
}

func writeRecord(ydb *sql.DB, workId, stmt, state string, affect uint, errMsg string) {
	if ydb == nil {
		return
	}
	_, err := ydb.Exec(
		"INSERT INTO `core_sql_records` (`work_id`,`sql`,`state`,`affectrow`,`time`,`error`) VALUES (?,?,?,?,?,?)",
		workId, stmt, state, affect, nowString(), errMsg)
	if err != nil {
		log.Printf("写执行记录失败: %v", err)
	}
}

func insertRollback(ydb *sql.DB, workId, rollbackSQL string) {
	if ydb == nil {
		return
	}
	_, err := ydb.Exec(
		"INSERT INTO `core_rollbacks` (`work_id`,`sql`) VALUES (?,?)",
		workId, rollbackSQL)
	if err != nil {
		log.Printf("写回滚语句失败: %v", err)
	}
}

func updateOrderStatus(ydb *sql.DB, workId string, status int, executeTime string) {
	if ydb == nil {
		return
	}
	var err error
	if executeTime == "" {
		_, err = ydb.Exec("UPDATE `core_sql_orders` SET `status`=? WHERE `work_id`=?", status, workId)
	} else {
		_, err = ydb.Exec(
			"UPDATE `core_sql_orders` SET `status`=?, `execute_time`=? WHERE `work_id`=?",
			status, executeTime, workId)
	}
	if err != nil {
		log.Printf("更新工单状态失败: %v", err)
	}
}

func succeedOrder(ydb *sql.DB, workId string) {
	updateOrderStatus(ydb, workId, orderStatusSuccess, nowString())
}

func failOrder(ydb *sql.DB, workId, reason string) {
	log.Printf("工单 %s 执行失败: %s", workId, reason)
	updateOrderStatus(ydb, workId, orderStatusFailed, nowString())
}

// ==================== 回滚语句生成 ====================

// captureRollback 在执行前捕获回滚语句。
// UPDATE/DELETE：按相同条件查回受影响行，生成反向语句（执行前返回）。
// INSERT：记录自增主键水位，执行后生成按主键范围的 DELETE（post 返回）。
func captureRollback(target *sql.DB, st ast.StmtNode, schema string) (pre, post []string) {
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
			if err := target.QueryRow(
				fmt.Sprintf("SELECT MAX(`%s`) FROM `%s`.`%s`", pk[0], schema, tbl.Name.O),
			).Scan(&maxBefore); err == nil && maxBefore.Valid {
				post = []string{fmt.Sprintf("DELETE FROM `%s` WHERE `%s` > %s;", tbl.Name.O, pk[0], quoteValue([]byte(maxBefore.String)))}
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
		log.Printf("回滚捕获查询失败: %v", err)
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

// quoteValue 将行值转为可执行的字面量：NULL / 数值 / 日期时间 / 十六进制字符串
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
	if looksLikeDatetime(s) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	// 通用安全路径：十六进制字面量（二进制安全）
	return "0x" + fmt.Sprintf("%x", b)
}

var datetimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}( \d{2}:\d{2}(:\d{2})?)?$`)

func looksLikeDatetime(s string) bool { return datetimeRe.MatchString(s) }

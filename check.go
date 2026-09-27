package main

import (
	"database/sql"
	"log"
	"fmt"
	"strings"
	"time"

	"github.com/pingcap/tidb/parser"
	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/format"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/parser/opcode"
	// parser 独立使用必须注册 driver
	_ "github.com/pingcap/tidb/parser/test_driver"
)

type finding struct {
	level uint8
	msg   string
}

// ==================== Engine.Check ====================

func (e *Engine) Check(args *CheckArgs, reply *[]Record) error {
	*reply = checkSQL(args.SQL, args.Schema, args.Kind, args.Lang, args.Rule,
		args.IP, args.Port, args.Username, args.Password)
	return nil
}

func checkSQL(sqlText, schema string, kind int, lang string, rule AuditRole,
	ip string, port int, username, password string) []Record {

	zh := !strings.EqualFold(lang, "en_us")
	T := func(zhMsg, enMsg string) string {
		if zh {
			return zhMsg
		}
		return enMsg
	}

	var target *sql.DB
	if kind == 1 && ip != "" {
		target = openTarget(ip, port, username, password, schema)
		if target == nil {
			log.Printf("[check] 目标库连接失败: %s:%d user=%s schema=%s pw_len=%d", ip, port, username, schema, len(password))
		} else {
			log.Printf("[check] 目标库连接建立: %s:%d user=%s schema=%s", ip, port, username, schema)
		}
		if target != nil {
			defer target.Close()
		}
	}

	stmts, _, err := newParser().Parse(sqlText, "", "")
	if err != nil {
		return []Record{{
			SQL: sqlText, Schema: schema,
			Level: LevelError,
			Error: T("SQL语法错误: ", "SQL syntax error: ") + err.Error(),
		}}
	}

	ddlCount := 0
	for _, st := range stmts {
		if isDDLStmt(st) {
			ddlCount++
		}
	}

	records := make([]Record, 0, len(stmts))
	for _, st := range stmts {
		text := strings.TrimSpace(st.Text())
		tbl, schemaName := extractTableName(st, schema)
		rec := Record{
			SQL: text, Table: tbl, Schema: schemaName,
			AffectRows: estimateAffectRows(st, schema, target),
		}

		var fs []finding
		stmtIsDDL := isDDLStmt(st)

		// 工单类型与语句类型必须匹配（Kind: 0=DDL工单 1=DML工单）
		if (kind == 0 && !stmtIsDDL) || (kind == 1 && stmtIsDDL) {
			orderKind := T("DDL", "DDL")
			if kind == 1 {
				orderKind = T("DML", "DML")
			}
			fs = append(fs, finding{LevelError,
				T(orderKind+"工单不允许包含", orderKind+" order does not allow ") +
					stmtTypeName(st, T) + T(" 语句", " statements")})
		}

		// 多条 DDL 限制
		if stmtIsDDL && ddlCount > 1 && !rule.DDLMultiToCommit {
			fs = append(fs, finding{LevelError,
				T("工单包含多条DDL语句，当前规则只允许单条DDL提交",
					"Order contains multiple DDL statements; only one is allowed")})
		}

		fs = append(fs, checkStatement(st, rule, target, T)...)

		rec.Level = LevelPass
		var msgs []string
		for _, f := range fs {
			msgs = append(msgs, f.msg)
			if f.level < rec.Level {
				rec.Level = f.level
			}
		}
		rec.Error = strings.Join(msgs, "\n")
		switch rec.Level {
		case LevelError:
			rec.Status = T("错误", "ERROR")
		case LevelWarning:
			rec.Status = T("警告", "WARNING")
		default:
			rec.Status = T("通过", "PASS")
		}
		records = append(records, rec)
	}
	return records
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

func stmtTypeName(st ast.StmtNode, T func(string, string) string) string {
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

// ==================== 规则实现 ====================

func checkStatement(st ast.StmtNode, rule AuditRole, target *sql.DB,
	T func(string, string) string) []finding {

	var fs []finding
	switch n := st.(type) {
	case *ast.UpdateStmt:
		fs = checkWhere(n.Where, "UPDATE", rule, T)
		if n.Limit != nil && !rule.DMLAllowLimitSTMT {
			fs = append(fs, finding{LevelError,
				T("UPDATE语句不允许使用limit关键字", "UPDATE does not allow LIMIT")})
		}
		if n.Order != nil && rule.DMLOrder {
			fs = append(fs, finding{LevelWarning,
				T("UPDATE语句不建议使用order by", "UPDATE should not use ORDER BY")})
		}
	case *ast.DeleteStmt:
		fs = checkWhere(n.Where, "DELETE", rule, T)
		if n.Limit != nil && !rule.DMLAllowLimitSTMT {
			fs = append(fs, finding{LevelError,
				T("DELETE语句不允许使用limit关键字", "DELETE does not allow LIMIT")})
		}
		if n.Order != nil && rule.DMLOrder {
			fs = append(fs, finding{LevelWarning,
				T("DELETE语句不建议使用order by", "DELETE should not use ORDER BY")})
		}
	case *ast.InsertStmt:
		fs = checkInsert(n, rule, T)
	case *ast.SelectStmt, *ast.SetOprStmt:
		if rule.DMLSelect {
			fs = append(fs, finding{LevelError,
				T("DML工单不允许包含SELECT查询语句", "DML order does not allow SELECT")})
		}
	case *ast.CreateTableStmt:
		fs = checkCreateTable(n, rule, T)
	case *ast.AlterTableStmt:
		fs = checkAlterTable(n, rule, T)
	case *ast.DropTableStmt:
		if !rule.DDLEnableDropTable {
			fs = append(fs, finding{LevelError,
				T("drop table语句已被当前审核规则禁止执行", "DROP TABLE is disabled by rule")})
		}
	case *ast.TruncateTableStmt:
		if !rule.DDLEnableDropTable {
			fs = append(fs, finding{LevelError,
				T("truncate语句等同于drop重建，已被当前审核规则禁止", "TRUNCATE is disabled by rule")})
		}
	case *ast.DropDatabaseStmt:
		if !rule.DDLEnableDropDatabase {
			fs = append(fs, finding{LevelError,
				T("drop database语句已被当前审核规则禁止执行", "DROP DATABASE is disabled by rule")})
		}
	case *ast.CreateViewStmt:
		if !rule.AllowCreateView {
			fs = append(fs, finding{LevelError,
				T("当前规则不允许创建视图", "CREATE VIEW is disabled by rule")})
		}
	}
	return fs
}

func checkWhere(where ast.ExprNode, verb string, rule AuditRole,
	T func(string, string) string) []finding {

	var fs []finding
	if where == nil {
		if rule.DMLWhere {
			fs = append(fs, finding{LevelError,
				T(verb+"语句必须携带where条件", verb+" statement requires a WHERE clause")})
		}
		return fs
	}
	if rule.DMLWhereExprValueIsNull && hasNullComparison(where) {
		fs = append(fs, finding{LevelWarning,
			T("where条件中存在与null比较的判断（应使用 is null / is not null）",
				"WHERE contains a comparison with NULL")})
	}
	return fs
}

func hasNullComparison(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.BinaryOperationExpr:
		if (v.Op == opcode.EQ || v.Op == opcode.NE) && isNullLiteral(v.R) {
			return true
		}
		return hasNullComparison(v.L) || hasNullComparison(v.R)
	case *ast.ParenthesesExpr:
		return hasNullComparison(v.Expr)
	case *ast.PatternLikeOrIlikeExpr:
		return hasNullComparison(v.Expr) || hasNullComparison(v.Pattern)
	}
	return false
}

func isNullLiteral(n ast.Node) bool {
	if v, ok := n.(ast.ValueExpr); ok {
		return v.GetValue() == nil
	}
	return false
}

func checkInsert(n *ast.InsertStmt, rule AuditRole,
	T func(string, string) string) []finding {

	var fs []finding
	if len(n.Columns) == 0 && rule.DMLInsertColumns {
		fs = append(fs, finding{LevelError,
			T("insert语句必须显式声明列名", "INSERT must declare columns explicitly")})
	}
	rows := len(n.Lists)
	if rule.DMLMaxInsertRows > 0 && rows > rule.DMLMaxInsertRows {
		fs = append(fs, finding{LevelError,
			T(fmt.Sprintf("insert语句单次插入行数(%d)超过最大限制(%d)", rows, rule.DMLMaxInsertRows),
				fmt.Sprintf("INSERT rows(%d) exceed max(%d)", rows, rule.DMLMaxInsertRows))})
	}
	if !rule.DMLAllowInsertNull {
	nullLoop:
		for _, row := range n.Lists {
			for _, col := range row {
				if isNullLiteral(col) {
					fs = append(fs, finding{LevelWarning,
						T("insert语句包含null值插入", "INSERT contains NULL value")})
					break nullLoop
				}
			}
		}
	}
	return fs
}

func checkCreateTable(n *ast.CreateTableStmt, rule AuditRole,
	T func(string, string) string) []finding {

	var fs []finding
	tableName := n.Table.Name.O

	if rule.MaxTableNameLen > 0 && len([]rune(tableName)) > rule.MaxTableNameLen {
		fs = append(fs, finding{LevelError,
			T(fmt.Sprintf("表名(%s)长度超过最大限制(%d)", tableName, rule.MaxTableNameLen),
				fmt.Sprintf("table name(%s) longer than max(%d)", tableName, rule.MaxTableNameLen))})
	}
	if rule.DDLTablePrefix != "" && !strings.HasPrefix(tableName, rule.DDLTablePrefix) {
		fs = append(fs, finding{LevelError,
			T(fmt.Sprintf("表名(%s)必须使用前缀(%s)", tableName, rule.DDLTablePrefix),
				fmt.Sprintf("table(%s) must have prefix(%s)", tableName, rule.DDLTablePrefix))})
	}
	if rule.CheckIdentifier && isReservedWord(tableName) {
		fs = append(fs, finding{LevelError,
			T(fmt.Sprintf("表名(%s)使用了保留关键字", tableName),
				fmt.Sprintf("table name(%s) is a reserved word", tableName))})
	}

	hasPK := false
	for _, ct := range n.Constraints {
		switch ct.Tp {
		case ast.ConstraintPrimaryKey:
			hasPK = true
		case ast.ConstraintForeignKey:
			if !rule.DDLEnableForeignKey {
				fs = append(fs, finding{LevelError,
					T("当前规则不允许使用外键", "FOREIGN KEY is disabled by rule")})
			}
		}
	}

	var colNames []string
	for _, col := range n.Cols {
		colName := col.Name.Name.O
		colNames = append(colNames, colName)
		var hasNotNull, hasDefault, hasComment, isAutoInc bool
		for _, opt := range col.Options {
			switch opt.Tp {
			case ast.ColumnOptionPrimaryKey:
				hasPK = true
			case ast.ColumnOptionNotNull:
				hasNotNull = true
			case ast.ColumnOptionDefaultValue:
				hasDefault = true
			case ast.ColumnOptionComment:
				hasComment = true
			case ast.ColumnOptionAutoIncrement:
				isAutoInc = true
			}
		}
		isUnsigned := col.Tp != nil && mysql.HasUnsignedFlag(col.Tp.GetFlag())

		if rule.CheckIdentifier && isReservedWord(colName) {
			fs = append(fs, finding{LevelError,
				T(fmt.Sprintf("字段名(%s)使用了保留关键字", colName),
					fmt.Sprintf("column(%s) is a reserved word", colName))})
		}
		if rule.DDLPrimaryKeyMust && hasPK && !strings.EqualFold(colName, "id") {
			fs = append(fs, finding{LevelWarning,
				T("主键名必须为id", "primary key must be named id")})
		}
		if rule.DDlCheckColumnComment && !hasComment {
			fs = append(fs, finding{LevelWarning,
				T(fmt.Sprintf("字段(%s)缺少注释", colName),
					fmt.Sprintf("column(%s) lacks comment", colName))})
		}
		if rule.DDLCheckColumnNullable && !hasNotNull && !isAutoInc && !hasPK {
			fs = append(fs, finding{LevelWarning,
				T(fmt.Sprintf("字段(%s)建议设置为NOT NULL", colName),
					fmt.Sprintf("column(%s) should be NOT NULL", colName))})
		}
		if rule.DDLCheckColumnDefault && !hasDefault && !isAutoInc {
			fs = append(fs, finding{LevelWarning,
				T(fmt.Sprintf("字段(%s)缺少默认值", colName),
					fmt.Sprintf("column(%s) lacks default value", colName))})
		}
		if rule.DDLMaxCharLength > 0 && col.Tp != nil {
			switch col.Tp.GetType() {
			case mysql.TypeVarchar, mysql.TypeString:
				if col.Tp.GetFlen() > int(rule.DDLMaxCharLength) {
					fs = append(fs, finding{LevelWarning,
						T(fmt.Sprintf("字段(%s)长度(%d)超过最大限制(%d)", colName, col.Tp.GetFlen(), rule.DDLMaxCharLength),
							fmt.Sprintf("column(%s) length(%d) exceeds max(%d)", colName, col.Tp.GetFlen(), rule.DDLMaxCharLength))})
				}
			}
		}
		if rule.DDLCheckFloatDouble && col.Tp != nil {
			switch col.Tp.GetType() {
			case mysql.TypeFloat, mysql.TypeDouble:
				fs = append(fs, finding{LevelWarning,
					T(fmt.Sprintf("字段(%s)为float/double类型，建议使用decimal", colName),
						fmt.Sprintf("column(%s) float/double should be decimal", colName))})
			}
		}
		if isAutoInc && !isUnsigned && rule.DDLEnableAutoincrementUnsigned {
			fs = append(fs, finding{LevelWarning,
				T(fmt.Sprintf("自增字段(%s)建议添加unsigned标志", colName),
					fmt.Sprintf("auto increment column(%s) should be unsigned", colName))})
		}
	}

	if !hasPK && rule.DDLEnablePrimaryKey {
		fs = append(fs, finding{LevelError,
			T("表必须包含主键", "table must have a primary key")})
	}

	if rule.MustHaveColumns != "" {
		for _, must := range strings.Split(rule.MustHaveColumns, ",") {
			must = strings.TrimSpace(must)
			if must == "" {
				continue
			}
			found := false
			for _, c := range colNames {
				if strings.EqualFold(c, must) {
					found = true
					break
				}
			}
			if !found {
				fs = append(fs, finding{LevelError,
					T(fmt.Sprintf("建表缺少必须字段(%s)", must),
						fmt.Sprintf("missing required column(%s)", must))})
			}
		}
	}

	var tableComment string
	indexCount := 0
	for _, opt := range n.Options {
		switch opt.Tp {
		case ast.TableOptionComment:
			tableComment = opt.StrValue
		case ast.TableOptionCharset:
			if rule.SupportCharset != "" && !containsToken(rule.SupportCharset, opt.StrValue) {
				fs = append(fs, finding{LevelError,
					T(fmt.Sprintf("字符集(%s)不在允许范围(%s)", opt.StrValue, rule.SupportCharset),
						fmt.Sprintf("charset(%s) not allowed(%s)", opt.StrValue, rule.SupportCharset))})
			}
		case ast.TableOptionCollate:
			if rule.SupportCollation != "" && !containsToken(rule.SupportCollation, opt.StrValue) {
				fs = append(fs, finding{LevelError,
					T(fmt.Sprintf("排序规则(%s)不在允许范围(%s)", opt.StrValue, rule.SupportCollation),
						fmt.Sprintf("collation(%s) not allowed(%s)", opt.StrValue, rule.SupportCollation))})
			}
		}
	}
	if rule.DDLCheckTableComment && tableComment == "" {
		fs = append(fs, finding{LevelWarning,
			T("表缺少注释", "table lacks comment")})
	}
	for _, ct := range n.Constraints {
		switch ct.Tp {
		case ast.ConstraintIndex, ast.ConstraintUniq, ast.ConstraintUniqIndex, ast.ConstraintUniqKey, ast.ConstraintKey:
			indexCount++
		}
	}
	if rule.DDLMaxKey > 0 && indexCount > int(rule.DDLMaxKey) {
		fs = append(fs, finding{LevelError,
			T(fmt.Sprintf("索引数量(%d)超过单表最大限制(%d)", indexCount, rule.DDLMaxKey),
				fmt.Sprintf("index count(%d) exceeds max(%d)", indexCount, rule.DDLMaxKey))})
	}
	for _, ct := range n.Constraints {
		if rule.DDLMaxKeyParts > 0 && len(ct.Keys) > int(rule.DDLMaxKeyParts) {
			fs = append(fs, finding{LevelError,
				T(fmt.Sprintf("单个索引字段数(%d)超过最大限制(%d)", len(ct.Keys), rule.DDLMaxKeyParts),
					fmt.Sprintf("index parts(%d) exceeds max(%d)", len(ct.Keys), rule.DDLMaxKeyParts))})
		}
		if ct.Name == "" && !rule.DDLEnableNullIndexName {
			fs = append(fs, finding{LevelError,
				T("索引名称不能为空", "index name must not be empty")})
		}
	}
	if n.Partition != nil && !rule.AllowCreatePartition {
		fs = append(fs, finding{LevelError,
			T("当前规则不允许创建分区表", "partitioned table is disabled by rule")})
	}
	return fs
}

func checkAlterTable(n *ast.AlterTableStmt, rule AuditRole,
	T func(string, string) string) []finding {

	var fs []finding
	if len(n.Specs) > 1 && !rule.DDLAllowMultiAlter {
		fs = append(fs, finding{LevelError,
			T("单条alter语句包含多个变更操作，当前规则不允许",
				"single ALTER with multiple specifications is disabled")})
	}
	for _, spec := range n.Specs {
		switch spec.Tp {
		case ast.AlterTableModifyColumn, ast.AlterTableChangeColumn:
			if !rule.DDLAllowColumnType {
				fs = append(fs, finding{LevelError,
					T("当前规则不允许修改字段类型", "column type modification is disabled")})
			}
		case ast.AlterTableAddConstraint:
			if spec.Constraint != nil && spec.Constraint.Tp == ast.ConstraintForeignKey && !rule.DDLEnableForeignKey {
				fs = append(fs, finding{LevelError,
					T("当前规则不允许使用外键", "FOREIGN KEY is disabled by rule")})
			}
			if spec.Constraint != nil && rule.DDLMaxKeyParts > 0 && len(spec.Constraint.Keys) > int(rule.DDLMaxKeyParts) {
				fs = append(fs, finding{LevelError,
					T(fmt.Sprintf("单个索引字段数(%d)超过最大限制(%d)", len(spec.Constraint.Keys), rule.DDLMaxKeyParts),
						fmt.Sprintf("index parts(%d) exceeds max(%d)", len(spec.Constraint.Keys), rule.DDLMaxKeyParts))})
			}
		case ast.AlterTableOption:
			for _, opt := range spec.Options {
				if opt.Tp == ast.TableOptionCharset && rule.SupportCharset != "" && !containsToken(rule.SupportCharset, opt.StrValue) {
					fs = append(fs, finding{LevelError,
						T(fmt.Sprintf("字符集(%s)不在允许范围(%s)", opt.StrValue, rule.SupportCharset),
							fmt.Sprintf("charset(%s) not allowed(%s)", opt.StrValue, rule.SupportCharset))})
				}
			}
		}
		if spec.Position != nil && rule.DDLAllowChangeColumnPosition {
			fs = append(fs, finding{LevelWarning,
				T("不建议使用after/first调整字段位置", "AFTER/FIRST positioning is discouraged")})
		}
	}
	return fs
}

func containsToken(list, v string) bool {
	for _, t := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(t), v) {
			return true
		}
	}
	return false
}

var reservedWords = map[string]bool{
	"order": true, "group": true, "table": true, "index": true, "key": true,
	"user": true, "desc": true, "asc": true, "range": true, "select": true,
	"insert": true, "update": true, "delete": true, "where": true, "from": true,
	"join": true, "left": true, "right": true, "on": true, "and": true, "or": true,
	"not": true, "null": true, "default": true, "primary": true, "unique": true,
	"check": true, "references": true, "create": true, "drop": true, "alter": true,
}

func isReservedWord(name string) bool {
	return reservedWords[strings.ToLower(name)]
}

// ==================== 影响行数估算（连接目标库 COUNT） ====================
// 注：MySQL 8/26 新版 EXPLAIN 对 UPDATE/DELETE 输出 iterator 树形计划、无 rows
// 列，故对单表 UPDATE/DELETE 直接用同条件 COUNT(*) 估算（与实际影响行数一致）。

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
		log.Printf("[check] 影响行数估算失败: %s → %v", q, err)
		return 0
	}
	return uint(n)
}

// ==================== Engine.Query ====================

func (e *Engine) Query(args *QueryArgs, reply *[]Record) error {
	var out []Record
	list := parseInsulateWords(args.InsulateWordList)

	stmts, _, err := newParser().Parse(args.SQL, "", "")
	if err != nil {
		out = append(out, Record{SQL: args.SQL, Level: LevelError, Error: "SQL语法错误: " + err.Error()})
		*reply = out
		return nil
	}
	for _, st := range stmts {
		text := strings.TrimSpace(st.Text())
		switch st.(type) {
		case *ast.SelectStmt, *ast.SetOprStmt:
			if _, hasLimit := selectHasLimit(st); !hasLimit && args.Limit > 0 {
				text = fmt.Sprintf("%s LIMIT %d", strings.TrimSuffix(text, ";"), args.Limit)
			}
			out = append(out, Record{SQL: text, Level: LevelPass, Status: "通过", InsulateWordList: list})
		default:
			out = append(out, Record{SQL: text, Level: LevelError, Error: "查询工单仅允许SELECT语句"})
		}
	}
	*reply = out
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
		if err := jsonUnmarshal(raw, &list); err == nil {
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

// ==================== Engine.MergeAlterTables ====================

func (e *Engine) MergeAlterTables(sqls string, reply *string) error {
	stmts, _, err := newParser().Parse(sqls, "", "")
	if err != nil {
		return err
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
			if s := restoreNode(spec); s != "" {
				g.specs = append(g.specs, s)
			}
		}
	}
	var merged []string
	for _, tbl := range order {
		g := groups[tbl]
		merged = append(merged, fmt.Sprintf("ALTER TABLE %s %s;", tbl, strings.Join(g.specs, ", ")))
	}
	*reply = strings.Join(merged, "\n")
	return nil
}

// ==================== Engine.StopDelay ====================

func (e *Engine) StopDelay(args *Confirm, reply *string) error {
	*reply = "ok"
	return nil
}

// ==================== 工具 ====================

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

func openTarget(ip string, port int, user, pass, schema string) *sql.DB {
	db, err := sql.Open("mysql", buildDSN(ip, port, user, pass, schema))
	if err != nil {
		return nil
	}
	db.SetConnMaxIdleTime(30 * time.Second)
	db.SetMaxOpenConns(2)
	return db
}

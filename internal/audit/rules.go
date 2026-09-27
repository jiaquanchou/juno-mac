package audit

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/parser/opcode"

	"github.com/jiaquanchou/juno-mac/internal/protocol"
)

// 文案函数：Lang != en_us 时输出中文。
type translate func(zhMsg, enMsg string) string

func translator(lang string) translate {
	zh := !strings.EqualFold(lang, "en_us")
	return func(zhMsg, enMsg string) string {
		if zh {
			return zhMsg
		}
		return enMsg
	}
}

type finding struct {
	level uint8
	msg   string
}

// checkStatement 按规则逐语句检查（不负责连接目标库，行数估算由调用方传入连接）。
func checkStatement(st ast.StmtNode, rule protocol.AuditRole, target *sql.DB,
	T translate) []finding {

	var fs []finding
	switch n := st.(type) {
	case *ast.UpdateStmt:
		fs = checkWhere(n.Where, "UPDATE", rule, T)
		if n.Limit != nil && !rule.DMLAllowLimitSTMT {
			fs = append(fs, finding{protocol.LevelError,
				T("UPDATE语句不允许使用limit关键字", "UPDATE does not allow LIMIT")})
		}
		if n.Order != nil && rule.DMLOrder {
			fs = append(fs, finding{protocol.LevelWarning,
				T("UPDATE语句不建议使用order by", "UPDATE should not use ORDER BY")})
		}
	case *ast.DeleteStmt:
		fs = checkWhere(n.Where, "DELETE", rule, T)
		if n.Limit != nil && !rule.DMLAllowLimitSTMT {
			fs = append(fs, finding{protocol.LevelError,
				T("DELETE语句不允许使用limit关键字", "DELETE does not allow LIMIT")})
		}
		if n.Order != nil && rule.DMLOrder {
			fs = append(fs, finding{protocol.LevelWarning,
				T("DELETE语句不建议使用order by", "DELETE should not use ORDER BY")})
		}
	case *ast.InsertStmt:
		fs = checkInsert(n, rule, T)
	case *ast.SelectStmt, *ast.SetOprStmt:
		if rule.DMLSelect {
			fs = append(fs, finding{protocol.LevelError,
				T("DML工单不允许包含SELECT查询语句", "DML order does not allow SELECT")})
		}
	case *ast.CreateTableStmt:
		fs = checkCreateTable(n, rule, T)
	case *ast.AlterTableStmt:
		fs = checkAlterTable(n, rule, T)
	case *ast.DropTableStmt:
		if !rule.DDLEnableDropTable {
			fs = append(fs, finding{protocol.LevelError,
				T("drop table语句已被当前审核规则禁止执行", "DROP TABLE is disabled by rule")})
		}
	case *ast.TruncateTableStmt:
		if !rule.DDLEnableDropTable {
			fs = append(fs, finding{protocol.LevelError,
				T("truncate语句等同于drop重建，已被当前审核规则禁止", "TRUNCATE is disabled by rule")})
		}
	case *ast.DropDatabaseStmt:
		if !rule.DDLEnableDropDatabase {
			fs = append(fs, finding{protocol.LevelError,
				T("drop database语句已被当前审核规则禁止执行", "DROP DATABASE is disabled by rule")})
		}
	case *ast.CreateViewStmt:
		if !rule.AllowCreateView {
			fs = append(fs, finding{protocol.LevelError,
				T("当前规则不允许创建视图", "CREATE VIEW is disabled by rule")})
		}
	}
	return fs
}

func checkWhere(where ast.ExprNode, verb string, rule protocol.AuditRole,
	T translate) []finding {

	var fs []finding
	if where == nil {
		if rule.DMLWhere {
			fs = append(fs, finding{protocol.LevelError,
				T(verb+"语句必须携带where条件", verb+" statement requires a WHERE clause")})
		}
		return fs
	}
	if rule.DMLWhereExprValueIsNull && hasNullComparison(where) {
		fs = append(fs, finding{protocol.LevelWarning,
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

func checkInsert(n *ast.InsertStmt, rule protocol.AuditRole, T translate) []finding {
	var fs []finding
	if len(n.Columns) == 0 && rule.DMLInsertColumns {
		fs = append(fs, finding{protocol.LevelError,
			T("insert语句必须显式声明列名", "INSERT must declare columns explicitly")})
	}
	rows := len(n.Lists)
	if rule.DMLMaxInsertRows > 0 && rows > rule.DMLMaxInsertRows {
		fs = append(fs, finding{protocol.LevelError,
			T(fmt.Sprintf("insert语句单次插入行数(%d)超过最大限制(%d)", rows, rule.DMLMaxInsertRows),
				fmt.Sprintf("INSERT rows(%d) exceed max(%d)", rows, rule.DMLMaxInsertRows))})
	}
	if !rule.DMLAllowInsertNull {
	nullLoop:
		for _, row := range n.Lists {
			for _, col := range row {
				if isNullLiteral(col) {
					fs = append(fs, finding{protocol.LevelWarning,
						T("insert语句包含null值插入", "INSERT contains NULL value")})
					break nullLoop
				}
			}
		}
	}
	return fs
}

func checkCreateTable(n *ast.CreateTableStmt, rule protocol.AuditRole, T translate) []finding {
	var fs []finding
	tableName := n.Table.Name.O

	if rule.MaxTableNameLen > 0 && len([]rune(tableName)) > rule.MaxTableNameLen {
		fs = append(fs, finding{protocol.LevelError,
			T(fmt.Sprintf("表名(%s)长度超过最大限制(%d)", tableName, rule.MaxTableNameLen),
				fmt.Sprintf("table name(%s) longer than max(%d)", tableName, rule.MaxTableNameLen))})
	}
	if rule.DDLTablePrefix != "" && !strings.HasPrefix(tableName, rule.DDLTablePrefix) {
		fs = append(fs, finding{protocol.LevelError,
			T(fmt.Sprintf("表名(%s)必须使用前缀(%s)", tableName, rule.DDLTablePrefix),
				fmt.Sprintf("table(%s) must have prefix(%s)", tableName, rule.DDLTablePrefix))})
	}
	if rule.CheckIdentifier && isReservedWord(tableName) {
		fs = append(fs, finding{protocol.LevelError,
			T(fmt.Sprintf("表名(%s)使用了保留关键字", tableName),
				fmt.Sprintf("table name(%s) is a reserved word", tableName))})
	}

	// 主键集合：约束级 + 列级
	pkCols := map[string]bool{}
	for _, ct := range n.Constraints {
		switch ct.Tp {
		case ast.ConstraintPrimaryKey:
			for _, k := range ct.Keys {
				pkCols[k.Column.Name.L] = true
			}
		case ast.ConstraintForeignKey:
			if !rule.DDLEnableForeignKey {
				fs = append(fs, finding{protocol.LevelError,
					T("当前规则不允许使用外键", "FOREIGN KEY is disabled by rule")})
			}
		}
	}

	var colNames []string
	for _, col := range n.Cols {
		colName := col.Name.Name.O
		colNames = append(colNames, colName)
		var hasNotNull, hasDefault, hasComment, isAutoInc, colIsPK bool
		for _, opt := range col.Options {
			switch opt.Tp {
			case ast.ColumnOptionPrimaryKey:
				colIsPK = true
				pkCols[col.Name.Name.L] = true
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
			fs = append(fs, finding{protocol.LevelError,
				T(fmt.Sprintf("字段名(%s)使用了保留关键字", colName),
					fmt.Sprintf("column(%s) is a reserved word", colName))})
		}
		if rule.DDLPrimaryKeyMust && colIsPK && !strings.EqualFold(colName, "id") {
			fs = append(fs, finding{protocol.LevelWarning,
				T("主键名必须为id", "primary key must be named id")})
		}
		if rule.DDlCheckColumnComment && !hasComment {
			fs = append(fs, finding{protocol.LevelWarning,
				T(fmt.Sprintf("字段(%s)缺少注释", colName),
					fmt.Sprintf("column(%s) lacks comment", colName))})
		}
		if rule.DDLCheckColumnNullable && !hasNotNull && !isAutoInc && !colIsPK {
			fs = append(fs, finding{protocol.LevelWarning,
				T(fmt.Sprintf("字段(%s)建议设置为NOT NULL", colName),
					fmt.Sprintf("column(%s) should be NOT NULL", colName))})
		}
		if rule.DDLCheckColumnDefault && !hasDefault && !isAutoInc {
			fs = append(fs, finding{protocol.LevelWarning,
				T(fmt.Sprintf("字段(%s)缺少默认值", colName),
					fmt.Sprintf("column(%s) lacks default value", colName))})
		}
		if rule.DDLMaxCharLength > 0 && col.Tp != nil {
			switch col.Tp.GetType() {
			case mysql.TypeVarchar, mysql.TypeString:
				if col.Tp.GetFlen() > int(rule.DDLMaxCharLength) {
					fs = append(fs, finding{protocol.LevelWarning,
						T(fmt.Sprintf("字段(%s)长度(%d)超过最大限制(%d)", colName, col.Tp.GetFlen(), rule.DDLMaxCharLength),
							fmt.Sprintf("column(%s) length(%d) exceeds max(%d)", colName, col.Tp.GetFlen(), rule.DDLMaxCharLength))})
				}
			}
		}
		if rule.DDLCheckFloatDouble && col.Tp != nil {
			switch col.Tp.GetType() {
			case mysql.TypeFloat, mysql.TypeDouble:
				fs = append(fs, finding{protocol.LevelWarning,
					T(fmt.Sprintf("字段(%s)为float/double类型，建议使用decimal", colName),
						fmt.Sprintf("column(%s) float/double should be decimal", colName))})
			}
		}
		if isAutoInc && !isUnsigned && rule.DDLEnableAutoincrementUnsigned {
			fs = append(fs, finding{protocol.LevelWarning,
				T(fmt.Sprintf("自增字段(%s)建议添加unsigned标志", colName),
					fmt.Sprintf("auto increment column(%s) should be unsigned", colName))})
		}
	}

	hasPK := len(pkCols) > 0
	if !hasPK && rule.DDLEnablePrimaryKey {
		fs = append(fs, finding{protocol.LevelError,
			T("表必须包含主键", "table must have a primary key")})
	}
	if hasPK && rule.DDLEnableAutoIncrement {
		pkAutoInc := false
		for _, col := range n.Cols {
			if pkCols[col.Name.Name.L] {
				for _, opt := range col.Options {
					if opt.Tp == ast.ColumnOptionAutoIncrement {
						pkAutoInc = true
					}
				}
			}
		}
		if !pkAutoInc {
			fs = append(fs, finding{protocol.LevelWarning,
				T("主键建议使用AUTO_INCREMENT自增列", "primary key should be AUTO_INCREMENT")})
		}
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
				fs = append(fs, finding{protocol.LevelError,
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
				fs = append(fs, finding{protocol.LevelError,
					T(fmt.Sprintf("字符集(%s)不在允许范围(%s)", opt.StrValue, rule.SupportCharset),
						fmt.Sprintf("charset(%s) not allowed(%s)", opt.StrValue, rule.SupportCharset))})
			}
		case ast.TableOptionCollate:
			if rule.SupportCollation != "" && !containsToken(rule.SupportCollation, opt.StrValue) {
				fs = append(fs, finding{protocol.LevelError,
					T(fmt.Sprintf("排序规则(%s)不在允许范围(%s)", opt.StrValue, rule.SupportCollation),
						fmt.Sprintf("collation(%s) not allowed(%s)", opt.StrValue, rule.SupportCollation))})
			}
		}
	}
	if rule.DDLCheckTableComment && tableComment == "" {
		fs = append(fs, finding{protocol.LevelWarning,
			T("表缺少注释", "table lacks comment")})
	}
	for _, ct := range n.Constraints {
		switch ct.Tp {
		case ast.ConstraintIndex, ast.ConstraintUniq, ast.ConstraintUniqIndex, ast.ConstraintUniqKey, ast.ConstraintKey:
			indexCount++
		}
	}
	if rule.DDLMaxKey > 0 && indexCount > int(rule.DDLMaxKey) {
		fs = append(fs, finding{protocol.LevelError,
			T(fmt.Sprintf("索引数量(%d)超过单表最大限制(%d)", indexCount, rule.DDLMaxKey),
				fmt.Sprintf("index count(%d) exceeds max(%d)", indexCount, rule.DDLMaxKey))})
	}
	for _, ct := range n.Constraints {
		if rule.DDLMaxKeyParts > 0 && len(ct.Keys) > int(rule.DDLMaxKeyParts) {
			fs = append(fs, finding{protocol.LevelError,
				T(fmt.Sprintf("单个索引字段数(%d)超过最大限制(%d)", len(ct.Keys), rule.DDLMaxKeyParts),
					fmt.Sprintf("index parts(%d) exceeds max(%d)", len(ct.Keys), rule.DDLMaxKeyParts))})
		}
		if ct.Name == "" && !rule.DDLEnableNullIndexName {
			fs = append(fs, finding{protocol.LevelError,
				T("索引名称不能为空", "index name must not be empty")})
		}
	}
	if n.Partition != nil && !rule.AllowCreatePartition {
		fs = append(fs, finding{protocol.LevelError,
			T("当前规则不允许创建分区表", "partitioned table is disabled by rule")})
	}
	return fs
}

func checkAlterTable(n *ast.AlterTableStmt, rule protocol.AuditRole, T translate) []finding {
	var fs []finding
	if len(n.Specs) > 1 && !rule.DDLAllowMultiAlter {
		fs = append(fs, finding{protocol.LevelError,
			T("单条alter语句包含多个变更操作，当前规则不允许",
				"single ALTER with multiple specifications is disabled")})
	}
	for _, spec := range n.Specs {
		switch spec.Tp {
		case ast.AlterTableModifyColumn, ast.AlterTableChangeColumn:
			if !rule.DDLAllowColumnType {
				fs = append(fs, finding{protocol.LevelError,
					T("当前规则不允许修改字段类型", "column type modification is disabled")})
			}
		case ast.AlterTableAddConstraint:
			if spec.Constraint != nil && spec.Constraint.Tp == ast.ConstraintForeignKey && !rule.DDLEnableForeignKey {
				fs = append(fs, finding{protocol.LevelError,
					T("当前规则不允许使用外键", "FOREIGN KEY is disabled by rule")})
			}
			if spec.Constraint != nil && rule.DDLMaxKeyParts > 0 && len(spec.Constraint.Keys) > int(rule.DDLMaxKeyParts) {
				fs = append(fs, finding{protocol.LevelError,
					T(fmt.Sprintf("单个索引字段数(%d)超过最大限制(%d)", len(spec.Constraint.Keys), rule.DDLMaxKeyParts),
						fmt.Sprintf("index parts(%d) exceeds max(%d)", len(spec.Constraint.Keys), rule.DDLMaxKeyParts))})
			}
		case ast.AlterTableOption:
			for _, opt := range spec.Options {
				if opt.Tp == ast.TableOptionCharset && rule.SupportCharset != "" && !containsToken(rule.SupportCharset, opt.StrValue) {
					fs = append(fs, finding{protocol.LevelError,
						T(fmt.Sprintf("字符集(%s)不在允许范围(%s)", opt.StrValue, rule.SupportCharset),
							fmt.Sprintf("charset(%s) not allowed(%s)", opt.StrValue, rule.SupportCharset))})
				}
			}
		}
		if spec.Position != nil && rule.DDLAllowChangeColumnPosition {
			fs = append(fs, finding{protocol.LevelWarning,
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

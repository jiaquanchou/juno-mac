// gen-rules-matrix 从 internal/protocol 的 AuditRole 结构体生成 README（中/英）
// 的「规则支持矩阵」，并可用 --check 校验 README 是否与代码同步。
//
// 规则的实现状态在本文件的 ruleStatus 中维护（这是唯一事实来源）：
// 新增 AuditRole 字段而未在 ruleStatus 登记时，本工具会报错，防止矩阵漏项。
//
// 用法：
//
//	go run ./tools/gen-rules-matrix            # 重新生成 README 中的矩阵段
//	go run ./tools/gen-rules-matrix --check    # 仅校验，不一致时退出码 1（CI 用）
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

const (
	structPath   = "internal/protocol/types.go"
	structName   = "AuditRole"
	markerFmt    = "<!-- rules-matrix:%s:start -->"
	markerEndFmt = "<!-- rules-matrix:%s:end -->"
	readmeZH     = "README.md"
	readmeEN     = "README_EN.md"
)

// status 描述单个规则字段的实现状态与中英文说明。
type status struct {
	state string // "yes" | "partial" | "no"
	zh    string
	en    string
}

var icons = map[string]string{"yes": "✅", "partial": "⚠️", "no": "❌"}

// ruleStatus 是规则支持矩阵的唯一事实来源。
var ruleStatus = map[string]status{
	"DMLTransaction":                 {"no", "事务化执行未实现（当前逐条自动提交）", "Transactional execution not implemented (autocommit per statement)"},
	"DMLAllowLimitSTMT":              {"yes", "是否允许 DML 使用 LIMIT", "Allow LIMIT in DML"},
	"DMLInsertColumns":               {"yes", "INSERT 必须显式声明列名", "INSERT must declare columns"},
	"DMLMaxInsertRows":               {"yes", "单条 INSERT 最大行数", "Max rows per INSERT"},
	"DMLWhere":                       {"yes", "UPDATE/DELETE 必须携带 WHERE（官方默认关闭）", "UPDATE/DELETE require a WHERE clause (off by default in Yearning)"},
	"DMLWhereExprValueIsNull":        {"yes", "WHERE 与 NULL 比较告警", "Warn on NULL comparisons in WHERE"},
	"DMLOrder":                       {"yes", "DML 中的 ORDER BY 告警", "Warn on ORDER BY in DML"},
	"DMLSelect":                      {"yes", "DML 工单禁含 SELECT", "Forbid SELECT inside DML orders"},
	"DMLAllowInsertNull":             {"yes", "INSERT 含 NULL 告警", "Warn on NULL inserts"},
	"DMLInsertMustExplicitly":        {"partial", "与 DMLInsertColumns 同路径", "Same code path as DMLInsertColumns"},
	"DDLEnablePrimaryKey":            {"yes", "表必须有主键", "Table must have a primary key"},
	"DDLCheckTableComment":           {"yes", "表注释告警", "Warn when table comment missing"},
	"DDlCheckColumnComment":          {"yes", "列注释告警", "Warn when column comment missing"},
	"DDLCheckColumnNullable":         {"yes", "NOT NULL 建议", "Suggest NOT NULL"},
	"DDLCheckColumnDefault":          {"yes", "默认值告警", "Warn when default value missing"},
	"DDLEnableAcrossDBRename":        {"no", "跨库表迁移", "Cross-database rename"},
	"DDLEnableAutoincrementInit":     {"no", "自增初始值", "AUTO_INCREMENT initial value"},
	"DDLEnableAutoIncrement":         {"yes", "主键建议自增", "Warn when PK is not AUTO_INCREMENT"},
	"DDLEnableAutoincrementUnsigned": {"yes", "自增列建议 unsigned", "Warn on signed auto-increment columns"},
	"DDLEnableDropTable":             {"yes", "DROP/TRUNCATE 禁用", "Forbid DROP/TRUNCATE"},
	"DDLEnableDropDatabase":          {"yes", "DROP DATABASE 禁用", "Forbid DROP DATABASE"},
	"DDLEnableNullIndexName":         {"yes", "空索引名检查", "Forbid empty index names"},
	"DDLIndexNameSpec":               {"no", "索引命名规范", "Index naming convention"},
	"DDLMaxKeyParts":                 {"yes", "单个索引字段数上限", "Index parts limit"},
	"DDLMaxKey":                      {"yes", "索引数量上限", "Index count limit"},
	"DDLMaxCharLength":               {"yes", "char/varchar 长度上限", "char/varchar length limit"},
	"MaxTableNameLen":                {"yes", "表名长度上限", "Table name length limit"},
	"MaxAffectRows":                  {"yes", "影响行数上限（UPDATE/DELETE 精确 COUNT，INSERT 按 VALUES 数）", "Affected-rows limit (exact COUNT for UPDATE/DELETE, VALUES count for INSERT)"},
	"MaxDDLAffectRows":               {"no", "DDL 影响行数上限", "DDL affected-rows limit"},
	"SupportCharset":                 {"yes", "字符集白名单", "Charset whitelist"},
	"SupportCollation":               {"yes", "排序规则白名单", "Collation whitelist"},
	"CheckIdentifier":                {"yes", "保留字检查（内置保留字表）", "Reserved-word check (built-in list)"},
	"MustHaveColumns":                {"yes", "建表必须字段", "Required columns on CREATE TABLE"},
	"DDLMultiToCommit":               {"yes", "单工单多条 DDL 限制", "One DDL statement per order"},
	"DDLPrimaryKeyMust":              {"yes", "主键名必须为 id", "PK must be named id"},
	"DDLAllowColumnType":             {"yes", "禁改列类型", "Forbid column type changes"},
	"DDLImplicitTypeConversion":      {"no", "隐式类型转换", "Implicit type conversion"},
	"DDLAllowPRINotInt":              {"no", "主键非整型", "Non-int primary key"},
	"DDLAllowMultiAlter":             {"yes", "单条 ALTER 多操作限制", "Forbid multi-spec single ALTER"},
	"DDLEnableForeignKey":            {"yes", "外键禁用", "Forbid foreign keys"},
	"DDLTablePrefix":                 {"yes", "表名前缀", "Table name prefix"},
	"DDLColumnsMustHaveIndex":        {"no", "指定列必须有索引", "Columns that must be indexed"},
	"DDLAllowChangeColumnPosition":   {"yes", "AFTER/FIRST 位置告警", "Warn on AFTER/FIRST"},
	"DDLCheckFloatDouble":            {"yes", "float/double 建议 decimal", "Suggest DECIMAL over FLOAT/DOUBLE"},
	"IsOSC":                          {"no", "pt-OSC 相关", "pt-online-schema-change"},
	"OSCExpr":                        {"no", "pt-OSC 相关", "pt-online-schema-change"},
	"OscSize":                        {"no", "pt-OSC 相关", "pt-online-schema-change"},
	"AllowCreateView":                {"yes", "视图禁用", "Forbid views"},
	"AllowCrateViewWithSelectStar":   {"no", "CREATE VIEW SELECT *", "CREATE VIEW with SELECT *"},
	"AllowCreatePartition":           {"yes", "分区表禁用", "Forbid partitioned tables"},
	"AllowSpecialType":               {"no", "特殊类型", "Special column types"},
	"PRIRollBack":                    {"no", "主键回滚", "PK rollback"},
}

func main() {
	check := flag.Bool("check", false, "仅校验 README 矩阵是否与代码一致（CI 用）")
	flag.Parse()

	fields, err := parseAuditRoleFields(structPath)
	if err != nil {
		fail("解析 %s 失败: %v", structPath, err)
	}

	// 结构体新增字段必须同步登记状态，防止矩阵漏项
	for _, f := range fields {
		if _, ok := ruleStatus[f]; !ok {
			fail("规则字段 %s 未在 ruleStatus 登记实现状态，请更新 tools/gen-rules-matrix/main.go", f)
		}
	}

	zhTable := renderTable("zh", fields)
	enTable := renderTable("en", fields)

	if *check {
		ok := true
		if !checkSection(readmeZH, "zh", zhTable) {
			ok = false
		}
		if !checkSection(readmeEN, "en", enTable) {
			ok = false
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "README 规则矩阵与代码不一致，请运行: go run ./tools/gen-rules-matrix")
			os.Exit(1)
		}
		fmt.Println("规则矩阵与代码一致")
		return
	}

	if !updateSection(readmeZH, "zh", zhTable) || !updateSection(readmeEN, "en", enTable) {
		os.Exit(1)
	}
	fmt.Println("已更新 README 规则矩阵（README.md / README_EN.md）")
}

// parseAuditRoleFields 按声明顺序返回 AuditRole 的全部字段名。
func parseAuditRoleFields(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var fields []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != structName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return nil, fmt.Errorf("%s 不是结构体", structName)
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.IsExported() {
						fields = append(fields, name.Name)
					}
				}
			}
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("未找到结构体 %s", structName)
	}
	return fields, nil
}

func category(field string) (string, string) {
	switch {
	case strings.HasPrefix(field, "DML"):
		return "DML", "DML"
	case strings.HasPrefix(field, "DDL"):
		return "DDL", "DDL"
	case field == "IsOSC" || field == "OSCExpr" || field == "OscSize":
		return "OSC", "OSC"
	default:
		return "通用", "General"
	}
}

func renderTable(lang string, fields []string) string {
	var b strings.Builder
	if lang == "zh" {
		b.WriteString("| 规则字段 | 分类 | 状态 | 说明 |\n|---|---|---|---|\n")
	} else {
		b.WriteString("| Rule | Category | Status | Notes |\n|---|---|---|---|\n")
	}
	for _, f := range fields {
		st := ruleStatus[f]
		catZh, catEn := category(f)
		note := st.zh
		cat := catZh
		if lang == "en" {
			note = st.en
			cat = catEn
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f, cat, icons[st.state], note)
	}
	return strings.TrimRight(b.String(), "\n")
}

func checkSection(readme, lang, table string) bool {
	content, err := os.ReadFile(readme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", readme, err)
		return false
	}
	_, found, ok := cutSection(string(content), lang)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s 中缺少规则矩阵标记 (%s)\n", readme, fmt.Sprintf(markerFmt, lang))
		return false
	}
	if strings.TrimSpace(found) != strings.TrimSpace(table) {
		fmt.Fprintf(os.Stderr, "%s 的规则矩阵已过期\n", readme)
		return false
	}
	return true
}

func updateSection(readme, lang, table string) bool {
	content, err := os.ReadFile(readme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", readme, err)
		return false
	}
	updated, _, ok := cutSection(string(content), lang)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s 中缺少规则矩阵标记 (%s)\n", readme, fmt.Sprintf(markerFmt, lang))
		return false
	}
	updated = replaceSection(updated, lang, table)
	if err := os.WriteFile(readme, []byte(updated), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", readme, err)
		return false
	}
	return true
}

// cutSection 提取标记包裹的现有内容；标记缺失时 ok=false。
func cutSection(content, lang string) (before, inner string, ok bool) {
	start := fmt.Sprintf(markerFmt, lang)
	end := fmt.Sprintf(markerEndFmt, lang)
	i := strings.Index(content, start)
	j := strings.Index(content, end)
	if i < 0 || j < 0 || j < i {
		return "", "", false
	}
	inner = strings.Trim(content[i+len(start):j], "\n")
	before = content
	return before, inner, true
}

// replaceSection 用新表替换标记之间的内容。
func replaceSection(content, lang, table string) string {
	start := fmt.Sprintf(markerFmt, lang)
	end := fmt.Sprintf(markerEndFmt, lang)
	i := strings.Index(content, start)
	j := strings.Index(content, end)
	if i < 0 || j < 0 || j < i {
		return content
	}
	return content[:i+len(start)] + "\n" + table + "\n" + content[j:]
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

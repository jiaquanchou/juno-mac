package audit

import (
	"strings"
	"testing"

	"github.com/jiaquanchou/juno-mac/internal/protocol"
)

// defaultRule 与 Yearning install 写入的初始 AuditRole 一致。
func defaultRule() protocol.AuditRole {
	return protocol.AuditRole{
		DMLMaxInsertRows: 10,
		DDLMaxKeyParts:   5,
		DDLMaxKey:        5,
		DDLMaxCharLength: 10,
		MaxTableNameLen:  10,
		MaxAffectRows:    1000,
	}
}

func TestCheckDefaultRules(t *testing.T) {
	rule := defaultRule()
	cases := []struct {
		name      string
		kind      int
		sql       string
		wantLevel uint8
		wantErr   string // 结果 Error 中应包含的子串，空串表示无错误信息
	}{
		{"DDL加列默认通过", 0, "ALTER TABLE users ADD COLUMN nickname VARCHAR(100);", protocol.LevelPass, ""},
		{"DROP TABLE默认禁止", 0, "DROP TABLE users;", protocol.LevelError, "drop table"},
		{"TRUNCATE默认禁止", 0, "TRUNCATE users;", protocol.LevelError, "truncate"},
		{"DROP DATABASE默认禁止", 0, "DROP DATABASE demo;", protocol.LevelError, "drop database"},
		{"建表默认通过", 0, "CREATE TABLE t1 (id BIGINT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(10));", protocol.LevelPass, ""},
		{"UPDATE带WHERE通过", 1, "UPDATE users SET status=0 WHERE id=1;", protocol.LevelPass, ""},
		{"UPDATE无WHERE默认放行", 1, "UPDATE users SET status=0;", protocol.LevelPass, ""},
		{"DELETE无WHERE默认放行", 1, "DELETE FROM users;", protocol.LevelPass, ""},
		{"DML含SELECT默认放行", 1, "SELECT 1;", protocol.LevelPass, ""},
		{"UPDATE带LIMIT默认禁止", 1, "UPDATE users SET status=0 WHERE id=1 LIMIT 1;", protocol.LevelError, "limit"},
		{"insert行数超限", 1, "INSERT INTO users (username) VALUES " + strings.Repeat("('u'),", 12) + "('x');", protocol.LevelError, "最大限制(10)"},
		{"工单类型不匹配-DDL工单含DML", 0, "UPDATE users SET status=0 WHERE id=1;", protocol.LevelError, "DDL工单不允许"},
		{"工单类型不匹配-DML工单含DDL", 1, "ALTER TABLE users ADD COLUMN a INT;", protocol.LevelError, "DML工单不允许"},
		{"多条DDL默认不允许", 0, "ALTER TABLE t ADD COLUMN a INT; ALTER TABLE t ADD COLUMN b INT;", protocol.LevelError, "多条DDL"},
		{"语法错误", 0, "ALTER TABL users;", protocol.LevelError, "SQL语法错误"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := CheckSQL(tc.sql, "demo", tc.kind, "zh_CN", rule, nil)
			if len(rs) == 0 {
				t.Fatal("无审核结果")
			}
			worst := protocol.LevelPass
			var msgs []string
			for _, r := range rs {
				if r.Level < worst {
					worst = r.Level
				}
				if r.Error != "" {
					msgs = append(msgs, r.Error)
				}
			}
			if worst != tc.wantLevel {
				t.Errorf("level = %d, want %d (errors: %q)", worst, tc.wantLevel, strings.Join(msgs, " | "))
			}
			if tc.wantErr != "" && !strings.Contains(strings.Join(msgs, " | "), tc.wantErr) {
				t.Errorf("错误信息 %q 中未找到 %q", strings.Join(msgs, " | "), tc.wantErr)
			}
		})
	}
}

func TestCheckDMLWhereEnabled(t *testing.T) {
	rule := defaultRule()
	rule.DMLWhere = true
	for _, sql := range []string{"UPDATE users SET status=0;", "DELETE FROM users;"} {
		rs := CheckSQL(sql, "demo", 1, "zh_CN", rule, nil)
		if len(rs) != 1 || rs[0].Level != protocol.LevelError {
			t.Fatalf("%s: 开启 DMLWhere 后应为错误，got %+v", sql, rs)
		}
		if !strings.Contains(rs[0].Error, "where条件") {
			t.Errorf("%s: 错误信息异常: %q", sql, rs[0].Error)
		}
	}
}

func TestCheckMaxAffectRows(t *testing.T) {
	rule := defaultRule()
	rule.MaxAffectRows = 2
	rs := CheckSQL("INSERT INTO users (username) VALUES ('a'),('b'),('c');", "demo", 1, "zh_CN", rule, nil)
	if len(rs) != 1 || rs[0].Level != protocol.LevelError {
		t.Fatalf("影响行数超限应为错误，got %+v", rs)
	}
	if !strings.Contains(rs[0].Error, "预计影响行数(3)超过最大限制(2)") {
		t.Errorf("错误信息异常: %q", rs[0].Error)
	}
}

func TestCheckCreateTableRules(t *testing.T) {
	t.Run("开启主键规则后无主键报错", func(t *testing.T) {
		rule := defaultRule()
		rule.DDLEnablePrimaryKey = true
		rs := CheckSQL("CREATE TABLE t (name VARCHAR(10));", "demo", 0, "zh_CN", rule, nil)
		if !strings.Contains(rs[0].Error, "表必须包含主键") {
			t.Errorf("未报主键错误: %q", rs[0].Error)
		}
	})
	t.Run("开启自增主键后非自增主键告警", func(t *testing.T) {
		rule := defaultRule()
		rule.DDLEnableAutoIncrement = true
		rs := CheckSQL("CREATE TABLE t (id BIGINT PRIMARY KEY, name VARCHAR(10));", "demo", 0, "zh_CN", rule, nil)
		if !strings.Contains(rs[0].Error, "AUTO_INCREMENT") {
			t.Errorf("未报自增告警: %q", rs[0].Error)
		}
	})
	t.Run("开启表注释后缺注释告警", func(t *testing.T) {
		rule := defaultRule()
		rule.DDLCheckTableComment = true
		rs := CheckSQL("CREATE TABLE t (id BIGINT PRIMARY KEY);", "demo", 0, "zh_CN", rule, nil)
		if !strings.Contains(rs[0].Error, "表缺少注释") {
			t.Errorf("未报表注释告警: %q", rs[0].Error)
		}
	})
	t.Run("char超长告警", func(t *testing.T) {
		rule := defaultRule()
		rs := CheckSQL("CREATE TABLE t (id BIGINT PRIMARY KEY, name VARCHAR(64));", "demo", 0, "zh_CN", rule, nil)
		if !strings.Contains(rs[0].Error, "长度(64)超过最大限制(10)") {
			t.Errorf("未报长度告警: %q", rs[0].Error)
		}
	})
	t.Run("保留字表名报错", func(t *testing.T) {
		rule := defaultRule()
		rule.CheckIdentifier = true
		rs := CheckSQL("CREATE TABLE `order` (id BIGINT PRIMARY KEY);", "demo", 0, "zh_CN", rule, nil)
		if !strings.Contains(rs[0].Error, "保留关键字") {
			t.Errorf("未报保留字错误: %q", rs[0].Error)
		}
	})
}

func TestQueryRewrite(t *testing.T) {
	rs := QueryRewrite("SELECT * FROM users", 100, "")
	if len(rs) != 1 || rs[0].Level != protocol.LevelPass {
		t.Fatalf("SELECT 应放行，got %+v", rs)
	}
	if rs[0].SQL != "SELECT * FROM users LIMIT 100" {
		t.Errorf("LIMIT 改写异常: %q", rs[0].SQL)
	}
	rs = QueryRewrite("UPDATE users SET status=0", 100, "")
	if len(rs) != 1 || rs[0].Level != protocol.LevelError || !strings.Contains(rs[0].Error, "仅允许SELECT") {
		t.Fatalf("非 SELECT 应报错，got %+v", rs)
	}
}

func TestMergeAlterTables(t *testing.T) {
	out, err := MergeAlterTables("ALTER TABLE t ADD COLUMN a INT; ALTER TABLE t ADD COLUMN b INT; ALTER TABLE other ADD COLUMN c INT;")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ALTER TABLE t") || !strings.Contains(out, "ALTER TABLE other") {
		t.Errorf("合并结果异常: %q", out)
	}
	if strings.Count(out, "ALTER TABLE") != 2 {
		t.Errorf("应按表分组为 2 条，got: %q", out)
	}
}

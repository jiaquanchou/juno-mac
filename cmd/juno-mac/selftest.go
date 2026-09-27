package main

import (
	"fmt"
	"strings"

	"github.com/jiaquanchou/juno-mac/internal/audit"
	"github.com/jiaquanchou/juno-mac/internal/config"
	"github.com/jiaquanchou/juno-mac/internal/protocol"
)

// defaultRule 与 Yearning install 写入的初始 AuditRole 一致（见 Yearning
// src/service/migrate.go），自测时使用。
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

// runSelfTest 对样例 SQL 跑一遍规则审核并打印结果（不连接任何数据库）。
func runSelfTest(cfg *config.Config) {
	lang := cfg.General.Lang
	if lang == "" {
		lang = "zh_CN"
	}
	rule := defaultRule()

	samples := []struct {
		kind int
		sql  string
	}{
		{0, "ALTER TABLE users ADD COLUMN nickname VARCHAR(100);"},
		{0, "DROP TABLE users;"},
		{0, "CREATE TABLE no_pk (name VARCHAR(10));"},
		{1, "UPDATE users SET status = 0 WHERE id = 1;"},
		{1, "UPDATE users SET status = 0;"},
		{1, "DELETE FROM users;"},
		{1, "UPDATE users SET status = 0 WHERE id = 1 LIMIT 1;"},
		{1, "UPDATE users SET status = 0 WHERE id = NULL;"},
		{1, "UPDATE users SET status = 0 WHERE id = 1 ORDER BY id;"},
		{1, "INSERT INTO users (username, email) VALUES " + strings.Repeat("('u','e'),", 12) + "('x','x');"},
		{1, "SELECT 1;"},
	}

	fmt.Println("===== 默认规则（Yearning install 初始值，无目标库连接） =====")
	for _, s := range samples {
		printRecords(s.kind, s.sql, audit.CheckSQL(s.sql, "yearning_demo", s.kind, lang, rule, nil))
	}

	rule2 := defaultRule()
	rule2.DMLWhere = true
	fmt.Println("\n===== 同批语句：开启 DMLWhere 后 =====")
	for _, s := range samples[3:6] {
		printRecords(s.kind, s.sql, audit.CheckSQL(s.sql, "yearning_demo", s.kind, lang, rule2, nil))
	}

	fmt.Println("\n===== MergeAlterTables =====")
	if merged, err := audit.MergeAlterTables("ALTER TABLE t ADD COLUMN a INT; ALTER TABLE t ADD COLUMN b INT; ALTER TABLE other ADD COLUMN c INT;"); err != nil {
		fmt.Println("merge error:", err)
	} else {
		fmt.Println(merged)
	}

	fmt.Println("\n===== Query（SELECT 限流改写） =====")
	for _, r := range audit.QueryRewrite("SELECT * FROM users", 100, "") {
		fmt.Printf("level=%d sql=%q err=%q\n", r.Level, r.SQL, r.Error)
	}
}

func printRecords(kind int, sqlText string, rs []protocol.Record) {
	fmt.Printf("--- [kind=%d] %s\n", kind, sqlText)
	for _, r := range rs {
		fmt.Printf("    level=%d status=%s affect=%d table=%s schema=%s error=%q\n",
			r.Level, r.Status, r.AffectRows, r.Table, r.Schema, r.Error)
	}
}

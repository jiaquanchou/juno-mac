package main

import (
	"fmt"
	"strings"
)

func runSelfTest() {
	rule := defaultRule()
	yearning := new(Engine)

	samples := []struct {
		kind int
		sql  string
	}{
		{0, "ALTER TABLE users ADD COLUMN nickname VARCHAR(100);"},
		{0, "DROP TABLE users;"},
		{1, "UPDATE users SET status = 0 WHERE id = 1;"},
		{1, "UPDATE users SET status = 0;"},
		{1, "DELETE FROM users;"},
		{1, "UPDATE users SET status = 0 WHERE id = 1 LIMIT 1;"},
		{1, "UPDATE users SET status = 0 WHERE id = NULL;"},
		{1, "UPDATE users SET status = 0 WHERE id = 1 ORDER BY id;"},
		{1, "INSERT INTO users (username, email) VALUES " + strings.Repeat("('u','e'),", 12) + "('x','x');"},
		{1, "SELECT 1;"},
		{0, "CREATE TABLE no_pk (name VARCHAR(10));"},
	}

	fmt.Println("===== 默认规则（install 初始值，无目标库连接） =====")
	for _, s := range samples {
		printRecords(s.kind, s.sql, checkSQL(s.sql, "yearning_demo", s.kind, "zh_CN", rule, "", 0, "", ""))
	}

	rule2 := defaultRule()
	rule2.DMLWhere = true
	fmt.Println("\n===== 同批语句：开启 DMLWhere 后 =====")
	for _, s := range samples[2:5] {
		printRecords(s.kind, s.sql, checkSQL(s.sql, "yearning_demo", s.kind, "zh_CN", rule2, "", 0, "", ""))
	}

	fmt.Println("\n===== MergeAlterTables =====")
	var merged string
	if err := yearning.MergeAlterTables("ALTER TABLE t ADD COLUMN a INT; ALTER TABLE t ADD COLUMN b INT; ALTER TABLE other ADD COLUMN c INT;", &merged); err != nil {
		fmt.Println("merge error:", err)
	}
	fmt.Println(merged)

	fmt.Println("\n===== Query（SELECT 限流改写） =====")
	var q []Record
	if err := yearning.Query(&QueryArgs{SQL: "SELECT * FROM users", Limit: 100}, &q); err != nil {
		fmt.Println("query error:", err)
	}
	for _, r := range q {
		fmt.Printf("level=%d sql=%q err=%q\n", r.Level, r.SQL, r.Error)
	}
}

func printRecords(kind int, sqlText string, rs []Record) {
	fmt.Printf("--- [kind=%d] %s\n", kind, sqlText)
	for _, r := range rs {
		fmt.Printf("    level=%d status=%s affect=%d table=%s schema=%s error=%q\n",
			r.Level, r.Status, r.AffectRows, r.Table, r.Schema, r.Error)
	}
}

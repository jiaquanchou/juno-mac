// Package yearningdb 封装引擎对 Yearning 系统库与被审计数据源的连接，
// 以及执行记录、回滚语句、工单状态的回写（官方约定：引擎与 web 共库共配置）。
package yearningdb

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/jiaquanchou/juno-mac/internal/config"
	"github.com/jiaquanchou/juno-mac/internal/protocol"
)

// Open 连接 Yearning 系统库（conf.toml [Mysql]）。
func Open(cfg config.Mysql) *sql.DB {
	port, _ := strconv.Atoi(cfg.Port)
	db, err := sql.Open("mysql", buildDSN(cfg.Host, port, cfg.User, cfg.Password, cfg.Db))
	if err != nil {
		log.Printf("[yearningdb] 连接 Yearning 库失败: %v", err)
		return nil
	}
	return db
}

// OpenTarget 连接被审计数据源；返回 nil 表示无法建立。
func OpenTarget(ip string, port int, user, pass, schema string) *sql.DB {
	db, err := sql.Open("mysql", buildDSN(ip, port, user, pass, schema))
	if err != nil {
		log.Printf("[yearningdb] 目标数据源连接初始化失败: %v", err)
		return nil
	}
	db.SetConnMaxIdleTime(30 * time.Second)
	db.SetMaxOpenConns(2)
	return db
}

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

func Now() string {
	return time.Now().Format("2006-01-02 15:04")
}

func WriteRecord(db *sql.DB, workId, stmt, state string, affect uint, errMsg string) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		"INSERT INTO `core_sql_records` (`work_id`,`sql`,`state`,`affectrow`,`time`,`error`) VALUES (?,?,?,?,?,?)",
		workId, stmt, state, affect, Now(), errMsg)
	if err != nil {
		log.Printf("[yearningdb] 写执行记录失败: %v", err)
	}
}

func InsertRollback(db *sql.DB, workId, rollbackSQL string) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		"INSERT INTO `core_rollbacks` (`work_id`,`sql`) VALUES (?,?)", workId, rollbackSQL)
	if err != nil {
		log.Printf("[yearningdb] 写回滚语句失败: %v", err)
	}
}

func UpdateOrderStatus(db *sql.DB, workId string, status int, executeTime string) {
	if db == nil {
		return
	}
	var err error
	if executeTime == "" {
		_, err = db.Exec("UPDATE `core_sql_orders` SET `status`=? WHERE `work_id`=?", status, workId)
	} else {
		_, err = db.Exec(
			"UPDATE `core_sql_orders` SET `status`=?, `execute_time`=? WHERE `work_id`=?",
			status, executeTime, workId)
	}
	if err != nil {
		log.Printf("[yearningdb] 更新工单状态失败: %v", err)
	}
}

func SucceedOrder(db *sql.DB, workId string) {
	UpdateOrderStatus(db, workId, protocol.OrderStatusSuccess, Now())
}

func FailOrder(db *sql.DB, workId, reason string) {
	log.Printf("[yearningdb] 工单 %s 执行失败: %s", workId, reason)
	UpdateOrderStatus(db, workId, protocol.OrderStatusFailed, Now())
}

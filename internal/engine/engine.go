// Package engine 实现 Yearning web 端调用的全部 RPC 方法，并负责启动 net/rpc(HTTP) 服务。
package engine

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"strings"

	"github.com/jiaquanchou/juno-mac/internal/audit"
	"github.com/jiaquanchou/juno-mac/internal/build"
	"github.com/jiaquanchou/juno-mac/internal/config"
	"github.com/jiaquanchou/juno-mac/internal/protocol"
	"github.com/jiaquanchou/juno-mac/internal/yearningdb"
)

// Engine 实现 Yearning web 端调用的全部 RPC 方法。
type Engine struct {
	Cfg *config.Config
}

func New(cfg *config.Config) *Engine {
	return &Engine{Cfg: cfg}
}

// Serve 注册 RPC 服务并监听 addr（即 Yearning conf.toml 的 RpcAddr）。
func Serve(cfg *config.Config, addr string) error {
	if err := rpc.Register(New(cfg)); err != nil {
		return fmt.Errorf("注册 RPC 服务失败: %w", err)
	}
	rpc.HandleHTTP()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	fmt.Printf("juno-mac %s is running on %s (shared conf: Yearning RpcAddr)\n", build.Version, addr)
	return http.Serve(l, nil)
}

// ==================== Engine.Check ====================

// Check 对工单 SQL 做规则检测（Yearning「检测」按钮 / 提交工单时调用）。
func (e *Engine) Check(args *protocol.CheckArgs, reply *[]protocol.Record) error {
	target := e.openTarget(args.IP, args.Port, args.Username, args.Password, args.Schema)
	if target != nil {
		defer target.Close()
	}
	*reply = audit.CheckSQL(args.SQL, args.Schema, args.Kind, args.Lang, args.Rule, target)
	return nil
}

// ==================== Engine.Query ====================

// Query 查询工单预检：仅放行 SELECT 并按 Limit 自动补全。
func (e *Engine) Query(args *protocol.QueryArgs, reply *[]protocol.Record) error {
	*reply = audit.QueryRewrite(args.SQL, args.Limit, args.InsulateWordList)
	return nil
}

// ==================== Engine.MergeAlterTables ====================

// MergeAlterTables 合并同一张表的多条 ALTER 语句。
func (e *Engine) MergeAlterTables(sqls string, reply *string) error {
	out, err := audit.MergeAlterTables(sqls)
	if err != nil {
		return err
	}
	*reply = out
	return nil
}

// ==================== Engine.StopDelay ====================

// StopDelay 取消延时执行——本地实现不支持延时调度，恒为 no-op。
func (e *Engine) StopDelay(args *protocol.Confirm, reply *string) error {
	*reply = "ok"
	return nil
}

// ==================== Engine.Exec ====================

// Exec 执行已审批的工单：执行前按工单规则重审（error 级拒绝），
// 逐语句执行并回写执行记录；DML 按 Backup 标志生成回滚语句；回写工单状态。
func (e *Engine) Exec(args *protocol.ExecArgs, reply *bool) error {
	*reply = true
	order := args.Order
	if order == nil || order.WorkId == "" {
		return nil
	}

	ydb := yearningdb.Open(e.Cfg.Mysql)
	if ydb != nil {
		defer ydb.Close()
	}

	target := e.openTarget(args.IP, args.Port, args.Username, args.Password, order.DataBase)
	if target == nil {
		yearningdb.FailOrder(ydb, order.WorkId,
			fmt.Sprintf("无法连接目标数据源 %s:%d", args.IP, args.Port))
		*reply = false
		return nil
	}
	defer target.Close()

	stmts, err := audit.Parse(order.SQL)
	if err != nil {
		msg := "SQL语法错误: " + err.Error()
		yearningdb.WriteRecord(ydb, order.WorkId, order.SQL, "执行失败", 0, msg)
		yearningdb.FailOrder(ydb, order.WorkId, msg)
		*reply = false
		return nil
	}

	// 执行前按工单规则重审：存在 error 级语句直接拒绝（与官方「执行前校验」一致）
	kind := order.Type // 0=DDL 1=DML
	pre := audit.CheckSQL(order.SQL, order.DataBase, kind, e.Cfg.General.Lang, args.Rules, target)
	for _, r := range pre {
		if r.Level == protocol.LevelError {
			msg := "执行被审核规则拦截: " + r.Error
			yearningdb.WriteRecord(ydb, order.WorkId, r.SQL, "执行失败", 0, msg)
			yearningdb.FailOrder(ydb, order.WorkId, msg)
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
			preRollback, postRollback = audit.CaptureRollback(target, st, order.DataBase)
		}

		res, err := target.Exec(text)
		if err != nil {
			yearningdb.WriteRecord(ydb, order.WorkId, text, "执行失败", 0, err.Error())
			if firstErr == "" {
				firstErr = err.Error()
			}
			break // 官方行为：执行失败即中断
		}
		affect := uint(0)
		if res != nil {
			if n, rowsErr := res.RowsAffected(); rowsErr == nil {
				affect = uint(n)
			}
		}
		yearningdb.WriteRecord(ydb, order.WorkId, text, "已执行", affect, "")
		if affect > 0 {
			for _, rb := range append(preRollback, postRollback...) {
				yearningdb.InsertRollback(ydb, order.WorkId, rb)
			}
		}
	}

	if firstErr != "" {
		yearningdb.FailOrder(ydb, order.WorkId, firstErr)
		*reply = false
		return nil
	}
	yearningdb.SucceedOrder(ydb, order.WorkId)
	return nil
}

func (e *Engine) openTarget(ip string, port int, user, pass, schema string) *sql.DB {
	if ip == "" {
		return nil
	}
	target := yearningdb.OpenTarget(ip, port, user, pass, schema)
	if target == nil {
		log.Printf("[engine] 目标库连接失败: %s:%d user=%s schema=%s", ip, port, user, schema)
	}
	return target
}

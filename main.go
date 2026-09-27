// juno-mac: Yearning v3.1.x 审核引擎的本地 macOS 实现。
//
// 背景：Yearning v3 的 web 端通过 net/rpc(HTTP) 调用独立审核引擎（官方 Juno，仅
// 提供 Linux 二进制且未开源）。本程序按 Yearning 公开源码中定义的调用契约
// （Engine.Check / Engine.Exec / Engine.Query / Engine.StopDelay /
// Engine.MergeAlterTables，参数结构见 Yearning src/engine/engine.go 与各 handler）
// 在 macOS 上提供等价的本地引擎，仅供本地学习环境使用。
//
// 与 Yearning 共用同一份 conf.toml（[Mysql] 用于回写执行记录，[General] 的
// RpcAddr 为本引擎监听地址），与官方「引擎与 Yearning 共库共配置」的约定一致。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"time"

	"github.com/BurntSushi/toml"
)

type mysqlConf struct {
	Host     string
	User     string
	Password string
	Db       string
	Port     string
}

type generalConf struct {
	SecretKey string
	Host      string
	Hours     int
	RpcAddr   string
	LogLevel  string
	Lang      string
}

// Config 与 Yearning 的 conf.toml 结构保持兼容（多出的键会被 toml 忽略）。
type Config struct {
	Mysql   mysqlConf
	General generalConf
}

var C Config

func main() {
	conf := flag.String("config", "conf.toml", "配置文件路径（与 Yearning 共用 conf.toml）")
	addr := flag.String("addr", "", "RPC 监听地址，覆盖 conf.toml 中的 RpcAddr")
	selftest := flag.Bool("selftest", false, "运行内置自测：对样例 SQL 执行 Check 并打印结果")
	flag.Parse()

	if _, err := toml.DecodeFile(*conf, &C); err != nil {
		log.Fatalf("读取配置 %s 失败: %v", *conf, err)
	}
	if *addr != "" {
		C.General.RpcAddr = *addr
	}
	if C.General.RpcAddr == "" {
		C.General.RpcAddr = "127.0.0.1:50001"
	}

	if *selftest {
		runSelfTest()
		return
	}

	if err := rpc.Register(new(Engine)); err != nil {
		log.Fatalf("注册 RPC 服务失败: %v", err)
	}
	rpc.HandleHTTP()

	l, err := net.Listen("tcp", C.General.RpcAddr)
	if err != nil {
		log.Fatalf("监听 %s 失败: %v", C.General.RpcAddr, err)
	}
	fmt.Printf("juno-mac is running on %s (shared conf: %s)\n", C.General.RpcAddr, *conf)
	log.Fatal(http.Serve(l, nil))
}

func now() string {
	return time.Now().Format("2006-01-02 15:04")
}

func isChinese(lang string) bool {
	return lang != "en_US"
}

// 默认审核规则（与 Yearning install 写入的初始 AuditRole 一致，见
// Yearning src/service/migrate.go），自测时使用。
func defaultRule() AuditRole {
	return AuditRole{
		DMLMaxInsertRows: 10,
		DDLMaxKeyParts:   5,
		DDLMaxKey:        5,
		DDLMaxCharLength: 10,
		MaxTableNameLen:  10,
		MaxAffectRows:    1000,
	}
}

func nowString() string {
	return now()
}

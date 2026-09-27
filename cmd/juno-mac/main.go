// juno-mac：Yearning SQL 审计平台「审核引擎」的本地实现。
//
// 官方架构中 web 端通过 net/rpc(HTTP) 调用独立审核引擎（官方 Juno，闭源且仅
// 提供 Linux 二进制）。本项目按 Yearning 公开源码中的调用契约实现引擎，使
// Yearning 在 macOS 等平台无需 Docker 即可完整跑通审核/执行链路。
//
// 与 Yearning 共用同一份 conf.toml（与官方「引擎与 Yearning 共库共配置」的
// 约定一致）：[Mysql] 用于回写执行记录，[General] 的 RpcAddr 为本引擎监听地址。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jiaquanchou/juno-mac/internal/build"
	"github.com/jiaquanchou/juno-mac/internal/config"
	"github.com/jiaquanchou/juno-mac/internal/engine"
)

func main() {
	conf := flag.String("config", "conf.toml", "配置文件路径（与 Yearning 共用 conf.toml）")
	addr := flag.String("addr", "", "RPC 监听地址，覆盖 conf.toml 中的 RpcAddr")
	selftest := flag.Bool("selftest", false, "运行内置自测：对样例 SQL 执行规则审核并打印结果")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("juno-mac %s\n", build.Version)
		return
	}

	cfg, err := config.Load(*conf)
	if err != nil {
		log.Fatalf("读取配置 %s 失败: %v", *conf, err)
	}
	if *addr != "" {
		cfg.General.RpcAddr = *addr
	}

	if *selftest {
		runSelfTest(cfg)
		return
	}

	if err := engine.Serve(cfg, cfg.General.RpcAddr); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}

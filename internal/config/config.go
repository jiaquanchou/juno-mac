// Package config 解析与 Yearning web 端共用的 conf.toml。
//
// 与官方配置文件保持兼容：[Mysql] 为 Yearning 系统库连接（引擎用它回写
// 执行记录/回滚语句/工单状态），[General] 的 RpcAddr 为本引擎监听地址。
package config

import (
	"github.com/BurntSushi/toml"
)

type Mysql struct {
	Host     string
	User     string
	Password string
	Db       string
	Port     string
}

type General struct {
	SecretKey string
	Host      string
	Hours     int
	RpcAddr   string
	LogLevel  string
	Lang      string
}

type Config struct {
	Mysql   Mysql
	General General
}

// Load 读取 conf.toml；RpcAddr 为空时回落到官方默认端口 127.0.0.1:50001。
func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, err
	}
	if c.General.RpcAddr == "" {
		c.General.RpcAddr = "127.0.0.1:50001"
	}
	return &c, nil
}

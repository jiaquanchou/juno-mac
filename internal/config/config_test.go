package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	content := `
[Mysql]
Db = "yearning"
Host = "127.0.0.1"
Port = "3306"
Password = "pw"
User = "yearning_sys"

[General]
SecretKey = "0123456789abcdef"
Hours = 4
Lang = "zh_CN"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mysql.Db != "yearning" || cfg.Mysql.Port != "3306" || cfg.General.SecretKey != "0123456789abcdef" {
		t.Fatalf("解析结果异常: %+v", cfg)
	}
	// RpcAddr 为空时回落默认值
	if cfg.General.RpcAddr != "127.0.0.1:50001" {
		t.Errorf("RpcAddr 默认值异常: %q", cfg.General.RpcAddr)
	}
}

func TestLoadRpcAddrOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	content := "[General]\nRpcAddr = \"127.0.0.1:60001\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.General.RpcAddr != "127.0.0.1:60001" {
		t.Errorf("RpcAddr 应保留配置值: %q", cfg.General.RpcAddr)
	}
}

// Package build 保存构建期注入的版本信息。
//
// 发布时通过 -ldflags "-X github.com/jiaquanchou/juno-mac/internal/build.Version=vX.Y.Z" 注入。
package build

var Version = "dev"

package store

import "embed"

// migrationsFS 内嵌 migrations 目录下的全部版本化迁移脚本（成对的 *.up.sql / *.down.sql）。
// 迁移脚本随二进制一同发布，启动时由 golang-migrate 读取并执行向上迁移（Req 23.3、18.3）。
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationsFS 返回内嵌迁移脚本的只读文件系统。
// 调用方可将其作为 golang-migrate 的 iofs source 使用，例如：
//
//	d, err := iofs.New(store.MigrationsFS(), "migrations")
//
// 这样迁移脚本无需依赖外部目录即可在启动时自动执行。
func MigrationsFS() embed.FS {
	return migrationsFS
}

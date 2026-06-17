package config

import (
	"github.com/caarlos0/env/v11"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// EnvConfig 保存来自环境变量的配置：数据库与 Redis 连接、data 目录。
//
// 数据库与 Redis 连接通过环境变量注入（Req 18.1、23.1）。其中 PG DSN 与 Redis 地址
// 为必需项，缺失时解析失败并终止启动（Req 18.3）。
type EnvConfig struct {
	// PGDSN 为 PostgreSQL 连接串（环境变量 MPG_PG_DSN，必需）。
	PGDSN string `env:"MPG_PG_DSN,required,notEmpty"`
	// RedisAddr 为 Redis 服务地址（环境变量 MPG_REDIS_ADDR，必需）。
	RedisAddr string `env:"MPG_REDIS_ADDR,required,notEmpty"`
	// RedisPassword 为 Redis 访问密码（环境变量 MPG_REDIS_PASSWORD，可选）。
	RedisPassword string `env:"MPG_REDIS_PASSWORD"`
	// DataDir 为 data 目录路径（环境变量 MPG_DATA_DIR，默认 /data）。
	DataDir string `env:"MPG_DATA_DIR" envDefault:"/data"`
}

// LoadEnvConfig 从环境变量解析 EnvConfig。
//
// 当必需环境变量缺失或为空时返回校验类错误，调用方应据此记录错误并终止启动。
func LoadEnvConfig() (EnvConfig, error) {
	var cfg EnvConfig
	if err := env.Parse(&cfg); err != nil {
		return EnvConfig{}, domain.NewError(
			domain.CodeValidation,
			"读取环境变量失败："+err.Error(),
		)
	}
	return cfg, nil
}

package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// yamlFileName 为常规配置在 data 目录下的文件名（Req 18.2、23.1）。
const yamlFileName = "config.yaml"

// Manager 是配置管理器（Config_Manager），持有环境变量配置与 YAML 常规配置，
// 负责 YAML 的读取、默认值生成、校验与回写持久化（Req 18）。
//
// Manager 的所有导出方法对并发使用是安全的：内部以读写锁保护 YAML 配置快照。
type Manager struct {
	// env 为来自环境变量的不可变配置。
	env EnvConfig
	// yamlPath 为 YAML 配置文件的绝对/相对路径。
	yamlPath string

	mu  sync.RWMutex
	cfg YAMLConfig
}

// Env 返回环境变量配置的副本。
func (m *Manager) Env() EnvConfig {
	return m.env
}

// YAMLPath 返回 YAML 配置文件路径。
func (m *Manager) YAMLPath() string {
	return m.yamlPath
}

// Config 返回当前 YAML 常规配置的快照副本。
func (m *Manager) Config() YAMLConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Load 是配置加载入口：读取环境变量、解析（或以默认值创建）YAML 文件并完成校验。
//
// 设计为可测试：dataDir 通过参数注入而非直接读取全局环境；返回 error 供
// main 据此调用 os.Exit（Req 18.1、18.2、18.3、18.5、18.6）。
//
// 行为：
//   - 读取环境变量，必需项缺失/无效则返回错误（Req 18.1、18.3）。
//   - 若 dataDir 为空则回退到环境变量 MPG_DATA_DIR（默认 /data）。
//   - YAML 文件不存在时以默认配置创建（Req 18.5）。
//   - YAML 内容非法时返回解析错误（Req 18.6）。
//   - 校验各字段取值范围，越界返回校验错误。
//
// 所有错误均通过 logger 记录后返回，便于在启动日志中体现失败原因。
func Load(logger *slog.Logger, dataDir string) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}

	envCfg, err := LoadEnvConfig()
	if err != nil {
		logger.Error("环境变量配置读取失败", "error", err)
		return nil, err
	}

	// dataDir 优先采用显式参数，否则回退到环境变量解析结果。
	if dataDir == "" {
		dataDir = envCfg.DataDir
	}

	yamlPath := filepath.Join(dataDir, yamlFileName)
	cfg, err := loadOrCreateYAML(logger, dataDir, yamlPath)
	if err != nil {
		return nil, err
	}

	if err := ValidateYAMLConfig(cfg); err != nil {
		logger.Error("YAML 配置校验失败", "path", yamlPath, "error", err)
		return nil, err
	}

	logger.Info("配置加载完成", "yamlPath", yamlPath, "dataDir", dataDir)
	return &Manager{
		env:      envCfg,
		yamlPath: yamlPath,
		cfg:      cfg,
	}, nil
}

// loadOrCreateYAML 读取 YAML 配置；文件不存在时以默认配置创建并落盘（Req 18.5）。
func loadOrCreateYAML(logger *slog.Logger, dataDir, yamlPath string) (YAMLConfig, error) {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在：以默认配置创建（Req 18.5）。
			def := DefaultYAMLConfig()
			if werr := writeYAML(dataDir, yamlPath, def); werr != nil {
				logger.Error("创建默认 YAML 配置失败", "path", yamlPath, "error", werr)
				return YAMLConfig{}, werr
			}
			logger.Info("YAML 配置文件不存在，已以默认配置创建", "path", yamlPath)
			return def, nil
		}
		// 其他读取错误（如权限）：记录并终止启动。
		logger.Error("读取 YAML 配置失败", "path", yamlPath, "error", err)
		return YAMLConfig{}, domain.NewError(
			domain.CodeValidation,
			"读取 YAML 配置失败："+err.Error(),
		)
	}

	// 在默认配置之上反序列化，使 YAML 中缺省的字段沿用默认值。
	cfg := DefaultYAMLConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		// YAML 格式非法：记录解析错误并终止启动（Req 18.6）。
		logger.Error("YAML 配置解析失败", "path", yamlPath, "error", err)
		return YAMLConfig{}, domain.NewError(
			domain.CodeValidation,
			"YAML 配置解析失败："+err.Error(),
		)
	}
	return cfg, nil
}

// Save 校验并将给定的 YAML 常规配置回写到文件，成功后更新内存快照（Req 18.4）。
//
// 校验失败时不写盘、不更新内存快照，返回校验错误。
func (m *Manager) Save(cfg YAMLConfig) error {
	if err := ValidateYAMLConfig(cfg); err != nil {
		return err
	}

	dataDir := filepath.Dir(m.yamlPath)
	if err := writeYAML(dataDir, m.yamlPath, cfg); err != nil {
		return err
	}

	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

// writeYAML 将配置序列化为 YAML 并原子性地写入文件，必要时创建 data 目录。
//
// 采用「写临时文件再重命名」的方式降低写入过程中断导致配置文件损坏的风险。
func writeYAML(dataDir, yamlPath string, cfg YAMLConfig) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return domain.NewError(domain.CodeValidation, "创建 data 目录失败："+err.Error())
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "序列化 YAML 配置失败："+err.Error())
	}

	tmp, err := os.CreateTemp(dataDir, ".config-*.yaml.tmp")
	if err != nil {
		return domain.NewError(domain.CodeValidation, "创建临时配置文件失败："+err.Error())
	}
	tmpName := tmp.Name()
	// 失败路径上清理临时文件，避免遗留。
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return domain.NewError(domain.CodeValidation, "写入临时配置文件失败："+err.Error())
	}
	if err := tmp.Close(); err != nil {
		return domain.NewError(domain.CodeValidation, "关闭临时配置文件失败："+err.Error())
	}

	if err := os.Rename(tmpName, yamlPath); err != nil {
		return domain.NewError(domain.CodeValidation, fmt.Sprintf("写回 YAML 配置失败：%v", err))
	}
	return nil
}

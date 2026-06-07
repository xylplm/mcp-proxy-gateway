package backup

import (
	"bytes"
	"encoding/json"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// Marshal 将备份对象序列化为可导入的备份文件字节（Req 23.4）。
//
// 采用带缩进的 JSON 以便人工审阅；序列化失败返回校验类错误（理论上不会发生）。
func Marshal(b Backup) ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "序列化备份文件失败："+err.Error())
	}
	return out, nil
}

// Unmarshal 将备份文件字节解析为备份对象（Req 23.5 的格式解析部分）。
//
// 解析失败（JSON 格式非法、字段类型不符等）一律返回备份无效错误（Req 23.6）。
// 该函数启用 DisallowUnknownFields 以拒绝结构不符的备份，避免静默丢弃未知字段。
func Unmarshal(data []byte) (Backup, error) {
	var b Backup
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Backup{}, domain.NewError(domain.CodeBackupInvalid, "备份文件格式无效："+err.Error())
	}
	// 拒绝尾随多余内容（多个 JSON 文档），保证备份为单一对象。
	if dec.More() {
		return Backup{}, domain.NewError(domain.CodeBackupInvalid, "备份文件格式无效：包含多余的尾随内容")
	}
	return b, nil
}

// Validate 校验备份对象的格式与内容（Req 23.5、23.6）。
//
// 校验内容包括：
//   - 版本号必须匹配 FormatVersion；
//   - YAML 常规配置须通过 config.ValidateYAMLConfig；
//   - 业务配置须通过引用完整性与字段级校验。
//
// 任一项不满足均返回 domain.CodeBackupInvalid 错误，使导入被拒绝（Req 23.6）。
func Validate(b Backup) error {
	if b.Version != FormatVersion {
		return domain.NewError(domain.CodeBackupInvalid,
			"备份文件版本不受支持（期望 "+FormatVersion+"）")
	}

	// 复用常规配置校验逻辑；将其校验错误归一化为备份无效错误。
	if err := config.ValidateYAMLConfig(b.YAML); err != nil {
		return backupInvalidFrom("YAML 常规配置校验失败", err)
	}

	if err := validateBusiness(b.Business); err != nil {
		return err
	}
	return nil
}

// ParseAndValidate 是「解析 + 校验」的组合便捷函数，供导入入口复用。
func ParseAndValidate(data []byte) (Backup, error) {
	b, err := Unmarshal(data)
	if err != nil {
		return Backup{}, err
	}
	if err := Validate(b); err != nil {
		return Backup{}, err
	}
	return b, nil
}

// backupInvalidFrom 将底层错误（如字段级校验错误）包装为携带字段信息的备份无效错误。
func backupInvalidFrom(message string, err error) error {
	var apiErr *domain.APIError
	if ae, ok := err.(*domain.APIError); ok {
		apiErr = ae
	}
	out := domain.NewError(domain.CodeBackupInvalid, message)
	if apiErr != nil {
		out.Message = message + "：" + apiErr.Message
		if len(apiErr.Fields) > 0 {
			out.Fields = apiErr.Fields
		}
	} else if err != nil {
		out.Message = message + "：" + err.Error()
	}
	return out
}

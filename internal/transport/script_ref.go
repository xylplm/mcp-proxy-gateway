package transport

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
)

// scriptRefFromParams 解析 connParams.scriptRef（兼容 map[string]any / typed struct 经 JSON 解码）。
func scriptRefFromParams(params map[string]any) (scripts.LaunchBinding, bool, error) {
	mode, _ := params[ParamLaunchMode].(string)
	if strings.TrimSpace(mode) != "script" {
		return scripts.LaunchBinding{}, false, nil
	}
	raw, ok := params[ParamScriptRef]
	if !ok || raw == nil {
		return scripts.LaunchBinding{}, true, fmt.Errorf("脚本启动缺少 scriptRef")
	}
	var ref scripts.LaunchBinding
	switch v := raw.(type) {
	case scripts.LaunchBinding:
		ref = v
	case map[string]any:
		ref.ScriptID, _ = v["scriptId"].(string)
		ref.Version, _ = v["version"].(string)
		ref.EntryFile, _ = v["entryFile"].(string)
		ref.ContentSHA256, _ = v["contentSha256"].(string)
		ref.Runtime, _ = v["runtime"].(string)
		ref.EntryPath, _ = v["entryPath"].(string)
		if risk, ok := v["riskLevel"].(string); ok {
			ref.RiskLevel = scripts.RiskLevel(risk)
		}
	default:
		// 防御性兼容 map[string]string / 结构体：用反射只读字段，不做 JSON 往返。
		rv := reflect.ValueOf(raw)
		if rv.Kind() == reflect.Pointer && !rv.IsNil() {
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			read := func(name string) string {
				f := rv.FieldByName(name)
				if f.IsValid() && f.Kind() == reflect.String {
					return f.String()
				}
				return ""
			}
			ref = scripts.LaunchBinding{
				ScriptID: read("ScriptID"), Version: read("Version"), EntryFile: read("EntryFile"),
				ContentSHA256: read("ContentSHA256"), Runtime: read("Runtime"), EntryPath: read("EntryPath"),
				RiskLevel: scripts.RiskLevel(read("RiskLevel")),
			}
		} else {
			return scripts.LaunchBinding{}, true, fmt.Errorf("scriptRef 格式非法")
		}
	}
	ref.ScriptID = strings.TrimSpace(ref.ScriptID)
	ref.Version = strings.TrimSpace(ref.Version)
	ref.ContentSHA256 = strings.ToLower(strings.TrimSpace(ref.ContentSHA256))
	if ref.ScriptID == "" {
		return scripts.LaunchBinding{}, true, fmt.Errorf("scriptRef.scriptId 不能为空")
	}
	if !scripts.ValidVersion(ref.Version) {
		return scripts.LaunchBinding{}, true, fmt.Errorf("scriptRef.version 必须为固定版本号（如 v1），不能使用 current")
	}
	if !scripts.ValidSHA256(ref.ContentSHA256) {
		return scripts.LaunchBinding{}, true, fmt.Errorf("scriptRef.contentSha256 必须为 64 位 SHA-256")
	}
	return ref, true, nil
}

// resolveManagedScript fail-closed 地从脚本仓解析绑定，忽略客户端携带的 entryPath/runtime。
// 固定版本会校验哈希；current 由脚本服务解析当前版。
func resolveManagedScript(params map[string]any) (command string, args []string, cwd string, risk scripts.RiskLevel, binding scripts.LaunchBinding, ok bool, err error) {
	ref, isScript, err := scriptRefFromParams(params)
	if err != nil || !isScript {
		return "", nil, "", "", scripts.LaunchBinding{}, isScript, err
	}
	svc := currentScriptService()
	if svc == nil {
		return "", nil, "", "", scripts.LaunchBinding{}, true, fmt.Errorf("脚本服务未就绪，无法启动受管脚本")
	}
	bind, cmd, bindArgs, bindCWD, err := svc.BuildLaunchBinding(ref.ScriptID, ref.Version)
	if err != nil {
		return "", nil, "", "", scripts.LaunchBinding{}, true, err
	}
	if ref.ContentSHA256 != bind.ContentSHA256 {
		return "", nil, "", "", scripts.LaunchBinding{}, true, fmt.Errorf("脚本版本内容已变化，哈希校验失败，请重新选择脚本版本")
	}
	return cmd, bindArgs, bindCWD, bind.RiskLevel, bind, true, nil
}

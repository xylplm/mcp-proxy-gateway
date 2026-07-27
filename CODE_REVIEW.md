# MCP Proxy Gateway 全项目代码审查报告

> 审查时间：2026-07-26
> 审查范围：后端 `internal/**`、`cmd/gateway`、配置层；前端 `web/src/**`
> 重点领域：最新「运行环境（runtime）」相关逻辑 —— 逻辑闭环性、性能、安全性、用户体验
> 文档状态（2026-07-27）：第一批至第四批共 20 条问题已修复并分别提交；当前保留 P1-6、P2-6 等后续观察项。
> 最近验证：`go test ./...` 通过、`npm run build` 通过、`git diff --check` 通过。

---

## 一、总体评价

### 1.1 做得好的地方

运行环境这一块的整体设计质量明显高于一般项目，尤其以下几点值得保留：

| 方面 | 具体实现 | 评价 |
| --- | --- | --- |
| fail-closed 设计 | `resolveDirectoryLaunch` / `resolveManagedScript` 都忽略客户端夹带的 `command/args/cwd`，重新从服务端权威来源解析 | 正确。这是防止「客户端传参绕过校验」的关键 |
| 二次校验 | `session_stdio.go` 在 `ResolveCommand*` 解析出绝对路径后**再次**调用 `ValidateCommandForSecurity`，防 PATH 劫持绕过 allowlist | 正确且容易被忽略，做到了 |
| 包装器 denylist | `deniedCommandBases` 不仅拒绝 shell，还拒绝 `env`/`xargs`/`timeout`/`nohup` 等可把任意命令当参数执行的包装器 | 思考深入，堵住了常见绕过 |
| 供应链完整性 | 预置安装固定版本 + 固定 SHA256 + 主机白名单 + HTTPS 强制 + 重定向校验 + 解压体积上限 + zip-slip 防护 + 跳过符号链接/设备节点 | 相当完整 |
| 脚本内容钉死 | `prepareVerifiedScript` 把已校验字节写入临时文件后 `unlink`，仅保留 FD，经 `/proc/self/fd/3` 执行，抵御 TOCTOU 原地覆写 | 设计精巧，比直接用原路径正确 |
| 存储层双重校验 | `ResolveEntryPath` 除了 EvalSymlinks 越界检查，还重新计算内容哈希与版本元数据比对 | 闭环 |
| 配置热生效 | `policyFromCfg` 每次从配置快照读取，`transport` 通过 provider 间接获取，保存设置无需重启 | 架构干净 |

### 1.2 结论摘要

审查共发现 **23 个问题**，其中：

- **P0（安全/正确性，需尽快修）**：4 个
- **P1（逻辑缺陷/闭环缺失）**：7 个
- **P2（性能/健壮性）**：6 个
- **P3（可维护性/冗余/UX）**：6 个

最需要关注的是：**#1 `isSensitiveEnvKey` 会误删用户显式配置之外的合法父进程变量**、**#2 严格档 `StrictAllowPolicyOnly` 默认 true 导致「严格」名不副实**、**#3 preflight 缓存键未包含 sandbox 能力且全局单例跨 Service 污染**、**#4 Windows 平台 `TerminateProcessTree` 与进程加固能力缺失导致孤儿进程**。

---
## 二、P0 问题（安全 / 正确性，建议尽快修复）

### P0-1 敏感环境变量启发式过宽，会误删业务必需的父进程变量

**位置**：`internal/runtime/env.go:isSensitiveEnvKey`

**现象**：判定逻辑包含如下通配启发：

```go
if strings.Contains(upper, "PASSWORD") ||
    strings.Contains(upper, "SECRET") ||
    strings.HasSuffix(upper, "_TOKEN") ||
    strings.HasSuffix(upper, "_API_KEY") { return true }
```

同时前缀列表里有 `"PG"`、`"KUBE"` 这类**无下划线的短前缀**。

**问题**：
1. `"PG"` 会匹配掉 `PGDATA`，但也会误杀任何以 `PG` 开头的合法变量（例如 `PGADMIN_*` 之外，用户自定义的 `PGO_MODE`）。更严重的是 **`"PG"` 前缀会匹配 `PGHOST`、`PGPORT`，这些恰恰是某些 stdio MCP 连接自己数据库时需要的**。
2. `Contains(upper, "SECRET")` 会误杀 `SECRETS_DIR`、`NO_SECRET_SCAN` 等纯路径/开关类变量。
3. 前缀 `"GH_"`、`"NPM_"` 会剥离 `NPM_CONFIG_REGISTRY` —— 这是**私有 registry 场景下 npx 拉包必需的**，剥离后严格档 npx 直接失败，且错误信息完全无法关联到这里。

**影响**：属于「静默失败」类缺陷。用户在容器里配好了 `NPM_CONFIG_REGISTRY`，stdio 上游却一直拉不到包，排查成本极高。

**最优解**：区分「凭证类」与「配置类」，把宽匹配收敛为**后缀/全词匹配**，并把非机密的配置类前缀移出黑名单：

```go
// 精确凭证后缀：仅在以这些结尾时判定为敏感
var sensitiveEnvSuffixes = []string{
    "_PASSWORD", "_PASSWD", "_SECRET", "_TOKEN",
    "_API_KEY", "_APIKEY", "_ACCESS_KEY", "_PRIVATE_KEY", "_CREDENTIALS",
}

func isSensitiveEnvKey(key string, prefixes []string) bool {
    upper := strings.ToUpper(strings.TrimSpace(key))
    if upper == "" { return false }
    if _, ok := sensitiveEnvExact[upper]; ok { return true }
    // 全词
    switch upper {
    case "TOKEN", "API_KEY", "PASSWORD", "SECRET":
        return true
    }
    for _, s := range sensitiveEnvSuffixes {
        if strings.HasSuffix(upper, s) { return true }
    }
    for _, p := range prefixes {
        if p != "" && strings.HasPrefix(upper, p) { return true }
    }
    return false
}
```

同时把 `sensitiveEnvPrefixes` 中的 `"PG"` 改为 `"PGPASS"`、`"PGSSL"` 等精确项（`PGPASSWORD` 已在 exact 表），把 `"NPM_"` 收窄为 `"NPM_TOKEN"`（已在 exact 表，可直接删除前缀项），`"GH_"` 收窄为 `"GH_TOKEN"`。保留 `MPG_`、`AWS_`、`AZURE_`、`GCP_` 这类确实整段敏感的前缀。

**补充**：这属于行为变更，建议同时在 `env_test.go` 补一条「`NPM_CONFIG_REGISTRY` 必须被保留」的用例锁定语义。

---
### P0-2 严格档默认 `StrictAllowPolicyOnly = true`，「严格安全」名不副实

**位置**：`internal/runtime/policy.go:DefaultPolicy`、`internal/config/yaml.go:defaultRuntimeConfig`、`internal/runtime/security_profile.go:ValidateIsolationRequirement`

**现象**：

```go
StrictAllowPolicyOnly: true, // Phase A：仅策略运行；有 bwrap 后再收紧默认
```

`ValidateIsolationRequirement` 在 `StrictAllowPolicyOnly == true` 时**直接放行**，不要求任何内核隔离。

**问题**：在没有 bubblewrap 的宿主（**包括所有 Windows、macOS，以及未安装 bwrap 的 Linux 容器**）上，用户在 UI 上选择「严格安全」档位后：

- 文件 allowlist 只是**校验 cwd 字符串前缀**，子进程完全可以 `open("/etc/shadow")`；
- 网络 `deny` 完全**不生效**，子进程可以任意联网;
- 但 UI 上显示的是绿色的「严格安全」徽章（`securityModeBadgeClass` 返回 success 色）。

这构成**安全上的错误预期**：用户以为选了强隔离，实际只有命令白名单。`EffectiveSecurity.PolicyOnlyIsolation` 字段确实标记了这一点，`RuntimeEnvironmentView.vue` 也在「本地安全能力」卡片里展示了 `策略 allowlist（本宿主无 bwrap）`，但**上游表单里的档位选择器没有就地提示**，用户在做决策的那一刻看不到。

**最优解**（分两层）：

1. **保留默认 `true`**（否则存量 Windows/macOS 用户升级后所有严格档上游直接连不上，是破坏性变更），但**必须在决策点显式降级展示**：

   在 `StdioSecurityPanel.vue` 中，当 `securityMode === 'strict'` 且后端 preflight 返回的 `effectiveSecurity.policyOnlyIsolation === true` 时，把徽章从 success 改为 warning，并就地显示：

   > 当前宿主未检测到 bubblewrap，严格档的文件与网络限制为**策略校验**，不是内核隔离。如需强隔离请在 Linux 容器安装 bubblewrap。

   对应需要 `StdioSecurityPanel` 新增一个 `policyOnlyIsolation?: boolean` prop，由 `UpstreamFormDrawer` 从 preflight 结果透传。

2. **在 `riskLevelFor` 中反映真实风险**。当前严格档恒定返回 `low`/`medium`：

   ```go
   case SecurityModeStrict:
       if eff.Network.Mode == NetworkAccessUnrestricted || eff.AllowSelfInstall {
           return "medium"
       }
       return "low"
   ```

   建议改为：仅策略运行时不允许评为 `low`：

   ```go
   case SecurityModeStrict:
       if eff.PolicyOnlyIsolation {
           return "medium" // 无内核隔离，不能宣称 low
       }
       if eff.Network.Mode == NetworkAccessUnrestricted || eff.AllowSelfInstall {
           return "medium"
       }
       return "low"
   ```

   注意：`riskLevelFor` 目前在 `PolicyOnlyIsolation` 赋值**之前**被调用，需调整 `ResolveEffectiveSecurity` 中的顺序 —— 先算 `PolicyOnlyIsolation`，再算 `RiskLevel`。

**当前顺序问题（附带 bug）**：`security_profile.go` 中

```go
eff.RiskLevel = riskLevelFor(mode, eff)      // 此时 eff.PolicyOnlyIsolation 仍是零值 false
if mode == SecurityModeStrict && IsolationAvailable() {
    eff.PolicyOnlyIsolation = false
} else {
    eff.PolicyOnlyIsolation = true
}
```

这两行顺序必须对调，否则上面的改法读到的永远是 `false`。

---
### P0-3 preflight 缓存为包级全局变量，缓存键缺失关键维度

**位置**：`internal/runtime/requirements.go` 末尾

**现象**：

```go
var (
    preflightCacheMu sync.Mutex
    preflightCache   = map[string]preflightCacheEntry{}
)
```

**问题**：

1. **包级全局，非 Service 成员**。虽然生产环境是单网关实例，但这让 `Service` 失去自包含性，测试之间会互相污染（一个测试写入的缓存会被另一个测试读到），也让未来多实例/多租户扩展受阻。这与项目「可扩展性高」的目标相悖。

2. **缓存键缺少 sandbox 能力维度**。`preflightCacheKey` 包含了 policy 的各字段，但**没有包含 `IsolationAvailable()` 的结果**。而 `EvaluatePreflight` 内部调用的 `ValidateIsolationRequirement` 与 `ResolveEffectiveSecurity` 都依赖它。虽然 bwrap 的探测结果被 `sync.Once` 永久缓存（见 P2-2，这本身也是个问题），当前不会变化，但一旦按 P2-2 建议改为可刷新，缓存就会返回陈旧结论。

3. **缓存键缺少 `runtimeDir` 下实际文件状态**。这是**真实的用户体验缺陷**：用户手动把 `node` 拷贝进 `runtime/bin`（不是通过预置安装），15 秒内再点「预检」仍然显示「未找到」。`InvalidatePreflightCache` 只在 `InstallPackage`/`UninstallPackage` 后被调用，覆盖不到手动放置这一**文档明确推荐的路径**（`README.txt` 与 `runtimeGuideSteps` 都写了「手动将可执行文件放入 bin」）。

**最优解**：

1. 把缓存移入 `Service`：

```go
type Service struct {
    // ...
    preflightMu    sync.Mutex
    preflightCache map[string]preflightCacheEntry
}
```

`InvalidatePreflightCache` 相应改为方法 `func (s *Service) InvalidatePreflightCache()`。若需保留包级函数以兼容现有调用点，可保留一个薄封装但标注 Deprecated。

2. 缓存键加入隔离能力与目录指纹：

```go
raw := strings.Join([]string{
    // ... 现有字段
    fmt.Sprintf("%v", IsolationAvailable()),
    runtimeDirFingerprint(runtimeDir), // 见下
}, "#")

// runtimeDirFingerprint 取各 PATH 前缀目录的 ModTime，成本 O(前缀数) 而非 O(文件数)
func runtimeDirFingerprint(runtimeDir string) string {
    var b strings.Builder
    for _, dir := range PathPrefixes(runtimeDir) {
        if st, err := os.Stat(dir); err == nil {
            fmt.Fprintf(&b, "%s:%d;", dir, st.ModTime().UnixNano())
        }
    }
    return b.String()
}
```

目录 `ModTime` 在其下**新增/删除文件**时会更新，正好覆盖「手动拷贝二进制」场景，且只需 4 次 `Stat`，开销可忽略。

3. 前端补一个显式「重新探测」按钮绕过缓存（配合 P1-6）。

---
### P0-4 Windows 平台进程加固完全缺失，且清理逻辑依赖外部命令

**位置**：`internal/runtime/sandbox_other.go`、`internal/runtime/process_windows.go`

**现象**：

```go
// sandbox_other.go
func applySandboxPlatform(cmd *exec.Cmd, _ SandboxOptions) {
    // Windows / 其他：保持 no-op
    _ = cmd
}
```

```go
// process_windows.go
_ = exec.CommandContext(ctx, "taskkill", "/PID", ..., "/T", "/F").Run()
```

**问题**：

1. **孤儿进程**。Windows 上子进程没有加入 Job Object，网关进程被强杀（任务管理器结束进程、崩溃）后，stdio 上游子进程及其孙进程会全部残留。而 `npx` 启动的 MCP 通常是 `npx → node → 实际服务` 的多层结构，残留会占用端口和内存，用户只能手工清理。

2. **`taskkill` 依赖外部可执行文件**。若 `PATH` 被污染或系统精简，`taskkill` 不可用则整个进程树清理静默失败（返回值被 `_ =` 丢弃）。而且 `taskkill` 在 `PATH` 中的位置是可被劫持的——虽然攻击者要能改 `PATH` 通常已有更高权限，但这里用了未限定路径，不符合本项目其他地方的严谨程度。

3. **能力描述与实际不符导致 UI 误导**：`describeSandboxPlatform` 在 Windows 返回 `ProcessHardeningSupported: false`，而 `EffectiveSecurity.ProcessHardening` 却来自 `policy.ProcessHardening`（默认 `true`）。`session_stdio.go` 用后者决定是否调用 `TerminateProcessTree`。于是 Windows 上 `hardening == true` 但 `applySandboxPlatform` 是 no-op —— 状态不一致，且前端 `sandboxHardeningLabel` 会显示「策略加固已启用」，语义模糊。

**最优解**：

1. **Windows 使用 Job Object**（这是该平台的标准做法，等价于 Linux 的进程组 + Pdeathsig）。新建 `sandbox_windows.go`：

```go
//go:build windows

package runtime

import (
    "os/exec"
    "syscall"
)

func applySandboxPlatform(cmd *exec.Cmd, opts SandboxOptions) {
    if cmd.SysProcAttr == nil {
        cmd.SysProcAttr = &syscall.SysProcAttr{}
    }
    // 新建进程组，使子进程不继承父进程的 Ctrl-C，并可按组终止
    cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func describeSandboxPlatform() SandboxCapabilities {
    return SandboxCapabilities{
        ProcessHardeningSupported:    true,
        FilesystemIsolationSupported: false,
        NetworkIsolationSupported:    false,
        HostAllowlistEnforced:        false,
        IsolationBackend:             "none",
        Platform:                     "windows",
        Description:                  "Windows：stdio 子进程使用独立进程组，会话关闭时按进程树终止；文件与网络限制为策略约束。",
    }
}
```

完整的 Job Object（`AssignProcessToJobObject` + `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`）能做到「父进程消失即杀子进程」，需要 `golang.org/x/sys/windows`。若不想引入依赖，`CREATE_NEW_PROCESS_GROUP` 至少让进程组语义成立，是最小可用改进。

2. **`taskkill` 使用绝对路径并记录失败**：

```go
sysRoot := os.Getenv("SystemRoot")
if sysRoot == "" { sysRoot = `C:\Windows` }
taskkill := filepath.Join(sysRoot, "System32", "taskkill.exe")
if err := exec.CommandContext(ctx, taskkill, "/PID", pid, "/T", "/F").Run(); err != nil {
    // 至少不要完全静默；由调用方 logger 记录
}
```

3. **统一 hardening 语义**：`ResolveEffectiveSecurity` 中把 `eff.ProcessHardening` 与平台能力取交集：

```go
eff.ProcessHardening = policy.ProcessHardening && DescribeSandbox().ProcessHardeningSupported
```

这样 `hardening` 字段的含义变成「实际生效」而非「配置期望」，前端展示与 `TerminateProcessTree` 的调用条件都会自洽。

---
## 三、P1 问题（逻辑缺陷 / 闭环缺失）

### P1-1 `DetectSelfInstallIntent` 存在明确的绕过路径

**位置**：`internal/runtime/security_profile.go:DetectSelfInstallIntent`

**现象**：

```go
for i, a := range args {
    al := strings.ToLower(strings.TrimSpace(a))
    if pm && (al == "-g" || al == "--global") { return error }
    if al == "install" || al == "i" || (pm && al == "add") {
        if pm || i == 0 { return error }
    }
}
if !pm { return nil }   // <-- 命令不是包管理器时，后续所有检查直接跳过
```

**问题**：当 `command` 是 `node` 时 `pm == false`，函数在遍历完 args 后**直接返回 nil**。而 `node` 完全可以执行一个内部调用 `npm install` 的脚本。当然，这类启发式本来就防不住任意脚本 —— 但问题在于**它给了虚假的安全感**，且与「严格档禁止自装包」的 UI 承诺不符。

更具体的绕过：`uvx --with some-package tool` 中的 `--with` 会**安装额外依赖**，`extractUvxTarget` 里 `--with` 被当作「跳过下一个参数」处理，`DetectSelfInstallIntent` 也不检查它。这是严格档下**实际可用的装包通道**。

**最优解**：

1. 在 `extractUvxTarget` 中，严格档遇到 `--with` / `--with-editable` / `--with-requirements` 时**直接拒绝**，而不是跳过：

```go
case al == "--with" || al == "--with-editable" || al == "--with-requirements":
    return "", false, fmt.Errorf("严格安全模式禁止 uvx 使用 %s 附加依赖，请将依赖固化到目标包", a)
```

2. 同理 npx 的 `--package` 只允许出现一次且必须落在白名单（当前逻辑是遇到 `-p` 就返回，多个 `-p` 只校验第一个）。

3. **在文案上诚实**。`DetectSelfInstallIntent` 的注释已经写了「启发式」，但 UI 上 `StdioSecurityPanel.vue` 的 `allowSelfInstall` 描述是「允许脚本自装包（npm/pip 等）」，暗示关闭后就不能装。建议改为：

   > 关闭后会拒绝常见的自装包参数（install / -g / --with 等）。注意：这是参数层面的检查，无法阻止脚本在运行时自行调用包管理器。需要强约束请使用严格档 + 网络拒绝出站。

---

### P1-2 `ValidateEffectiveSecurity` 的 `RequiresAck` 校验可被绕过

**位置**：`internal/runtime/security_profile.go`

**现象**：

```go
if profile.Mode == SecurityModeUnrestricted {
    eff.RequiresAck = true
}
```

只有**上游显式声明** `mode: unrestricted` 时才要求确认备注。若全局 `default_stdio_security_mode` 被设为 `unrestricted`，上游不写 `mode`，则 `profile.Mode == ""`，`eff.Mode` 解析为 `unrestricted`，但 `RequiresAck` 为 `false`，**跳过备注强制**。

代码注释解释了这个设计（「全局默认 unrestricted 不拦截存量未声明上游，避免主业务被一刀切」），意图可以理解，但结果是：**管理员把全局默认改成完全放行后，所有上游静默进入最高风险档且无任何确认痕迹**。

**最优解**：保留「不拦截存量」的意图，但改为在**全局设置层**做一次性确认，而不是在每个上游放行：

1. 在 `SettingsView` 保存 `default_stdio_security_mode = unrestricted` 时，要求管理员二次确认（前端 `ConfirmDialog` + 后端在 `settings.go` 校验一个 `acknowledgeUnrestrictedDefault: true` 字段）。
2. `BuildSummary` 的 `RiskNotes` 在全局默认为 `unrestricted` 时置顶一条红色提示。
3. `runtimeSummary.ts` 的 `defaultSecurityModeLabel` 在 `unrestricted` 时返回带警示的展示（当前只是「完全放行」四个字，与「标准」同样的中性样式）。

---
### P1-3 `findExecutableInDir` 忽略可执行位，导致探测「假阳性」

**位置**：`internal/runtime/path_lookup.go:findExecutableInDir`

**现象**：

```go
if runtime.GOOS != "windows" {
    if st.Mode()&0o111 == 0 {
        // 卷内用户拷贝的二进制偶发无 +x，仍允许作为候选（由 OS 最终判定）。
        // 保持发现能力，避免「文件在却探测不到」。
    }
}
return c, true
```

一个空的 `if` 块，只有注释。

**问题**：意图（避免「文件在却探测不到」）是合理的，但**实现方式把问题推给了后面**：

- 运行环境页会显示该工具「可用」并给出路径；
- 用户创建 stdio 上游，preflight 也显示「依赖已就绪」；
- 真正连接时 `exec` 返回 `permission denied`，错误信息与运行环境页的绿色状态直接矛盾。

这是典型的**闭环断裂**：探测层与执行层判据不一致。

**最优解**：保留发现能力，但**把状态如实传达**。`ToolStatus` 增加一个字段：

```go
type ToolStatus struct {
    Name      string `json:"name"`
    Available bool   `json:"available"`
    Path      string `json:"path,omitempty"`
    // Warning 非空表示找到了文件但可能无法执行（如缺少可执行位）
    Warning   string `json:"warning,omitempty"`
}
```

`findExecutableInDir` 返回第三个值标记 `needsExecBit`，向上传递到 `Doctor.Probe` 与 `PreflightItem`：

```go
if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
    return c, true, true // found, but not executable
}
```

前端在 `RuntimeEnvironmentView.vue` 的工具卡片上，对有 warning 的项显示 warning 色 + 可操作提示：

> 已找到文件但缺少可执行权限，请执行 `chmod +x <path>`

这样用户拿到的是**可直接行动的信息**，而不是一个矛盾的绿色状态。同样地，`PreflightItem.Message` 也应带上这条。

---

### P1-4 `InspectDirectoryLaunch` 的约定探测与 manifest 分支行为不一致

**位置**：`internal/runtime/directory_launch.go`

**现象**：manifest 分支中，任何一个 entry 校验失败都会导致**整个 inspect 失败**：

```go
normalized, err := normalizeDirectoryEntry(clean, entry, policy)
if err != nil {
    return DirectoryLaunchResult{}, fmt.Errorf("entries[%d]：%w", i, err)
}
```

而约定探测分支中，校验失败的候选被**静默跳过**：

```go
normalized, nerr := normalizeDirectoryEntry(clean, entry, policy)
if nerr == nil {
    res.Entries = append(res.Entries, normalized)
}
```

**问题**：用户写了一个 4 条 entry 的 `mpg.launch.json`，其中第 3 条的 command 不在白名单，则**前 2 条可用的也全部拿不到**，UI 只显示一条 `entries[2]：命令 "xxx" 不在 stdio 允许列表中`。用户必须先修好第 3 条才能用第 1 条，体验割裂。

**最优解**：manifest 分支改为**部分成功 + 结构化警告**，与约定分支对齐：

```go
for i, entry := range manifest.Entries {
    normalized, err := normalizeDirectoryEntry(clean, entry, policy)
    if err != nil {
        res.Warnings = append(res.Warnings,
            fmt.Sprintf("entries[%d] 已跳过：%v", i, err))
        continue
    }
    if _, ok := seen[normalized.ID]; ok {
        res.Warnings = append(res.Warnings,
            fmt.Sprintf("entries[%d] 入口 ID 重复已跳过：%s", i, normalized.ID))
        continue
    }
    seen[normalized.ID] = struct{}{}
    res.Entries = append(res.Entries, normalized)
}
if len(res.Entries) == 0 {
    return DirectoryLaunchResult{}, fmt.Errorf("mpg.launch.json 中没有可用入口：%s",
        strings.Join(res.Warnings, "；"))
}
```

**注意**：JSON 解析失败、version 非法、entries 超过 16 条这些**清单级**错误仍应整体失败，只有**单条 entry 级**错误降级为警告。前端 `StdioLaunchPanel.vue` 已经有 `directoryWarnings` 的展示位，无需改动即可承载。

---
### P1-5 目录启动的可用根与路径浏览根不一致，UI 可选到无法启动的目录

**位置**：`internal/transport/directory_ref.go:resolveDirectoryLaunch` 对比 `internal/runtime/fsbrowse.go:BuildBrowseRoots`

**现象**：

- **浏览**根 = `dataDir` + `runtimeDir` + `GlobalFileRoots` + **`BrowseExtraRoots`**
- **目录启动**根 = `GlobalFileRoots` + 上游声明的 `FileAccess.Paths`（**不含 `BrowseExtraRoots`、不含 `dataDir`/`runtimeDir`**）

```go
// directory_ref.go
roots := append([]string{}, policy.GlobalFileRoots...)
roots = append(roots, declaredRoots...)
if len(roots) == 0 {
    return ..., fmt.Errorf("目录启动根不在文件允许路径内")
}
```

**问题**：这个差异是**有意的**（注释写了「额外浏览根仅供管理台选路，不授予代码执行权限」），安全上正确。但**用户体验上是断裂的**：

1. 用户在 `StdioLaunchPanel` 点「项目目录」的路径选择器，能浏览到 `BrowseExtraRoots` 下的目录；
2. 选中后点「识别入口」—— `runtimeDirectoryInspect` 走的是 `BrowseStat`（浏览根），**成功**，列出了入口；
3. 用户选了入口、填完表单、点保存 —— `validateStdioParams` → `resolveDirectoryLaunch` 用的是启动根，**失败**：「目录启动根真实位置不在允许路径内」。

用户走完整个流程才在最后一步被拒绝，且错误文案没有说明「该目录可浏览但不可启动，请加入 global_file_roots」。

另外，`GlobalFileRoots` 为空时（**这是出厂默认**：`defaultRuntimeConfig` 里 `GlobalFileRoots: []string{}`），只要上游没在 `securityProfile.fileAccess.paths` 里声明路径，**目录启动模式 100% 不可用**。而 UI 上「本地目录启动」这个卡片是**默认可见可点的**，没有任何前置条件提示。

**最优解**：

1. **在 inspect 阶段就用启动根校验**，让失败尽早发生。`runtimeDirectoryInspect` 增加一步：

```go
// 先经浏览根确认可读（已有）
stat, err := r.runtimeEnv.BrowseStat(path, nil)
// ...
// 再确认该目录是否落在「可启动根」内，不满足时明确告知
pol := r.runtimeEnv.Policy()
if len(pol.GlobalFileRoots) == 0 {
    respondError(c, domain.NewValidationError(
        "尚未配置全局文件允许路径，目录启动不可用。请先在系统设置 → 运行环境中添加 global_file_roots，或在本上游的严格档文件允许路径中声明该目录。",
        map[string]string{"path": "缺少可启动的文件允许根"}))
    return
}
```

2. **前端在启动方式卡片上做前置提示**。`StdioLaunchPanel.vue` 接收一个 `directoryLaunchAvailable: boolean`（由 `runtime/summary` 的 `globalFileRoots.length > 0` 推导），为 false 时把「本地目录启动」卡片置灰并显示：

   > 需要先配置全局文件允许路径

3. **错误文案补充可操作指引**。`resolveDirectoryLaunch` 的两处错误文案改为：

```go
return "", nil, "", true, fmt.Errorf(
    "目录 %q 不在文件允许路径内。可浏览的目录不等于可启动，请将其加入系统设置的 global_file_roots，或在本上游严格档的文件允许路径中声明", ref.Root)
```

---

### P1-6 手动放置工具后无刷新入口，缓存导致状态陈旧

**位置**：`web/src/views/RuntimeEnvironmentView.vue`、`internal/runtime/requirements.go`

**现象**：运行环境页的「刷新探测」调用 `getRuntimeSummary()`。`Summary()` 每次都重新 `Probe()`，**不走 preflight 缓存**，所以这个按钮是有效的。

但**上游表单里的 preflight 走 15 秒缓存**，且如 P0-3 所述缓存键不含目录状态。用户流程：

1. 运行环境页手动放入 `node` → 点刷新 → 显示「可用」✅
2. 切到上游表单 → preflight 命中 15 秒前的缓存 → 显示「依赖未就绪」❌

两个页面**同一时刻显示矛盾的结论**。

**最优解**：

1. 按 P0-3 把 `runtimeDirFingerprint` 加入缓存键，从根源消除陈旧。
2. `preflightRuntime` API 增加可选的 `refresh?: boolean`，前端「重新检测」按钮传 `true`，后端跳过缓存读取（仍写入）：

```go
func (s *Service) Preflight(req PreflightRequest) PreflightResult {
    // ...
    if !req.Refresh {
        // 查缓存
    }
    res := EvaluatePreflight(req, policy, runtimeDir, nil)
    // 写缓存
}
```

3. 安装/卸载后除了 `InvalidatePreflightCache()`，前端也应主动重新触发一次表单的 preflight（当前 `RuntimeEnvironmentView` 安装完只 `load()` 自己的 summary，上游表单如果开着不会更新）。

---
### P1-7 `SoftDelete` 在 rename 失败时返回 nil，掩盖了错误

**位置**：`internal/scripts/store.go:SoftDelete`

**现象**：

```go
if err := os.Rename(src, dst); err != nil {
    // rename 失败时仍保留 trash 状态：脚本已从列表消失，后续 Get 返回不存在。
    // 不回滚为 active，避免「删除成功」后仍可被启动。
    return nil
}
```

**问题**：注释的推理是对的（不回滚是正确选择），但**返回 nil 隐瞒了失败**。结果是：

- `data/scripts/library/scr_xxx/` 目录**永久残留**，占用磁盘且计入 `scriptCountUnlocked()` 的 `MaxScripts=500` 配额；
- 用户看到「删除成功」，但脚本数量上限却在逼近，无从解释；
- 运维在磁盘上看到大量 `status: trash` 的目录却不在 trash 目录里，难以诊断。

Windows 上如果有进程持有该目录下文件的句柄（例如刚启动过的脚本快照），`os.Rename` 失败是**很常见**的。

**最优解**：状态保持不回滚（正确），但把失败**上报为可诊断的成功降级**：

```go
if err := os.Rename(src, dst); err != nil {
    // 状态已置 trash，业务上已不可见/不可启动；但物理清理失败需要可观测。
    return fmt.Errorf("脚本已停用，但回收站移动失败，目录仍占用磁盘（%s）：%w", src, err)
}
```

调用方 `httpapi/scripts.go:deleteScript` 目前是 `respondError(c, mapScriptErr(err))`，会把它当成失败返回给前端 —— 这不合适，因为业务上确实删成功了。建议改为：

- `scripts.Service.Delete` 返回一个可区分的哨兵错误 `ErrTrashMoveFailed`；
- handler 遇到它时**返回成功**，但把详情写入系统日志（`internal/syslog`），并在响应里带一个 `warning` 字段；
- 前端 toast 显示 `脚本已删除，但磁盘清理未完成，请联系管理员检查`。

这样既不误导用户，也保留了可观测性。另外建议增加一个启动时的清理任务：扫描 library 下 `status == trash` 的目录并重试移动。

---

## 四、P2 问题（性能 / 健壮性）

### P2-1 `PathPrefixes` 在每次 stdio 连接时同步 `Stat` 四次

**位置**：`internal/runtime/layout.go:PathPrefixes`，经 `transport/security_hooks.go:currentPathPrefixes` 在每次 `Connect` 调用

**现象**：`currentPathPrefixes()` → `PathPrefixes(runtimeDir)` → 对 `bin`、`node/bin`、`python/bin`、`uv/bin` 各做一次 `os.Stat`。

**影响**：单次连接 4 次 syscall 本身不重，但：
- 连接重试场景下（`manager.RetryPolicy` 退避重连）会持续触发；
- `EvaluatePreflight` 中也会调用；
- 若 `runtimeDir` 位于网络存储（NFS/SMB 挂载的数据卷，Docker 部署常见），`Stat` 延迟可能达到毫秒级，4 次叠加在连接路径上。

**最优解**：加一个短 TTL 的缓存，与 preflight 的 fingerprint 复用同一份 `Stat` 结果：

```go
type prefixCache struct {
    mu       sync.RWMutex
    dir      string
    at       time.Time
    prefixes []string
}

func (c *prefixCache) get(runtimeDir string) []string {
    c.mu.RLock()
    if c.dir == runtimeDir && time.Since(c.at) < 5*time.Second {
        out := append([]string{}, c.prefixes...)
        c.mu.RUnlock()
        return out
    }
    c.mu.RUnlock()
    // 慢路径重算并写入
}
```

5 秒 TTL 对「放入文件后重启连接」的场景足够灵敏，同时消除高频重连时的重复 syscall。注意返回副本，避免调用方修改缓存内容。

---

### P2-2 bwrap 探测结果被 `sync.Once` 永久缓存，安装后需重启进程

**位置**：`internal/runtime/sandbox_linux.go`

**现象**：

```go
var (
    bwrapOnce sync.Once
    bwrapPath string
)
func lookPathBwrap() string {
    bwrapOnce.Do(func() { ... })
    return bwrapPath
}
```

**问题**：管理员在运行中的容器里 `apt-get install bubblewrap` 后，`IsolationAvailable()` 仍返回 `false` 直到**重启网关进程**。而：

- `DescribeSandbox()` 会一直告诉 UI「本宿主无 bwrap」；
- `ValidateIsolationRequirement` 会一直走 `StrictAllowPolicyOnly` 分支；
- 严格档一直是仅策略运行。

用户没有任何提示说明「需要重启才生效」。

**最优解**：改为带 TTL 的缓存（隔离能力探测不是热路径，30 秒足够）：

```go
var bwrapCache struct {
    mu   sync.RWMutex
    path string
    at   time.Time
}

func lookPathBwrap() string {
    bwrapCache.mu.RLock()
    if time.Since(bwrapCache.at) < 30*time.Second {
        p := bwrapCache.path
        bwrapCache.mu.RUnlock()
        return p
    }
    bwrapCache.mu.RUnlock()

    bwrapCache.mu.Lock()
    defer bwrapCache.mu.Unlock()
    if time.Since(bwrapCache.at) < 30*time.Second {
        return bwrapCache.path
    }
    p, _ := exec.LookPath("bwrap")
    bwrapCache.path = p
    bwrapCache.at = time.Now()
    return p
}
```

配合 P0-3 把 `IsolationAvailable()` 纳入 preflight 缓存键，安装 bwrap 后 30 秒内即可自动生效，无需重启。

---
### P2-3 `Summary()` 每次都触发 `EnsureRuntimeLayout` 与全量 catalog 扫描

**位置**：`internal/runtime/doctor.go:Service.Summary`

**现象**：

```go
func (s *Service) Summary() Summary {
    // ...
    _ = EnsureRuntimeLayout(runtimeDir)          // 6 次 MkdirAll + README Stat
    prefixes := PathPrefixes(runtimeDir)          // 4 次 Stat
    doctor := NewDoctor(...)
    // Probe: 8 个工具 × (4 个前缀 Stat + LookPath 全 PATH 扫描)
    inst := s.installer()
    return BuildSummary(..., inst.CatalogWithStatus(), inst.ListInstalled())
    // CatalogWithStatus + ListInstalled 各读一次 installed.json
}
```

**影响**：`GET /api/admin/runtime/summary` 是运行环境页的主接口。单次调用约：6 `MkdirAll` + 4 `Stat` + 8×(4 `Stat` + `LookPath` 遍历整个 PATH) + 2 次 JSON 文件读取 + `DescribeSandbox`。在 PATH 较长的容器里，`exec.LookPath` 对每个工具要遍历所有 PATH 目录。总计可达数百次 syscall。

页面本身只在打开时调用一次，问题不大。但：
- `loadState()` 被调用两次（`CatalogWithStatus` 一次、`ListInstalled` 一次），读同一个文件；
- `EnsureRuntimeLayout` 在每次查看页面时做 6 次 `MkdirAll`，是纯粹的浪费（目录已存在）。

**最优解**：

1. `loadState()` 只读一次，传给两个消费者：

```go
func (s *Service) Summary() Summary {
    // ...
    inst := s.installer()
    state := inst.loadState()                       // 一次读取
    catalog := inst.catalogWithState(state)         // 新增接受 state 的内部方法
    return BuildSummary(policy, doctor.Probe(), dataDir, runtimeDir,
        prefixes, catalog, state.Packages)
}
```

2. `EnsureRuntimeLayout` 改为幂等且带内存标记，同一 runtimeDir 只在进程内首次执行：

```go
var layoutEnsured sync.Map // runtimeDir -> struct{}

func EnsureRuntimeLayoutOnce(runtimeDir string) error {
    if runtimeDir == "" { return nil }
    if _, ok := layoutEnsured.Load(runtimeDir); ok { return nil }
    if err := EnsureRuntimeLayout(runtimeDir); err != nil { return err }
    layoutEnsured.Store(runtimeDir, struct{}{})
    return nil
}
```

`Summary()` 改用 `EnsureRuntimeLayoutOnce`。`build.go` 启动时已经调了一次 `EnsureRuntimeLayout`，正常情况下 Summary 里这次是纯浪费。

3. `Probe()` 复用 P2-1 的 prefix 缓存。

---

### P2-4 `ListBrowseDir` 的分页截断逻辑存在边界问题

**位置**：`internal/runtime/fsbrowse.go:ListBrowseDir`

**现象**：

```go
for len(entries) <= limit {
    names, readErr := f.Readdirnames(256)
    // ...
    for _, name := range names {
        // ... 过滤
        entries = append(entries, item)
        if len(entries) > limit {
            truncated = true
            break
        }
    }
    if truncated || readErr == io.EOF || len(names) == 0 { break }
}
if truncated { entries = entries[:limit] }
```

**问题**：

1. **`truncated` 语义不精确**。当目录恰好有 `limit` 个匹配项时，外层 `for len(entries) <= limit` 条件成立（`limit <= limit`），会再读一批；若读到 EOF 则正常退出，`truncated = false`。正确。但若目录有 `limit + 1` 个匹配项，读到第 `limit+1` 个时 `len(entries) > limit` 成立，`truncated = true` —— 此时确实还有更多，正确。逻辑其实是对的，但**可读性差**，`<=` 与 `>` 的配合需要仔细推演才能确认无误。

2. **排序在截断之后**。`sort.SliceStable` 在 `entries[:limit]` 上执行，意味着**返回的不是「排序后的前 N 项」，而是「读取顺序的前 N 项再排序」**。目录项的读取顺序是文件系统决定的（ext4 上是哈希序），所以：
   - 用户看到的「前 200 项」是**随机子集**，不是字典序前 200；
   - 每次刷新可能返回**不同的**子集（如果目录有变化）；
   - 「目录优先」的排序只在这个随机子集内生效，可能有目录被截断掉而文件留下。

对于一个用于「选择目录」的路径浏览器，这个行为会让用户在大目录里**找不到明明存在的子目录**。

**最优解**：目录浏览场景下，条目数通常可控（`browseMaxLimit = 500`）。建议**先收集全部匹配项（带上限保护），排序后再截断**：

```go
const hardScanCap = 5000 // 防止超大目录耗尽内存

all := make([]BrowseEntry, 0, limit)
scanned := 0
for {
    names, readErr := f.Readdirnames(256)
    for _, name := range names {
        scanned++
        // ... 过滤，追加到 all
    }
    if readErr == io.EOF || len(names) == 0 || scanned >= hardScanCap { break }
}
sort.SliceStable(all, byDirThenName)
truncated := len(all) > limit
if truncated { all = all[:limit] }
```

这样返回的永远是**字典序前 N 项且目录优先**，行为稳定可预期。`hardScanCap` 保证内存上界（5000 项 × ~200 字节 ≈ 1MB，可接受）。若 `scanned >= hardScanCap` 也应置 `truncated = true` 并在响应里区分「因上限截断」，前端提示用户使用手动输入路径。

---
### P2-5 安装失败时残留 staging 目录与半删除的 target

**位置**：`internal/runtime/install.go:placeNode`

**现象**：

```go
_ = os.RemoveAll(target)                      // 先删除旧版本
if err := os.Rename(staging, target); err != nil {
    if copyErr := copyTree(staging, target); copyErr != nil {
        _ = os.RemoveAll(staging)
        return fmt.Errorf("安装 Node 失败：%w", err)  // <-- 此时 target 已被删除且未重建
    }
    _ = os.RemoveAll(staging)
}
```

**问题**：`os.RemoveAll(target)` 在 `Rename` **之前**执行。若 `Rename` 与 `copyTree` 都失败（磁盘满、权限变更），则：
- 旧的 Node 安装**已被删除**；
- 新的**没有装上**；
- `installed.json` 中的记录**还在**（`saveState` 在 `placePackage` 之后才调用，所以这次不会写入新记录，但**上一次**的记录仍在）。

结果：`state.find(spec.ID)` 返回 `true`，但 `toolsPresent` 返回 `false`，于是下次 `Install` 会重新走完整下载流程 —— 这个恢复路径是通的。但在此之前，**运行环境页会显示「已安装」而工具探测显示「未检测到」**，且所有依赖 node 的 stdio 上游全部失效。

**最优解**：调整为「先换后删」，保证任意时刻 target 都是完整的：

```go
target := filepath.Join(in.runtimeDir, RuntimeSubdirNode)
staging := target + ".staging"
backup := target + ".old"

_ = os.RemoveAll(staging)
_ = os.RemoveAll(backup)
if err := renameOrCopyTree(root, staging); err != nil {
    _ = os.RemoveAll(staging)
    return fmt.Errorf("安装 Node 失败：%w", err)
}
// 旧版本先改名保留，不直接删
hasOld := false
if _, err := os.Stat(target); err == nil {
    if err := os.Rename(target, backup); err != nil {
        _ = os.RemoveAll(staging)
        return fmt.Errorf("安装 Node 失败：无法暂存旧版本：%w", err)
    }
    hasOld = true
}
if err := os.Rename(staging, target); err != nil {
    // 回滚
    if hasOld { _ = os.Rename(backup, target) }
    _ = os.RemoveAll(staging)
    return fmt.Errorf("安装 Node 失败：%w", err)
}
if hasOld { _ = os.RemoveAll(backup) }  // 成功后才删旧版本
```

同样的问题存在于 `placeUV`（`_ = os.RemoveAll(target)` 后若 `copyFile` 失败，uv 就没了）。建议同样处理。

另外注意：`placeNode` 中失败分支 `return fmt.Errorf("安装 Node 失败：%w", err)` 包装的是 `Rename` 的 err，而 `copyTree` 的 `copyErr` 被丢弃了 —— 真正的失败原因（磁盘满）看不到。应改为 `%w` 包装 `copyErr` 或用 `errors.Join(err, copyErr)`。

---

### P2-6 `findSingleTopDir` 对多目录归档的回退可能选错根

**位置**：`internal/runtime/install.go:findSingleTopDir`

**现象**：

```go
if len(dirs) == 1 { return dirs[0], nil }
if len(entries) > 0 { return root, nil }   // 多个目录时直接返回 extract 根
```

**问题**：当归档解出多个顶层目录时（例如某些发行版打包了 `node-v22/` 和 `LICENSE/`），会返回 `extractDir` 本身作为 Node 根。随后 `placeNode` 检查 `root/bin` 或 `root/node.exe` —— 都不存在，返回「Node 发行包布局无法识别」。

这个错误信息对用户毫无帮助（他们无法控制官方 tarball 的结构）。虽然当前 catalog 里的固定版本不会触发，但**升级 catalog 版本时可能踩到**，且失败时已经下载了 100MB+。

**最优解**：在多目录时按特征查找，而非盲目回退：

```go
func findSingleTopDir(root string) (string, error) {
    entries, err := os.ReadDir(root)
    if err != nil { return "", err }
    var dirs []string
    for _, e := range entries {
        if e.IsDir() { dirs = append(dirs, filepath.Join(root, e.Name())) }
    }
    if len(dirs) == 1 { return dirs[0], nil }
    // 多目录：优先选包含 bin/ 或 node.exe 的那个
    for _, d := range dirs {
        if st, err := os.Stat(filepath.Join(d, "bin")); err == nil && st.IsDir() {
            return d, nil
        }
        if _, err := os.Stat(filepath.Join(d, "node.exe")); err == nil {
            return d, nil
        }
    }
    if len(entries) > 0 { return root, nil }
    return "", fmt.Errorf("空归档")
}
```

同时把 `placeNode` 的错误文案改为可诊断：

```go
return fmt.Errorf("Node 发行包布局无法识别（解压根 %s 下未找到 bin/ 或 node.exe），请反馈该版本以便修正内置目录", root)
```

---
## 五、P3 问题（可维护性 / 冗余 / 用户体验）

### P3-1 死代码与冗余（可直接删除）

| 位置 | 内容 | 处理 |
| --- | --- | --- |
| `internal/runtime/security_profile.go:782` | `func parseStringSlice(raw any) []string` 全项目无调用（已被严格版 `parseStrictStringSlice` 取代） | 删除 |
| `internal/runtime/process_unix.go:48` | `func processAlive(p *os.Process) bool` 无调用，注释写「供测试/兼容」但测试也没用 | 删除 |
| `internal/runtime/install.go:415` | `_ = binDir` —— `binDir` 变量在 `placeNode` 中计算后从未真正使用 | 删除变量与该行 |
| `internal/runtime/httpclient.go:45` | `var _ = time.Minute` 注释说「避免未使用 time 告警」，但 `defaultInstallTimeout` 已经用到 `time`，该行多余 | 删除 |
| `internal/runtime/path_lookup.go` | `findExecutableInDir` 中的空 `if` 块（仅含注释） | 按 P1-3 改为返回可执行位状态 |
| `internal/runtime/security_profile.go` | `ValidateSecurityProfile` 中的空分支：`if p.Mode == SecurityModeUnrestricted && p.Note == "" { // 不强制 note }` | 删除该 if |

这些都是 `go vet` 不会报、但影响可读性的残留。建议一并清理。

---

### P3-2 `ResolveEffectiveSecurity` 中的 StrictPathOnly 分支是恒等赋值

**位置**：`internal/runtime/security_profile.go`

**现象**：

```go
if mode == SecurityModeStrict {
    eff.ProcessHardening = true
    if !policy.StrictPathOnlyRuntime {
        eff.StrictPathOnly = false
    } else {
        eff.StrictPathOnly = true
    }
}
```

`eff.StrictPathOnly` 在函数开头已经赋值为 `policy.StrictPathOnlyRuntime`，这个 if-else 是**完全等价的重复赋值**，只是把同一个值再写一遍。注释「仅在缺省未配置时偏安全为 true」描述的行为并未实现（`policy.StrictPathOnlyRuntime` 是 bool，无法区分「缺省」与「显式 false」，配置层已用 `*bool` 处理并回填了 true）。

**最优解**：直接删除 if-else，只保留 `eff.ProcessHardening = true`：

```go
if mode == SecurityModeStrict {
    // 严格档强制进程加固；StrictPathOnly 已在上方跟随全局开关赋值。
    eff.ProcessHardening = true
}
```

---

### P3-3 `runtimeRequirements.ts` 与后端 `requirements.go` 逻辑重复实现

**位置**：`web/src/utils/runtimeRequirements.ts` 对比 `internal/runtime/requirements.go`

**现象**：`inferToolsFromCommand`、`inferToolsFromTemplateRuntimes`、`resolveEffectiveTools` 三个函数在前后端各实现一遍，映射表（`npx → [node, npx]` 等）硬编码两份。

**问题**：这是**双维护点**。新增一种运行时（例如 `bun` 或 `deno`）必须同步改两处，漏改会导致前端预览与后端预检结论不一致。当前两份实现确实是等价的，但缺少任何机制保证它们保持同步。

**最优解**（按侵入性从低到高，建议选 1）：

1. **让前端不再自己推断**。前端已经会调 `/runtime/preflight`，后端返回的 `PreflightResult` 里就有 `suggestedTools` 与 `requirements.tools`。前端只需展示后端结果，删除本地推断逻辑。仅在**输入防抖期间**用本地推断做乐观预览时才需要保留 —— 若确实需要，把它降级为纯展示提示，并加注释说明「以后端 preflight 为准」。

2. 若必须保留本地推断，则把映射表从 `/runtime/tools` 接口下发（`KnownTool` 结构增加 `inferFrom: string[]` 字段），前端据此构建映射，消除硬编码。

考虑到 AGENTS.md 里「不冗余设计」的要求，**方案 1 更契合**：前端已有 preflight 调用链路，本地推断属于重复实现。

---

### P3-4 `RuntimeEnvironmentView` 缺少安装进度反馈

**位置**：`web/src/views/RuntimeEnvironmentView.vue`

**现象**：安装按钮点击后仅显示旋转图标 + 「安装中…」，前端超时设为 12 分钟（`installRuntimePackage` 的 `timeout: 12 * 60 * 1000`）。

**问题**：Node.js 完整包约 50MB，慢速网络下可能需要数分钟。用户在这段时间里：
- 看不到下载进度、已下载字节数、当前阶段（下载中 / 校验中 / 解压中）；
- 不知道是否卡死；
- 无法取消（后端 `Install` 接受 ctx，但没有暴露取消入口）；
- 若刷新页面，安装仍在后端继续，但前端完全失去了状态（再次进入页面看不到「安装中」）。

对于一个可能持续数分钟的操作，这个体验不足。

**最优解**（按投入产出排序）：

1. **最小改进**：`Installer` 增加一个进度状态字段（受 `instMu` 保护），`Summary` 里返回当前是否有安装在进行及其阶段；前端轮询 `summary`（3 秒）在卡片上显示阶段文案。这样刷新页面后状态可恢复。

```go
type InstallProgress struct {
    PackageID string `json:"packageId"`
    Phase     string `json:"phase"`     // downloading | verifying | extracting | placing
    Bytes     int64  `json:"bytes"`
    Total     int64  `json:"total"`     // 来自 Content-Length，可能为 0
    StartedAt string `json:"startedAt"`
}
```

`downloadFile` 中用一个自定义 `io.Writer` 包装来累加字节数（已有 `io.MultiWriter(f, h)`，再加一个 counter 即可，零额外开销）。

2. **进一步**：前端用 `EventSource`/轮询显示百分比进度条。

3. **取消能力**：把安装 ctx 的 `cancel` 存入 Service，暴露 `POST /runtime/install/cancel`。注意 `Installer.mu` 是串行锁，取消后需确保锁被释放（当前 `defer in.mu.Unlock()` 已保证）。

---
### P3-5 运行环境页信息层级偏平，缺少「我该做什么」的收敛

**位置**：`web/src/views/RuntimeEnvironmentView.vue`

**现象**：页面自上而下依次是：4 张概览卡 → 本地安全能力（3 格） → 补齐引导（条件显示） → 预置安装 → 宿主工具（8 格） → 安全说明（5~6 条 riskNotes）。

**问题**：

1. **`riskNotes` 是固定长文案**，5 条常驻文本占据整屏底部，但它们是**静态说明**而非当前状态的反馈。用户第二次进入页面时这些文字仍然全量展示，属于噪音。按 AGENTS.md「信息密度适中、避免无意义装饰」的要求，这部分应当可折叠。

2. **缺少一个明确的「当前状态结论」**。用户打开页面最想知道的是「我的 stdio 能不能用」，但这个结论要自己从 4 张卡片 + 8 个工具格子里拼出来。

3. **「本地安全能力」三格在无 bwrap 时全是「策略 xxx」**，对普通用户信息量为零，但占了一整行。

**最优解**：

1. 页面顶部增加一行**结论式状态条**（单行，非卡片），根据 summary 计算：

   - stdio 禁用 → 灰色：`本地 stdio 已禁用，仅可使用远程与 OpenAPI 上游` + 「去启用」按钮
   - `missingCount > 0` → 警告色：`缺少 N 个常用工具，部分 stdio 模板不可用` + 「一键安装 Node/uv」按钮
   - 全部就绪 → 成功色：`本地运行环境就绪，可创建 stdio 上游`

   这条状态条直接回答「我该做什么」，下方细节供需要时展开。

2. `riskNotes` 改为默认折叠的 `<details>`，标题「安全说明（N 条）」。保留内容但不占据默认视野。

3. 「本地安全能力」三格在 `filesystemIsolationSupported === false && networkIsolationSupported === false` 时，合并为一行简短说明 + 一个「如何启用内核隔离」的展开项（内容：在 Linux 容器安装 bubblewrap 的命令）。避免三个格子都写「策略 xxx」。

4. `runtimeGuideSteps` 当前把「预置安装」和「手动放入」混在一个有序列表里（`使用本页「预置安装」…，或手动将可执行文件放入 …`），第一步就有两个分支。建议拆成两条并列路径：「推荐：使用下方预置安装」与「高级：手动放入 bin 目录」。

---

### P3-6 前端 `catalog` 展示了 `assets` 中的完整下载 URL 与 SHA256

**位置**：`internal/runtime/install.go:CatalogWithStatus` → `web/src/api/runtime.ts:RuntimeCatalogPackage.assets`

**现象**：`CatalogPackage` 内嵌 `PackageSpec`，后者包含完整的 `Assets []PackageAsset`（含 `URL`、`SHA256`）。代码里有一行自问自答的注释：

```go
// 不向管理台回传完整 URL 列表？保留以便透明；资产 URL 是官方固定源。
```

**问题**：不是安全问题（URL 是公开的官方源），但：
- 每个包 3 个平台资产 × (URL + 64 字符 SHA256) ≈ 500 字节，2 个包约 1KB 冗余；
- `RuntimeSummary` 内嵌 `catalog`，所以**每次打开运行环境页都传输**这些数据；
- 前端 `RuntimeEnvironmentView.vue` **完全没有使用 `assets`**，只用了 `assetGoos`/`assetGoarch`（已单独提供）；
- 类型定义里 `assets?: RuntimePackageAsset[]` 是死字段。

**最优解**：这属于「不必要的接口暴露 + 冗余传输」。建议在响应中裁掉：

```go
// CatalogPackage 用于管理台展示，不内嵌完整资产列表。
type CatalogPackage struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Version     string   `json:"version"`
    Description string   `json:"description"`
    Kind        PackageKind `json:"kind"`
    Tools       []string `json:"tools"`
    Supported   bool     `json:"supported"`
    Installed   bool     `json:"installed"`
    InstalledAt string   `json:"installedAt,omitempty"`
    AssetGOOS   string   `json:"assetGoos,omitempty"`
    AssetGOARCH string   `json:"assetGoarch,omitempty"`
    // SourceHost 仅用于向用户展示来源可信度，不暴露完整 URL
    SourceHost  string   `json:"sourceHost,omitempty"`
}
```

`SourceHost` 取 `nodejs.org` / `github.com`，前端可在卡片上显示「来源：nodejs.org · SHA256 校验」，既保持透明度又不传冗余数据。同步删除 `web/src/api/runtime.ts` 中的 `RuntimePackageAsset` 接口与 `assets` 字段。

---

## 六、非运行环境部分的观察

由于本次审查重点在运行环境，其余模块只做了结构性抽查，以下为值得关注的点（未逐行验证，建议后续专项审查）：

### 6.1 前端超大单文件

| 文件 | 体积 | 建议 |
| --- | --- | --- |
| `web/src/components/upstreams/UpstreamFormDrawer.vue` | **114 KB** | 已拆出 `StdioLaunchPanel`/`StdioSecurityPanel`，但主体仍过大。建议继续按「传输类型」拆分：`HttpTransportFields.vue`、`OpenAPITransportFields.vue`、`CredentialFields.vue` |
| `web/src/views/UpstreamsView.vue` | 104 KB | 列表、详情抽屉、批量操作可拆分 |
| `web/src/views/APIServiceView.vue` | 77 KB | 同上 |
| `web/src/views/SettingsView.vue` | 69 KB | 按配置分组拆成多个 section 组件 |

这几个文件的体积会显著影响：编辑时的 IDE 响应、代码审查的可读性、以及 Vite HMR 的粒度。属于可维护性风险。

### 6.2 `internal/httpapi/router.go` 的 Deps 结构

`Deps` 有 **26 个字段**，`Router` 结构体同样庞大。这是组合层的必然结果，且 AGENTS.md 明确要求 httpapi 保持组合层定位，当前实现符合约定。但建议考虑按领域分组：

```go
type Deps struct {
    Core       CoreDeps       // Upstream, Aggregation, ToolCache, ...
    Governance GovernanceDeps // Rules, Filters, ToolPolicy
    Identity   IdentityDeps   // APIKeys, ACL, Auth, Security
    Ops        OpsDeps        // Stats, Audit, SystemLogs, Backup
    Runtime    RuntimeDeps    // RuntimeEnv, Scripts, Templates
}
```

属于 nice-to-have，不影响正确性。

### 6.3 建议的后续专项审查

- `internal/aggregation`：工具聚合与路由策略是核心链路，有较多 property test，值得单独 review 并发安全性
- `internal/apikey` + `internal/security`：鉴权、ACL、限流、自动封禁的组合逻辑
- `internal/store`：SQL 与迁移的一致性
- 前端 `UpstreamFormDrawer.vue` 的表单状态机（launchMode 切换时的字段清理逻辑，`delete obj.directoryRef` 等）

---
## 七、运行环境逻辑闭环性专项结论

针对「运行环境相关逻辑是否闭环」这一问题，逐链路核对如下：

### 7.1 闭环完好的链路 ✅

| 链路 | 闭环性 | 说明 |
| --- | --- | --- |
| 配置 → 策略 → 校验 → 启动 | ✅ | `policyFromCfg` 热读取 → `SetPolicyProvider` → `currentPolicy()` → `ValidateCommandForSecurity`，保存设置立即生效，无需重启 |
| 保存校验 ↔ 连接校验 | ✅ | `validate.go:validateStdioParams` 与 `session_stdio.go:Connect` 使用**同一组**校验函数（`ResolveEffectiveSecurity` + `ValidateCommandForSecurity` + `ValidateEffectiveSecurityWithCommand`），不存在「保存能过、连接失败」的错配 |
| preflight ↔ 实际启动 | ✅ | `EvaluatePreflight` 在严格档下显式复用 `ResolveCommandStrictRuntime`（与启动路径一致），非严格档复用 `LookPathWithPrefixes`。这一点做得好，注释也说明了意图 |
| 目录启动 fail-closed | ✅ | `resolveDirectoryLaunch` 重新扫描目录、忽略客户端 command/args/cwd；`ResolveExistingPathWithinRoots` 做符号链接真实路径校验 |
| 脚本启动 fail-closed | ✅ | `resolveManagedScript` 从脚本仓重新解析 + 哈希比对；`ResolveEntryPath` 二次校验内容哈希；`prepareVerifiedScript` 用 FD 钉死内容 |
| 安装 → 缓存失效 | ✅ | `InstallPackage`/`UninstallPackage` 成功后调用 `InvalidatePreflightCache()` |
| 脚本删除 → 引用检查 | ✅ | `deleteScript` 在 `scriptRefMu` 保护下检查上游引用，阻止悬空 scriptRef |
| PATH 劫持防护 | ✅ | 解析出绝对路径后**再次**按基名校验 allowlist |

### 7.2 闭环存在缺口的链路 ⚠️

| 链路 | 缺口 | 对应问题 |
| --- | --- | --- |
| 手动放置工具 → 探测状态 | 缓存不感知目录变化，运行环境页与上游表单结论矛盾 | P0-3、P1-6 |
| 探测「可用」→ 实际可执行 | 忽略可执行位，探测通过但 exec 失败 | P1-3 |
| 严格档选择 → 实际隔离强度 | 无 bwrap 时静默降级为策略校验，决策点无提示 | P0-2 |
| 目录可浏览 → 目录可启动 | 两套根不同，用户走到最后一步才失败 | P1-5 |
| bwrap 安装 → 能力生效 | `sync.Once` 永久缓存，需重启进程 | P2-2 |
| 安装失败 → 环境状态 | `placeNode` 先删后换，失败后旧版本丢失 | P2-5 |
| 脚本删除 → 磁盘清理 | rename 失败被吞，目录永久残留且占配额 | P1-7 |
| 全局默认改 unrestricted → 风险确认 | 绕过 `RequiresAck`，无任何确认痕迹 | P1-2 |

### 7.3 安全边界总体评估

**结论：设计意图正确，实现基本到位，但存在「宣称强度 > 实际强度」的表达问题。**

具体来说，纵深防御的各层都在：

1. **总开关**：`stdio_enabled`
2. **命令 denylist**：shell/脚本宿主/包装器，**不可通过 allowlist 放开**（正确）
3. **命令 allowlist**：按档位分层（standard 全量 / strict 交集 / unrestricted 仅 denylist）
4. **路径解析约束**：严格档仅 runtime 卷，含 EvalSymlinks 边界校验
5. **参数层**：npx/uvx 目标包白名单、自装包意图检测
6. **文件策略**：cwd allowlist（+ bwrap bind）
7. **网络策略**：deny（+ bwrap unshare-net）/ allowlist（声明）
8. **环境清理**：敏感变量剥离 + 严格档白名单继承
9. **进程加固**：进程组 + Pdeathsig（Linux）
10. **内容完整性**：脚本哈希钉死 + FD 执行

这是一个**认真设计过的**安全模型。主要问题不在于层数不够，而在于：

- **第 6、7 层在无 bwrap 时是「声明」而非「强制」**，但 UI 用了成功色的「严格安全」徽章（P0-2）；
- **第 5 层的自装包检测有已知绕过**（`uvx --with`，P1-1），而 UI 文案暗示了强约束；
- **第 9 层在 Windows 完全缺失**，但 `ProcessHardening` 仍为 true（P0-4）。

修复方向应当是**让 UI 如实反映实际强度**，而不是削弱功能。用户基于准确信息做的风险决策，比基于乐观描述做的决策安全得多。

---

## 八、修复优先级建议

### 当前修复状态

第一批至第四批已完成。每条问题均在修改前核对上下游行为，确认问题真实存在；修改后执行针对性测试、diff review 和必要的前端构建，并独立提交。当前未将后续观察项（如 P1-6、P2-6）纳入本轮修复。

| 批次 | 问题 | 状态 | 提交 | 主要验证 |
| --- | --- | --- | --- | --- |
| 第一批 | P0-2 严格档风险等级与隔离提示 | ✅ 已修复 | `e6e3047` | runtime / transport / 前端构建 |
| 第一批 | P0-1 敏感环境变量清理过宽 | ✅ 已修复 | `111679f` | runtime 测试 |
| 第一批 | P1-2 unrestricted 默认档风险确认 | ✅ 已修复 | `a526e62` | config / runtime / httpapi / 前端构建 |
| 第一批 | P1-1 严格档拒绝 uvx 附加依赖参数 | ✅ 已修复 | `ec3e52a` | runtime / transport / 前端构建 |
| 第二批 | P0-3 preflight 缓存隔离与目录指纹 | ✅ 已修复 | `1767ea3` | runtime / httpapi 测试 |
| 第二批 | P1-3 可执行位状态如实上报 | ✅ 已修复 | `3cad00b` | runtime 测试 / 前端构建 |
| 第二批 | P1-5 目录启动文件根前置校验 | ✅ 已修复 | `0ee0f81` | httpapi / transport / runtime / 前端构建 |
| 第二批 | P2-5 安装失败回滚 | ✅ 已修复 | `c4b4f72` | runtime 测试与 vet |
| 第二批 | P1-7 回收站移动失败可观测 | ✅ 已修复 | `22f218e` | scripts / httpapi / 前端构建 |
| 第三批 | P0-4 Windows 进程加固与 hardening 语义 | ✅ 已修复 | `b0b1c9b` | runtime / transport 测试；Windows 交叉编译 |
| 第三批 | P2-2 bwrap 探测 TTL 缓存 | ✅ 已修复 | `219dd34` | runtime 测试 |
| 第三批 | P2-1 / P2-3 PATH 缓存与 Summary 重复 IO | ✅ 已修复 | `29abb00`、`7edb454` | runtime / httpapi 测试 |
| 第三批 | P2-4 目录浏览排序后截断 | ✅ 已修复 | `fabc899` | httpapi / runtime 测试 |
| 第三批 | P1-4 manifest 单条 entry 部分成功 | ✅ 已修复 | `2da4125` | runtime 测试 |
| 第四批 | P3-1 清理运行时死代码 | ✅ 已修复 | `0839059`、`8927b11` | runtime / transport 测试 |
| 第四批 | P3-2 删除 StrictPathOnly 恒等分支 | ✅ 已修复 | `4b2cae2` | runtime / transport 测试 |
| 第四批 | P3-6 精简 catalog 接口字段 | ✅ 已修复 | `4ad42d3` | runtime / httpapi 测试；前端构建 |
| 第四批 | P3-3 统一运行时依赖推断元数据 | ✅ 已修复 | `55db61c` | runtime / httpapi 测试；前端构建 |
| 第四批 | P3-4 增加运行时安装进度反馈 | ✅ 已修复 | `39c6a54` | runtime / httpapi 测试；前端构建 |
| 第四批 | P3-5 收數运行环境页面信息层级 | ✅ 已修复 | `cf54192` | 前端类型检查与生产构建 |

当前剩余问题主要为 P1-6（手动放置工具后的显式重新探测入口）和 P2-6（多目录归档根目录识别），以及报告中未纳入本轮的后续专项观察项；第三批、第四批已修复条目不再列为剩余问题。

### 第一批（安全表达与正确性，建议本迭代完成）

1. ~~**P0-2** 调整 `ResolveEffectiveSecurity` 中 `RiskLevel` 与 `PolicyOnlyIsolation` 的计算顺序，并在 `StdioSecurityPanel` 增加仅策略运行的降级提示~~ ✅ 已修复（`e6e3047`）
2. ~~**P0-1** 收敛 `isSensitiveEnvKey` 的宽匹配，补 `NPM_CONFIG_REGISTRY` 保留用例~~ ✅ 已修复（`111679f`）
3. ~~**P1-2** 全局默认改为 unrestricted 时要求二次确认~~ ✅ 已修复（`a526e62`）
4. ~~**P1-1** `uvx --with` 系列参数在严格档直接拒绝；同步修正 `allowSelfInstall` 的 UI 文案~~ ✅ 已修复（`ec3e52a`）

### 第二批（闭环修复）

5. ~~**P0-3** preflight 缓存移入 Service + 缓存键加入目录指纹与隔离能力~~ ✅ 已修复（`1767ea3`）
6. ~~**P1-3** 可执行位状态如实上报到 UI~~ ✅ 已修复（`3cad00b`）
7. ~~**P1-5** 目录启动可用性前置提示 + 错误文案可操作化~~ ✅ 已修复（`0ee0f81`）
8. ~~**P2-5** `placeNode`/`placeUV` 改为先换后删~~ ✅ 已修复（`c4b4f72`）
9. ~~**P1-7** `SoftDelete` 失败可观测~~ ✅ 已修复（`22f218e`）

### 第三批（平台完备性与性能）

10. ~~**P0-4** Windows Job Object / 进程组 + `taskkill` 绝对路径 + hardening 语义统一~~ ✅ 已修复（`b0b1c9b`）
11. ~~**P2-2** bwrap 探测改 TTL 缓存~~ ✅ 已修复（`219dd34`）
12. ~~**P2-1 / P2-3** PATH 前缀缓存、`Summary` 减少重复 IO~~ ✅ 已修复（`29abb00`、`7edb454`）
13. ~~**P2-4** 目录浏览改为先排序后截断~~ ✅ 已修复（`fabc899`）
14. ~~**P1-4** manifest 单条 entry 失败降级为警告~~ ✅ 已修复（`2da4125`）

### 第四批（清理与体验）

15. ~~**P3-1** 删除死代码（6 处）~~ ✅ 已修复（`0839059`、`8927b11`）
16. ~~**P3-2** 删除恒等赋值分支~~ ✅ 已修复（`4b2cae2`）
17. ~~**P3-6** catalog 裁掉 assets 字段~~ ✅ 已修复（`4ad42d3`）
18. ~~**P3-3** 前端删除重复的工具推断逻辑，改用后端元数据与 preflight 结果~~ ✅ 已修复（`55db61c`）
19. ~~**P3-4** 安装进度反馈~~ ✅ 已修复（`39c6a54`）
20. ~~**P3-5** 运行环境页增加结论式状态条、riskNotes 折叠~~ ✅ 已修复（`cf54192`）

---

## 九、验证记录

本次审查执行的验证命令与结果：

```
go test ./...                                           ✅ 通过
npm run build（工作目录：web）                         ✅ 通过
git diff --check                                        ✅ 通过
```

说明：

- 报告中所有问题均基于**实际阅读源码**得出，文件路径与函数名已核对；
- 每条已修复问题均在提交前完成对应的上下游 review、针对性测试或构建验证；本轮最终再次执行 `go test ./...`、`npm run build` 和 `git diff --check`。
- `P1-6` 与 `P2-6` 尚未处理，仍保留在报告的后续问题清单中；非运行环境部分仍属于结构性观察，未被本轮状态更新误标为已修复。

---

## 十、附：本报告未覆盖的范围

诚实说明审查边界，避免给出超出实际检查深度的结论：

- **逐行审查**：`internal/runtime/**`、`internal/transport/{session_stdio,validate,security_hooks,directory_ref,script_ref,verified_script*}.go`、`internal/scripts/**`、`internal/httpapi/{runtime_env,fsbrowse,scripts}.go`、`internal/config/yaml.go`、`internal/app/build.go`、前端 runtime/fsbrowse 相关 api 与 utils、`RuntimeEnvironmentView.vue`、`StdioLaunchPanel.vue`、`StdioSecurityPanel.vue`
- **结构性抽查**：`internal/httpapi/router.go`、前端文件清单与体积
- **未审查**：`internal/aggregation`、`internal/apikey`、`internal/security`、`internal/store`、`internal/stats`、`internal/audit`、`internal/mcpapi`、`internal/xiaozhi`、`internal/manager`、`internal/sync`，以及前端大部分 views/components

第六节列出的非运行环境问题仅基于文件体积与接口签名的观察，**未做逻辑验证**，不应视为已确认的缺陷。如需覆盖这些模块，建议另行安排专项审查。

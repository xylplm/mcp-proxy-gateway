package runtime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// maxInstallBytes 单包下载上限（防止异常大文件占满磁盘）。
	maxInstallBytes = 512 << 20 // 512 MiB
	// maxExtractBytes 解压后总字节上限（防压缩炸弹）。
	maxExtractBytes = 1024 << 20 // 1 GiB
	// defaultInstallTimeout 单次安装超时。
	defaultInstallTimeout = 10 * time.Minute
	// installStateFile 安装状态清单。
	installStateFile = "installed.json"
)

// InstallRecord 记录已安装预置包。
type InstallRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Kind      string    `json:"kind"`
	Installed time.Time `json:"installedAt"`
	GOOS      string    `json:"goos"`
	GOARCH    string    `json:"goarch"`
	Tools     []string  `json:"tools"`
}

// InstallState 持久化在 runtime/state/installed.json。
type InstallState struct {
	Packages []InstallRecord `json:"packages"`
}

// InstallResult 为一次安装结果。
type InstallResult struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Tools      []string `json:"tools"`
	RuntimeDir string   `json:"runtimeDir"`
	Reused     bool     `json:"reused"`
}

type InstallProgress struct {
	PackageID string    `json:"packageId"`
	Phase     string    `json:"phase"`
	Bytes     int64     `json:"bytes"`
	Total     int64     `json:"total"`
	StartedAt time.Time `json:"startedAt"`
}

// maxInstallLogs 为环形缓冲保留的最近日志条数（避免长时间运行后内存膨胀）。
const maxInstallLogs = 200

// InstallLogLevel 为日志条目的严重级别。
type InstallLogLevel string

const (
	InstallLogInfo    InstallLogLevel = "info"
	InstallLogSuccess InstallLogLevel = "success"
	InstallLogError   InstallLogLevel = "error"
)

// InstallLogEntry 为一条结构化安装日志（供管理台展示与排查）。
type InstallLogEntry struct {
	Phase   string          `json:"phase"`
	Level   InstallLogLevel `json:"level"`
	Message string          `json:"message"`
	Source  string          `json:"source,omitempty"` // 下载源 URL 或镜像 host
	Bytes   int64           `json:"bytes,omitempty"`
	At      time.Time       `json:"at"`
}

// Installer 负责受控预置包安装到 runtimeDir。
type Installer struct {
	runtimeDir string
	client     *http.Client
	// catalog 可注入以便测试；nil 使用 DefaultCatalog。
	catalog func() []PackageSpec
	// now 可注入时间。
	now func() time.Time

	mu         sync.Mutex // 串行化安装，避免并发写同一目录
	progressMu sync.RWMutex
	progress   *InstallProgress
	lastError  string // 最近一次安装失败原因（progress 清空后仍保留，供前端展示）
	logMu      sync.RWMutex
	logs       []InstallLogEntry
}

// NewInstaller 构造安装器；client 可空（使用官方源白名单客户端）。
func NewInstaller(runtimeDir string, client *http.Client) *Installer {
	if client == nil {
		client = newInstallHTTPClient()
	}
	return &Installer{
		runtimeDir: strings.TrimSpace(runtimeDir),
		client:     client,
		catalog:    DefaultCatalog,
		now:        time.Now,
	}
}

// PreviewInstall 校验是否可安装（不下载）。
func (in *Installer) PreviewInstall(packageID string) (CatalogPackage, error) {
	if strings.TrimSpace(in.runtimeDir) == "" {
		return CatalogPackage{}, fmt.Errorf("运行时目录未配置")
	}
	spec, ok := in.findPackage(packageID)
	if !ok {
		return CatalogPackage{}, fmt.Errorf("未知预置包 %q，仅可安装内置目录中的包", packageID)
	}
	asset, ok := SelectAsset(spec, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return CatalogPackage{}, fmt.Errorf("预置包 %q 不支持当前平台 %s/%s", packageID, runtime.GOOS, runtime.GOARCH)
	}
	state := in.loadState()
	installed, at := state.find(spec.ID)
	return CatalogPackage{
		ID:          spec.ID,
		Name:        spec.Name,
		Version:     spec.Version,
		Description: spec.Description,
		Kind:        spec.Kind,
		Tools:       append([]string{}, spec.Tools...),
		Supported:   true,
		Installed:   installed,
		InstalledAt: at,
		AssetGOOS:   asset.GOOS,
		AssetGOARCH: asset.GOARCH,
	}, nil
}

// Install 下载、校验、解压预置包到 runtimeDir（幂等：同版本已装则跳过）。
func (in *Installer) Install(ctx context.Context, packageID string) (InstallResult, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	pkgID := strings.TrimSpace(packageID)
	in.clearLogs()
	in.setProgress(&InstallProgress{PackageID: pkgID, Phase: "preparing", StartedAt: time.Now().UTC()})
	defer in.setProgress(nil)
	in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogInfo, Message: "开始安装 " + pkgID})

	if strings.TrimSpace(in.runtimeDir) == "" {
		in.setLastError("运行时目录未配置")
		in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogError, Message: "运行时目录未配置"})
		return InstallResult{}, fmt.Errorf("运行时目录未配置")
	}
	if err := EnsureRuntimeLayout(in.runtimeDir); err != nil {
		msg := fmt.Sprintf("准备运行时目录失败：%v", err)
		in.setLastError(msg)
		in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogError, Message: msg})
		return InstallResult{}, fmt.Errorf("准备运行时目录失败：%w", err)
	}

	spec, ok := in.findPackage(packageID)
	if !ok {
		msg := fmt.Sprintf("未知预置包 %q，仅可安装内置目录中的包", packageID)
		in.setLastError(msg)
		in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogError, Message: msg})
		return InstallResult{}, fmt.Errorf("%s", msg)
	}
	asset, ok := SelectAsset(spec, runtime.GOOS, runtime.GOARCH)
	if !ok {
		msg := fmt.Sprintf("预置包 %q 不支持当前平台 %s/%s", packageID, runtime.GOOS, runtime.GOARCH)
		in.setLastError(msg)
		in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogError, Message: msg})
		return InstallResult{}, fmt.Errorf("%s", msg)
	}

	state := in.loadState()
	if ok, _ := state.find(spec.ID); ok {
		// 若关键工具已在 PATH 前缀中可用，视为复用。
		if in.toolsPresent(spec.Tools) {
			in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogSuccess, Message: spec.Name + " 已存在且工具可用，跳过下载"})
			return InstallResult{
				ID:         spec.ID,
				Name:       spec.Name,
				Version:    spec.Version,
				Tools:      append([]string{}, spec.Tools...),
				RuntimeDir: in.runtimeDir,
				Reused:     true,
			}, nil
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultInstallTimeout)
		defer cancel()
	}

	tmpDir, err := os.MkdirTemp(filepath.Join(in.runtimeDir, RuntimeSubdirCache), "install-")
	if err != nil {
		// cache 可能不存在
		_ = os.MkdirAll(filepath.Join(in.runtimeDir, RuntimeSubdirCache), 0o755)
		tmpDir, err = os.MkdirTemp(filepath.Join(in.runtimeDir, RuntimeSubdirCache), "install-")
		if err != nil {
			msg := fmt.Sprintf("创建临时目录失败：%v", err)
			in.setLastError(msg)
			in.addLog(InstallLogEntry{Phase: "preparing", Level: InstallLogError, Message: msg})
			return InstallResult{}, fmt.Errorf("创建临时目录失败：%w", err)
		}
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, "package"+extForFormat(asset.Format))
	in.updateProgressPhase("downloading")
	if err := in.downloadWithFallback(ctx, asset.downloadSources(), archivePath, asset.SHA256); err != nil {
		in.setLastError(err.Error())
		// downloadWithFallback 内部已逐源记录日志，这里补一条汇总。
		in.addLog(InstallLogEntry{Phase: "downloading", Level: InstallLogError, Message: err.Error()})
		return InstallResult{}, err
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return InstallResult{}, err
	}
	switch strings.ToLower(asset.Format) {
	case "tar.gz", "tgz":
		in.updateProgressPhase("extracting")
		in.addLog(InstallLogEntry{Phase: "extracting", Level: InstallLogInfo, Message: "正在解压 tar.gz"})
		if err := extractTarGz(archivePath, extractDir); err != nil {
			msg := fmt.Sprintf("解压失败：%v", err)
			in.setLastError(msg)
			in.addLog(InstallLogEntry{Phase: "extracting", Level: InstallLogError, Message: msg})
			return InstallResult{}, fmt.Errorf("解压失败：%w", err)
		}
	case "zip":
		in.updateProgressPhase("extracting")
		in.addLog(InstallLogEntry{Phase: "extracting", Level: InstallLogInfo, Message: "正在解压 zip"})
		if err := extractZip(archivePath, extractDir); err != nil {
			msg := fmt.Sprintf("解压失败：%v", err)
			in.setLastError(msg)
			in.addLog(InstallLogEntry{Phase: "extracting", Level: InstallLogError, Message: msg})
			return InstallResult{}, fmt.Errorf("解压失败：%w", err)
		}
	default:
		msg := fmt.Sprintf("不支持的包格式 %q", asset.Format)
		in.setLastError(msg)
		in.addLog(InstallLogEntry{Phase: "extracting", Level: InstallLogError, Message: msg})
		return InstallResult{}, fmt.Errorf("%s", msg)
	}

	in.updateProgressPhase("placing")
	if err := in.placePackage(spec, extractDir); err != nil {
		in.setLastError(err.Error())
		in.addLog(InstallLogEntry{Phase: "placing", Level: InstallLogError, Message: err.Error()})
		return InstallResult{}, err
	}

	rec := InstallRecord{
		ID:        spec.ID,
		Name:      spec.Name,
		Version:   spec.Version,
		Kind:      string(spec.Kind),
		Installed: in.now().UTC(),
		GOOS:      asset.GOOS,
		GOARCH:    asset.GOARCH,
		Tools:     append([]string{}, spec.Tools...),
	}
	state.upsert(rec)
	if err := in.saveState(state); err != nil {
		msg := fmt.Sprintf("写入安装状态失败：%v", err)
		in.setLastError(msg)
		in.addLog(InstallLogEntry{Phase: "placing", Level: InstallLogError, Message: msg})
		return InstallResult{}, fmt.Errorf("写入安装状态失败：%w", err)
	}

	in.addLog(InstallLogEntry{
		Phase:   "placing",
		Level:   InstallLogSuccess,
		Message: spec.Name + " " + spec.Version + " 安装完成",
	})
	return InstallResult{
		ID:         spec.ID,
		Name:       spec.Name,
		Version:    spec.Version,
		Tools:      append([]string{}, spec.Tools...),
		RuntimeDir: in.runtimeDir,
		Reused:     false,
	}, nil
}

func (in *Installer) setProgress(progress *InstallProgress) {
	in.progressMu.Lock()
	if progress == nil {
		in.progress = nil
	} else {
		copy := *progress
		in.progress = &copy
		// 开始新安装时清空上一次错误。
		in.lastError = ""
	}
	in.progressMu.Unlock()
}

// setLastError 记录安装失败原因（progress 清空后仍保留）。
func (in *Installer) setLastError(msg string) {
	in.progressMu.Lock()
	in.lastError = msg
	in.progressMu.Unlock()
}

func (in *Installer) updateProgressPhase(phase string) {
	in.progressMu.Lock()
	if in.progress != nil {
		in.progress.Phase = phase
	}
	in.progressMu.Unlock()
}

func (in *Installer) updateProgressTotal(total int64) {
	in.progressMu.Lock()
	if in.progress != nil {
		in.progress.Total = total
	}
	in.progressMu.Unlock()
}

func (in *Installer) addProgressBytes(n int64) {
	in.progressMu.Lock()
	if in.progress != nil {
		in.progress.Bytes += n
	}
	in.progressMu.Unlock()
}

// currentProgressBytes 返回当前已下载字节数（只读快照）。
func (in *Installer) currentProgressBytes() int64 {
	in.progressMu.RLock()
	defer in.progressMu.RUnlock()
	if in.progress == nil {
		return 0
	}
	return in.progress.Bytes
}

func (in *Installer) currentProgress() *InstallProgress {
	in.progressMu.RLock()
	defer in.progressMu.RUnlock()
	if in.progress == nil {
		return nil
	}
	copy := *in.progress
	return &copy
}

// lastInstallError 返回最近一次安装失败原因（无进度时仍保留，供前端排查）。
func (in *Installer) lastInstallError() string {
	in.progressMu.RLock()
	defer in.progressMu.RUnlock()
	return in.lastError
}

// addLog 追加一条安装日志，环形缓冲保留最近 maxInstallLogs 条。
func (in *Installer) addLog(entry InstallLogEntry) {
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	in.logMu.Lock()
	defer in.logMu.Unlock()
	in.logs = append(in.logs, entry)
	if len(in.logs) > maxInstallLogs {
		// 丢弃最旧的一批，避免每次安装切片膨胀。
		in.logs = append([]InstallLogEntry(nil), in.logs[len(in.logs)-maxInstallLogs:]...)
	}
}

// clearLogs 清空历史日志（新一轮安装开始时调用，避免新旧混淆）。
func (in *Installer) clearLogs() {
	in.logMu.Lock()
	defer in.logMu.Unlock()
	in.logs = nil
}

// Logs 返回安装日志的副本（稳定顺序，最早在前）。
func (in *Installer) Logs() []InstallLogEntry {
	in.logMu.RLock()
	defer in.logMu.RUnlock()
	if len(in.logs) == 0 {
		return nil
	}
	out := make([]InstallLogEntry, len(in.logs))
	copy(out, in.logs)
	return out
}

type installProgressWriter struct {
	installer *Installer
}

func (w installProgressWriter) Write(p []byte) (int, error) {
	w.installer.addProgressBytes(int64(len(p)))
	return len(p), nil
}

// Uninstall 移除预置包落盘内容并更新状态（仅允许 catalog 内 id）。
func (in *Installer) Uninstall(packageID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()

	spec, ok := in.findPackage(packageID)
	if !ok {
		return fmt.Errorf("未知预置包 %q", packageID)
	}
	if strings.TrimSpace(in.runtimeDir) == "" {
		return fmt.Errorf("运行时目录未配置")
	}

	switch spec.Kind {
	case PackageKindNode:
		_ = os.RemoveAll(filepath.Join(in.runtimeDir, RuntimeSubdirNode))
	case PackageKindUV:
		_ = os.RemoveAll(filepath.Join(in.runtimeDir, RuntimeSubdirUV))
		// 清理 bin 下可能由 uv 安装的 shim（仅删除同名工具，避免误删用户文件时过于激进：只删 uv/uvx）
		for _, name := range []string{"uv", "uvx", "uv.exe", "uvx.exe"} {
			_ = os.Remove(filepath.Join(in.runtimeDir, RuntimeSubdirBin, name))
		}
	default:
		return fmt.Errorf("不支持卸载类型 %q", spec.Kind)
	}

	state := in.loadState()
	state.remove(spec.ID)
	return in.saveState(state)
}

// ListInstalled 返回已安装记录。
func (in *Installer) ListInstalled() []InstallRecord {
	return in.loadState().Packages
}

// CatalogWithStatus 合并目录与安装状态。
func (in *Installer) CatalogWithStatus() []CatalogPackage {
	return in.catalogWithState(in.loadState())
}

func (in *Installer) catalogWithState(state InstallState) []CatalogPackage {
	out := make([]CatalogPackage, 0, len(DefaultCatalog()))
	list := DefaultCatalog()
	if in.catalog != nil {
		list = in.catalog()
	}
	for _, spec := range list {
		asset, supported := SelectAsset(spec, runtime.GOOS, runtime.GOARCH)
		installed, at := state.find(spec.ID)
		item := CatalogPackage{
			ID:          spec.ID,
			Name:        spec.Name,
			Version:     spec.Version,
			Description: spec.Description,
			Kind:        spec.Kind,
			Tools:       append([]string{}, spec.Tools...),
			Supported:   supported,
			Installed:   installed,
			InstalledAt: at,
		}
		if supported {
			item.AssetGOOS = asset.GOOS
			item.AssetGOARCH = asset.GOARCH
		}
		// 不向管理台回传完整 URL 列表？保留以便透明；资产 URL 是官方固定源。
		out = append(out, item)
	}
	return out
}

func (in *Installer) findPackage(id string) (PackageSpec, bool) {
	id = strings.TrimSpace(id)
	list := DefaultCatalog()
	if in != nil && in.catalog != nil {
		list = in.catalog()
	}
	for _, p := range list {
		if p.ID == id {
			return p, true
		}
	}
	return PackageSpec{}, false
}

func (in *Installer) toolsPresent(tools []string) bool {
	prefixes := PathPrefixes(in.runtimeDir)
	for _, tool := range tools {
		if _, err := LookPathWithPrefixes(tool, prefixes, nil); err != nil {
			return false
		}
	}
	return true
}

// downloadSource 描述一个可尝试的下载来源（官方优先，镜像兜底）。
type downloadSource struct {
	url    string
	host   string // 预解析主机名，供日志展示；空则从 url 解析
	mirror bool   // 是否镜像源
}

type downloadAttempt struct {
	url    string
	mirror bool
	err    error
}

// downloadWithFallback 依次尝试官方源与镜像源；首个成功（通过 SHA256 校验）即返回。
// 全部失败时返回聚合错误，列出每次尝试的目标与原因，便于排查被墙/超时。
func (in *Installer) downloadWithFallback(ctx context.Context, sources []downloadSource, dest, wantSHA string) error {
	wantSHA = strings.ToLower(strings.TrimSpace(wantSHA))
	if wantSHA == "" || len(wantSHA) != 64 {
		return fmt.Errorf("预置包缺少有效 SHA256")
	}
	if len(sources) == 0 {
		return fmt.Errorf("预置包缺少下载地址")
	}
	in.addLog(InstallLogEntry{
		Phase:   "downloading",
		Level:   InstallLogInfo,
		Message: fmt.Sprintf("共 %d 个下载源（官方优先，镜像兜底）", len(sources)),
	})
	var attempts []downloadAttempt
	for _, src := range sources {
		label := "官方源"
		if src.mirror {
			label = "镜像源"
		}
		start := time.Now()
		in.addLog(InstallLogEntry{Phase: "downloading", Level: InstallLogInfo, Message: label + " 开始下载", Source: src.url})
		err := in.downloadFile(ctx, src.url, dest, wantSHA)
		if err == nil {
			in.addLog(InstallLogEntry{
				Phase:   "downloading",
				Level:   InstallLogSuccess,
				Message: fmt.Sprintf("%s 下载完成（用时 %s）", label, time.Since(start).Round(time.Millisecond)),
				Source:  src.url,
				Bytes:   in.currentProgressBytes(),
			})
			return nil
		}
		in.addLog(InstallLogEntry{
			Phase:   "downloading",
			Level:   InstallLogError,
			Message: fmt.Sprintf("%s 失败：%v", label, err),
			Source:  src.url,
		})
		attempts = append(attempts, downloadAttempt{url: src.url, mirror: src.mirror, err: err})
	}
	// 聚合失败原因，明确列出每个源，避免「下载失败」后无从判断是被墙还是校验错。
	var b strings.Builder
	b.WriteString("所有下载源均失败：")
	for i, a := range attempts {
		if i > 0 {
			b.WriteString("；")
		}
		label := "官方源"
		if a.mirror {
			label = "镜像源"
		}
		fmt.Fprintf(&b, "%s %s → %v", label, a.url, a.err)
	}
	return fmt.Errorf("%s", b.String())
}

func (in *Installer) downloadFile(ctx context.Context, rawURL, dest, wantSHA string) error {
	wantSHA = strings.ToLower(strings.TrimSpace(wantSHA))
	if wantSHA == "" || len(wantSHA) != 64 {
		return fmt.Errorf("预置包缺少有效 SHA256")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求失败：%w", err)
	}
	if req.URL == nil {
		return fmt.Errorf("下载 URL 无效")
	}
	host := strings.ToLower(req.URL.Hostname())
	isLoopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
	// 生产 catalog 仅 HTTPS 官方源；单测 httptest 允许 loopback HTTP。
	if !isLoopback && !strings.EqualFold(req.URL.Scheme, "https") {
		return fmt.Errorf("仅允许 HTTPS 下载预置包")
	}
	if !isLoopback && !allowedInstallHost(host) {
		return fmt.Errorf("下载主机不在允许列表：%s", host)
	}
	req.Header.Set("User-Agent", "mcp-proxy-gateway-runtime-installer/1.0")

	resp, err := in.client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	in.updateProgressTotal(resp.ContentLength)

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	limited := io.LimitReader(resp.Body, maxInstallBytes+1)
	written, err := io.Copy(io.MultiWriter(f, h, installProgressWriter{installer: in}), limited)
	if err != nil {
		return fmt.Errorf("写入下载文件失败：%w", err)
	}
	if written > maxInstallBytes {
		return fmt.Errorf("下载文件超过大小上限（%d 字节）", maxInstallBytes)
	}
	in.updateProgressPhase("verifying")
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != wantSHA {
		return fmt.Errorf("校验和不匹配：期望 %s，实际 %s", wantSHA, sum)
	}
	return nil
}

func (in *Installer) placePackage(spec PackageSpec, extractDir string) error {
	switch spec.Kind {
	case PackageKindNode:
		return in.placeNode(extractDir)
	case PackageKindUV:
		return in.placeUV(extractDir)
	default:
		return fmt.Errorf("未知包类型 %q", spec.Kind)
	}
}

func (in *Installer) placeNode(extractDir string) error {
	// 官方 tarball/zip 顶层为 node-vX.Y.Z-...
	root, err := findSingleTopDir(extractDir)
	if err != nil {
		return err
	}
	// 需要 node/bin 或 Windows 根目录下 node.exe
	if _, err := os.Stat(filepath.Join(root, "bin")); err != nil {
		// Windows zip: 可执行文件在根目录
		if _, err2 := os.Stat(filepath.Join(root, "node.exe")); err2 != nil {
			return fmt.Errorf("Node 发行包布局无法识别")
		}
		// Normalize the Windows layout while the package is still in the
		// extraction directory, so a preparation failure cannot affect target.
		binDir := filepath.Join(root, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return fmt.Errorf("准备 Node bin 目录失败：%w", err)
		}
		for _, name := range []string{"node.exe", "npm.cmd", "npx.cmd", "node", "npm", "npx"} {
			src := filepath.Join(root, name)
			if st, statErr := os.Stat(src); statErr == nil && !st.IsDir() {
				if copyErr := copyFile(src, filepath.Join(binDir, name), st.Mode()); copyErr != nil {
					return fmt.Errorf("准备 Node 可执行文件失败：%w", copyErr)
				}
			}
		}
	}
	target := filepath.Join(in.runtimeDir, RuntimeSubdirNode)
	staging := target + ".staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// 先落到 staging，成功后再原子替换，避免半安装状态。
	if err := renameOrCopyTree(root, staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("安装 Node 失败：%w", err)
	}
	if err := replaceStagedTree(staging, target); err != nil {
		return fmt.Errorf("安装 Node 失败：%w", err)
	}
	return nil
}

func (in *Installer) placeUV(extractDir string) error {
	// uv 压缩包通常直接包含 uv / uvx 可执行文件
	candidates, err := findExecutablesNamed(extractDir, []string{"uv", "uvx", "uv.exe", "uvx.exe"})
	if err != nil || len(candidates) == 0 {
		return fmt.Errorf("uv 发行包中未找到可执行文件")
	}
	target := filepath.Join(in.runtimeDir, RuntimeSubdirUV)
	staging := target + ".staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(filepath.Join(staging, "bin"), 0o755); err != nil {
		return err
	}
	binRoot := filepath.Join(in.runtimeDir, RuntimeSubdirBin)
	if err := os.MkdirAll(binRoot, 0o755); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("准备 uv 工具目录失败：%w", err)
	}
	shimStaging, err := os.MkdirTemp(filepath.Dir(binRoot), ".uv-bin-staging-")
	if err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("创建 uv shim 临时目录失败：%w", err)
	}
	defer os.RemoveAll(shimStaging)

	for _, src := range candidates {
		base := filepath.Base(src)
		dst := filepath.Join(staging, "bin", base)
		st, _ := os.Stat(src)
		mode := os.FileMode(0o755)
		if st != nil {
			mode = st.Mode()
		}
		if err := copyFile(src, dst, mode); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
		if err := copyFile(src, filepath.Join(shimStaging, base), mode); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("准备 uv 工具 shim 失败：%w", err)
		}
	}
	replacements := make([]fileReplacement, 0, len(candidates))
	for _, src := range candidates {
		base := filepath.Base(src)
		replacement, err := replaceStagedFileWithBackup(filepath.Join(shimStaging, base), filepath.Join(binRoot, base))
		if err != nil {
			rollbackFileReplacements(replacements)
			return fmt.Errorf("更新 uv 工具 shim 失败：%w", err)
		}
		replacements = append(replacements, replacement)
	}
	if err := replaceStagedTree(staging, target); err != nil {
		rollbackFileReplacements(replacements)
		return fmt.Errorf("安装 uv 运行时失败：%w", err)
	}
	for _, replacement := range replacements {
		if replacement.backup != "" {
			_ = os.Remove(replacement.backup)
		}
	}
	return nil
}

func (in *Installer) statePath() string {
	return filepath.Join(in.runtimeDir, RuntimeSubdirState, installStateFile)
}

func (in *Installer) loadState() InstallState {
	path := in.statePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return InstallState{Packages: []InstallRecord{}}
	}
	var st InstallState
	if err := json.Unmarshal(b, &st); err != nil {
		return InstallState{Packages: []InstallRecord{}}
	}
	if st.Packages == nil {
		st.Packages = []InstallRecord{}
	}
	return st
}

func (in *Installer) saveState(st InstallState) error {
	path := in.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "installed-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows 上目标存在时 Rename 可能失败，先删再改。
	_ = os.Remove(path)
	return os.Rename(tmpName, path)
}

func (st *InstallState) find(id string) (bool, string) {
	for _, p := range st.Packages {
		if p.ID == id {
			if p.Installed.IsZero() {
				return true, ""
			}
			return true, p.Installed.UTC().Format(time.RFC3339)
		}
	}
	return false, ""
}

func (st *InstallState) upsert(rec InstallRecord) {
	for i, p := range st.Packages {
		if p.ID == rec.ID {
			st.Packages[i] = rec
			return
		}
	}
	st.Packages = append(st.Packages, rec)
}

func (st *InstallState) remove(id string) {
	out := st.Packages[:0]
	for _, p := range st.Packages {
		if p.ID != id {
			out = append(out, p)
		}
	}
	st.Packages = out
}

func extForFormat(format string) string {
	switch strings.ToLower(format) {
	case "zip":
		return ".zip"
	default:
		return ".tar.gz"
	}
}

func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		n, err := writeTarEntry(dest, hdr, tr, &total)
		if err != nil {
			return err
		}
		total += n
		if total > maxExtractBytes {
			return fmt.Errorf("解压内容超过大小上限（%d 字节）", maxExtractBytes)
		}
	}
	return nil
}

func writeTarEntry(dest string, hdr *tar.Header, r io.Reader, total *int64) (int64, error) {
	// 防止 zip-slip
	target, err := safeJoin(dest, hdr.Name)
	if err != nil {
		return 0, err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return 0, os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, err
		}
		mode := os.FileMode(hdr.Mode) & 0o777
		if mode == 0 {
			mode = 0o644
		}
		// 去掉 setuid/setgid/sticky 高位（已用 &0o777）
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return 0, err
		}
		// 限制单文件与累计解压体积
		remain := int64(maxExtractBytes)
		if total != nil {
			remain = int64(maxExtractBytes) - *total
			if remain < 0 {
				remain = 0
			}
		}
		written, copyErr := io.Copy(f, io.LimitReader(r, remain+1))
		closeErr := f.Close()
		if copyErr != nil {
			return written, copyErr
		}
		if closeErr != nil {
			return written, closeErr
		}
		if written > remain {
			return written, fmt.Errorf("解压内容超过大小上限（%d 字节）", maxExtractBytes)
		}
		return written, nil
	case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		// 跳过链接与设备节点，降低攻击面。
		return 0, nil
	default:
		return 0, nil
	}
}

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	var total int64
	for _, f := range r.File {
		n, err := writeZipEntry(dest, f, &total)
		if err != nil {
			return err
		}
		total += n
		if total > maxExtractBytes {
			return fmt.Errorf("解压内容超过大小上限（%d 字节）", maxExtractBytes)
		}
	}
	return nil
}

func writeZipEntry(dest string, f *zip.File, total *int64) (int64, error) {
	// 拒绝符号链接等特殊模式
	mode := f.Mode()
	if mode&os.ModeSymlink != 0 {
		return 0, nil
	}
	if !mode.IsRegular() && !f.FileInfo().IsDir() {
		// 非普通文件/目录跳过
		if !f.FileInfo().IsDir() {
			return 0, nil
		}
	}
	target, err := safeJoin(dest, f.Name)
	if err != nil {
		return 0, err
	}
	if f.FileInfo().IsDir() {
		return 0, os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	// 仅保留权限位，去掉 setuid 等
	fileMode := mode.Perm()
	if fileMode == 0 {
		fileMode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
	if err != nil {
		return 0, err
	}
	remain := int64(maxExtractBytes)
	if total != nil {
		remain = int64(maxExtractBytes) - *total
		if remain < 0 {
			remain = 0
		}
	}
	written, copyErr := io.Copy(out, io.LimitReader(rc, remain+1))
	closeErr := out.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if closeErr != nil {
		return written, closeErr
	}
	if written > remain {
		return written, fmt.Errorf("解压内容超过大小上限（%d 字节）", maxExtractBytes)
	}
	return written, nil
}

func safeJoin(base, name string) (string, error) {
	// 清理并确保结果仍在 base 下。
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法归档路径 %q", name)
	}
	// 绝对路径拒绝
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("非法归档绝对路径 %q", name)
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("非法归档路径 %q", name)
	}
	return target, nil
}

func findSingleTopDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	if len(dirs) == 1 {
		return dirs[0], nil
	}
	// 若直接就是内容
	if len(entries) > 0 {
		return root, nil
	}
	return "", fmt.Errorf("空归档")
}

func findExecutablesNamed(root string, names []string) ([]string, error) {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[strings.ToLower(n)] = struct{}{}
	}
	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		base := strings.ToLower(filepath.Base(path))
		if _, ok := want[base]; ok {
			found = append(found, path)
		}
		return nil
	})
	return found, err
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func renameOrCopyTree(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// 跨卷时 fallback 复制
	return copyTree(src, dst)
}

// replaceStagedTree replaces target only after staging is complete. The old
// target is moved aside first so a failed replacement can restore it, which is
// especially important on Windows where renaming over an existing directory
// is not supported.
func replaceStagedTree(staging, target string) error {
	defer func() { _ = os.RemoveAll(staging) }()

	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			if err := os.Rename(staging, target); err != nil {
				return err
			}
			return nil
		}
		return err
	}

	backup, err := temporarySibling(target, "backup")
	if err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("备份现有运行时失败：%w", err)
	}

	if err := os.Rename(staging, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("替换运行时失败：%w；恢复旧版本失败：%v", err, restoreErr)
		}
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

type fileReplacement struct {
	target    string
	backup    string
	hadTarget bool
}

func replaceStagedFileWithBackup(staging, target string) (fileReplacement, error) {
	replacement := fileReplacement{target: target}
	defer func() { _ = os.Remove(staging) }()
	backup, err := temporarySibling(target, "backup")
	if err != nil {
		return replacement, err
	}
	_, targetErr := os.Lstat(target)
	hasTarget := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		_ = os.Remove(backup)
		return replacement, targetErr
	}
	if hasTarget {
		if err := os.Rename(target, backup); err != nil {
			_ = os.Remove(backup)
			return replacement, err
		}
		replacement.backup = backup
		replacement.hadTarget = true
	}
	if err := os.Rename(staging, target); err != nil {
		if hasTarget {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return replacement, fmt.Errorf("替换文件失败：%w；恢复旧文件失败：%v", err, restoreErr)
			}
		}
		return fileReplacement{target: target}, err
	}
	return replacement, nil
}

func rollbackFileReplacements(replacements []fileReplacement) {
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		_ = os.RemoveAll(replacement.target)
		if replacement.hadTarget && replacement.backup != "" {
			_ = os.Rename(replacement.backup, replacement.target)
		}
	}
}

func temporarySibling(target, suffix string) (string, error) {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(parent, filepath.Base(target)+"."+suffix+"-")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target, info.Mode())
	})
}

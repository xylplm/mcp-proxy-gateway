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

// Installer 负责受控预置包安装到 runtimeDir。
type Installer struct {
	runtimeDir string
	client     *http.Client
	// catalog 可注入以便测试；nil 使用 DefaultCatalog。
	catalog func() []PackageSpec
	// now 可注入时间。
	now func() time.Time

	mu sync.Mutex // 串行化安装，避免并发写同一目录
}

// NewInstaller 构造安装器；client 可空（使用带超时的默认客户端）。
func NewInstaller(runtimeDir string, client *http.Client) *Installer {
	if client == nil {
		client = &http.Client{Timeout: defaultInstallTimeout}
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
		PackageSpec: spec,
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

	if strings.TrimSpace(in.runtimeDir) == "" {
		return InstallResult{}, fmt.Errorf("运行时目录未配置")
	}
	if err := EnsureRuntimeLayout(in.runtimeDir); err != nil {
		return InstallResult{}, fmt.Errorf("准备运行时目录失败：%w", err)
	}

	spec, ok := in.findPackage(packageID)
	if !ok {
		return InstallResult{}, fmt.Errorf("未知预置包 %q，仅可安装内置目录中的包", packageID)
	}
	asset, ok := SelectAsset(spec, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return InstallResult{}, fmt.Errorf("预置包 %q 不支持当前平台 %s/%s", packageID, runtime.GOOS, runtime.GOARCH)
	}

	state := in.loadState()
	if ok, _ := state.find(spec.ID); ok {
		// 若关键工具已在 PATH 前缀中可用，视为复用。
		if in.toolsPresent(spec.Tools) {
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
			return InstallResult{}, fmt.Errorf("创建临时目录失败：%w", err)
		}
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, "package"+extForFormat(asset.Format))
	if err := in.downloadFile(ctx, asset.URL, archivePath, asset.SHA256); err != nil {
		return InstallResult{}, err
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return InstallResult{}, err
	}
	switch strings.ToLower(asset.Format) {
	case "tar.gz", "tgz":
		if err := extractTarGz(archivePath, extractDir); err != nil {
			return InstallResult{}, fmt.Errorf("解压失败：%w", err)
		}
	case "zip":
		if err := extractZip(archivePath, extractDir); err != nil {
			return InstallResult{}, fmt.Errorf("解压失败：%w", err)
		}
	default:
		return InstallResult{}, fmt.Errorf("不支持的包格式 %q", asset.Format)
	}

	if err := in.placePackage(spec, extractDir); err != nil {
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
		return InstallResult{}, fmt.Errorf("写入安装状态失败：%w", err)
	}

	return InstallResult{
		ID:         spec.ID,
		Name:       spec.Name,
		Version:    spec.Version,
		Tools:      append([]string{}, spec.Tools...),
		RuntimeDir: in.runtimeDir,
		Reused:     false,
	}, nil
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
	state := in.loadState()
	out := make([]CatalogPackage, 0, len(DefaultCatalog()))
	list := DefaultCatalog()
	if in.catalog != nil {
		list = in.catalog()
	}
	for _, spec := range list {
		asset, supported := SelectAsset(spec, runtime.GOOS, runtime.GOARCH)
		installed, at := state.find(spec.ID)
		item := CatalogPackage{
			PackageSpec: spec,
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

func (in *Installer) downloadFile(ctx context.Context, rawURL, dest, wantSHA string) error {
	wantSHA = strings.ToLower(strings.TrimSpace(wantSHA))
	if wantSHA == "" || len(wantSHA) != 64 {
		return fmt.Errorf("预置包缺少有效 SHA256")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求失败：%w", err)
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

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	limited := io.LimitReader(resp.Body, maxInstallBytes+1)
	written, err := io.Copy(io.MultiWriter(f, h), limited)
	if err != nil {
		return fmt.Errorf("写入下载文件失败：%w", err)
	}
	if written > maxInstallBytes {
		return fmt.Errorf("下载文件超过大小上限（%d 字节）", maxInstallBytes)
	}
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
	binDir := filepath.Join(root, "bin")
	if _, err := os.Stat(binDir); err != nil {
		// Windows zip: 可执行文件在根目录
		if _, err2 := os.Stat(filepath.Join(root, "node.exe")); err2 != nil {
			return fmt.Errorf("Node 发行包布局无法识别")
		}
		binDir = root
	}
	target := filepath.Join(in.runtimeDir, RuntimeSubdirNode)
	_ = os.RemoveAll(target)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// 整棵树移入 node/，并保证 bin 路径符合 PathPrefixes。
	if err := renameOrCopyTree(root, target); err != nil {
		return fmt.Errorf("安装 Node 失败：%w", err)
	}
	// 若 Windows 结构没有 bin/，创建 bin 并链接/复制可执行文件。
	finalBin := filepath.Join(target, "bin")
	if _, err := os.Stat(finalBin); err != nil {
		_ = os.MkdirAll(finalBin, 0o755)
		for _, name := range []string{"node.exe", "npm.cmd", "npx.cmd", "node", "npm", "npx"} {
			src := filepath.Join(target, name)
			if st, err := os.Stat(src); err == nil && !st.IsDir() {
				_ = copyFile(src, filepath.Join(finalBin, name), st.Mode())
			}
		}
	}
	_ = binDir
	return nil
}

func (in *Installer) placeUV(extractDir string) error {
	// uv 压缩包通常直接包含 uv / uvx 可执行文件
	candidates, err := findExecutablesNamed(extractDir, []string{"uv", "uvx", "uv.exe", "uvx.exe"})
	if err != nil || len(candidates) == 0 {
		return fmt.Errorf("uv 发行包中未找到可执行文件")
	}
	target := filepath.Join(in.runtimeDir, RuntimeSubdirUV)
	_ = os.RemoveAll(target)
	if err := os.MkdirAll(filepath.Join(target, "bin"), 0o755); err != nil {
		return err
	}
	binRoot := filepath.Join(in.runtimeDir, RuntimeSubdirBin)
	_ = os.MkdirAll(binRoot, 0o755)

	for _, src := range candidates {
		base := filepath.Base(src)
		dst1 := filepath.Join(target, "bin", base)
		dst2 := filepath.Join(binRoot, base)
		st, _ := os.Stat(src)
		mode := os.FileMode(0o755)
		if st != nil {
			mode = st.Mode()
		}
		if err := copyFile(src, dst1, mode); err != nil {
			return err
		}
		if err := copyFile(src, dst2, mode); err != nil {
			return err
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
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := writeTarEntry(dest, hdr, tr); err != nil {
			return err
		}
	}
	return nil
}

func writeTarEntry(dest string, hdr *tar.Header, r io.Reader) error {
	// 防止 zip-slip
	target, err := safeJoin(dest, hdr.Name)
	if err != nil {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(hdr.Mode) & 0o777
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, r)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	case tar.TypeSymlink:
		// 跳过符号链接，降低攻击面；Node 发行版在非 link 情况下仍可用。
		return nil
	default:
		return nil
	}
}

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := writeZipEntry(dest, f); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(dest string, f *zip.File) error {
	target, err := safeJoin(dest, f.Name)
	if err != nil {
		return err
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, rc)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
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

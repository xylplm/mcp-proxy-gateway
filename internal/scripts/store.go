package scripts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LibraryRoot 返回脚本库根目录 {dataDir}/scripts/library。
func LibraryRoot(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(dataDir, "scripts", "library"))
}

// TrashRoot 返回回收站目录。
func TrashRoot(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(dataDir, "scripts", "trash"))
}

// EnsureLayout 幂等创建脚本目录布局。
func EnsureLayout(dataDir string) error {
	for _, p := range []string{LibraryRoot(dataDir), TrashRoot(dataDir)} {
		if p == "" {
			continue
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

type scriptMetaFile struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Language       Language   `json:"language"`
	Runtime        string     `json:"runtime"`
	EntryFile      string     `json:"entryFile"`
	Tags           []string   `json:"tags,omitempty"`
	Status         Status     `json:"status"`
	CurrentVersion string     `json:"currentVersion"`
	ContentSHA256  string     `json:"contentSha256,omitempty"`
	Risk           RiskReport `json:"risk"`
	SizeBytes      int64      `json:"sizeBytes"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type versionMetaFile struct {
	Version       string     `json:"version"`
	ContentSHA256 string     `json:"contentSha256"`
	SizeBytes     int64      `json:"sizeBytes"`
	Risk          RiskReport `json:"risk"`
	Note          string     `json:"note,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	EntryFile     string     `json:"entryFile"`
}

// Store 为基于本地文件的脚本仓储（进程内互斥）。
type Store struct {
	dataDir string
	mu      sync.Mutex
}

// NewStore 构造脚本仓储。
func NewStore(dataDir string) *Store {
	return &Store{dataDir: strings.TrimSpace(dataDir)}
}

func (s *Store) root() string { return LibraryRoot(s.dataDir) }

func (s *Store) scriptDir(id string) string {
	return filepath.Join(s.root(), id)
}

func (s *Store) metaPath(id string) string {
	return filepath.Join(s.scriptDir(id), "meta.json")
}

func (s *Store) versionDir(id, version string) string {
	return filepath.Join(s.scriptDir(id), "versions", version)
}

// List 返回全部 active 脚本（按更新时间倒序）。
func (s *Store) List() ([]Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := EnsureLayout(s.dataDir); err != nil {
		return nil, err
	}
	root := s.root()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Script{}, nil
		}
		return nil, err
	}
	out := make([]Script, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !validScriptID(e.Name()) {
			continue
		}
		meta, err := s.readMetaUnlocked(e.Name())
		if err != nil || meta.Status != StatusActive {
			continue
		}
		out = append(out, s.toScript(meta))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// Get 读取脚本元数据。
func (s *Store) Get(id string) (Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return Script{}, err
	}
	if meta.Status != StatusActive {
		return Script{}, fmt.Errorf("脚本不存在")
	}
	return s.toScript(meta), nil
}

// GetDetail 读取元数据 + 当前版内容。
func (s *Store) GetDetail(id string) (ScriptDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return ScriptDetail{}, err
	}
	if meta.Status != StatusActive {
		return ScriptDetail{}, fmt.Errorf("脚本不存在")
	}
	content, err := s.readVersionContentUnlocked(id, meta.CurrentVersion, meta.EntryFile)
	if err != nil {
		return ScriptDetail{}, err
	}
	return ScriptDetail{Script: s.toScript(meta), Content: content}, nil
}

// Create 创建脚本并写入 v1。
func (s *Store) Create(in CreateInput) (ScriptDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := EnsureLayout(s.dataDir); err != nil {
		return ScriptDetail{}, err
	}
	if err := ValidateScriptName(in.Name); err != nil {
		return ScriptDetail{}, err
	}
	if err := ValidateContent(in.Content); err != nil {
		return ScriptDetail{}, err
	}
	count, err := s.scriptCountUnlocked()
	if err != nil {
		return ScriptDetail{}, err
	}
	if count >= MaxScripts {
		return ScriptDetail{}, fmt.Errorf("脚本数量已达上限 %d", MaxScripts)
	}
	if exists, err := s.nameExistsUnlocked(strings.TrimSpace(in.Name), ""); err != nil {
		return ScriptDetail{}, err
	} else if exists {
		return ScriptDetail{}, fmt.Errorf("脚本名称已存在")
	}
	lang := NormalizeLanguage(string(in.Language))
	runtime, err := NormalizeRuntime(in.Runtime, lang)
	if err != nil {
		return ScriptDetail{}, err
	}
	if strings.TrimSpace(in.Description) != "" && len(in.Description) > MaxDescription {
		return ScriptDetail{}, fmt.Errorf("描述过长")
	}
	id, err := newScriptID()
	if err != nil {
		return ScriptDetail{}, err
	}
	entry := DefaultEntryFile(lang)
	now := time.Now().UTC()
	risk := AnalyzeContent(in.Content)
	sum := sha256Hex(in.Content)
	meta := scriptMetaFile{
		ID:             id,
		Name:           strings.TrimSpace(in.Name),
		Description:    strings.TrimSpace(in.Description),
		Language:       lang,
		Runtime:        runtime,
		EntryFile:      entry,
		Tags:           NormalizeTags(in.Tags),
		Status:         StatusActive,
		CurrentVersion: "v1",
		ContentSHA256:  sum,
		Risk:           risk,
		SizeBytes:      int64(len(in.Content)),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.writeVersionUnlocked(id, "v1", entry, in.Content, risk, strings.TrimSpace(in.Note), now); err != nil {
		return ScriptDetail{}, err
	}
	if err := s.writeMetaUnlocked(meta); err != nil {
		_ = os.RemoveAll(s.scriptDir(id))
		return ScriptDetail{}, err
	}
	return ScriptDetail{Script: s.toScript(meta), Content: in.Content}, nil
}

// UpdateMeta 更新名称/描述/标签。
func (s *Store) UpdateMeta(id string, in UpdateMetaInput) (Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return Script{}, err
	}
	if meta.Status != StatusActive {
		return Script{}, fmt.Errorf("脚本不可用")
	}
	if in.Name != nil {
		if err := ValidateScriptName(*in.Name); err != nil {
			return Script{}, err
		}
		name := strings.TrimSpace(*in.Name)
		if exists, err := s.nameExistsUnlocked(name, id); err != nil {
			return Script{}, err
		} else if exists {
			return Script{}, fmt.Errorf("脚本名称已存在")
		}
		meta.Name = name
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if len(d) > MaxDescription {
			return Script{}, fmt.Errorf("描述过长")
		}
		meta.Description = d
	}
	if in.Tags != nil {
		meta.Tags = NormalizeTags(in.Tags)
	}
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaUnlocked(meta); err != nil {
		return Script{}, err
	}
	return s.toScript(meta), nil
}

// SaveContent 发布新版本（vN+1）。
func (s *Store) SaveContent(id string, in SaveContentInput) (ScriptDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ValidateContent(in.Content); err != nil {
		return ScriptDetail{}, err
	}
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return ScriptDetail{}, err
	}
	if meta.Status != StatusActive {
		return ScriptDetail{}, fmt.Errorf("脚本不可用")
	}
	count, err := s.versionCountUnlocked(id)
	if err != nil {
		return ScriptDetail{}, err
	}
	if count >= MaxVersions {
		return ScriptDetail{}, fmt.Errorf("脚本版本数已达上限 %d，请新建脚本资产归档后续版本", MaxVersions)
	}
	next, err := s.nextAvailableVersionUnlocked(id, meta.CurrentVersion)
	if err != nil {
		return ScriptDetail{}, err
	}
	now := time.Now().UTC()
	risk := AnalyzeContent(in.Content)
	sum := sha256Hex(in.Content)
	if err := s.writeVersionUnlocked(id, next, meta.EntryFile, in.Content, risk, strings.TrimSpace(in.Note), now); err != nil {
		return ScriptDetail{}, err
	}
	meta.CurrentVersion = next
	meta.ContentSHA256 = sum
	meta.Risk = risk
	meta.SizeBytes = int64(len(in.Content))
	meta.UpdatedAt = now
	if err := s.writeMetaUnlocked(meta); err != nil {
		_ = os.RemoveAll(s.versionDir(id, next))
		return ScriptDetail{}, err
	}
	// 已发布版本不可变且可能被上游固定引用，不自动裁剪。
	return ScriptDetail{Script: s.toScript(meta), Content: in.Content}, nil
}

// SoftDelete 移入回收站目录（保留内容便于审计，不暴露在列表）。
func (s *Store) SoftDelete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return err
	}
	if meta.Status != StatusActive {
		return fmt.Errorf("脚本不存在")
	}
	// 先落盘 trash 状态，确保 List/Get 即使在 rename 失败时也不会把资产当 active。
	meta.Status = StatusTrash
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaUnlocked(meta); err != nil {
		return err
	}
	src := s.scriptDir(id)
	dstRoot := TrashRoot(s.dataDir)
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dstRoot, id+"-"+time.Now().UTC().Format("20060102T150405"))
	if err := os.Rename(src, dst); err != nil {
		// rename 失败时仍保留 trash 状态：脚本已从列表消失，后续 Get 返回不存在。
		// 不回滚为 active，避免「删除成功」后仍可被启动。
		return nil
	}
	return nil
}

// ListVersions 列出版本（新→旧）。
func (s *Store) ListVersions(id string) ([]VersionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return nil, err
	}
	if meta.Status != StatusActive {
		return nil, fmt.Errorf("脚本不存在")
	}
	dir := filepath.Join(s.scriptDir(id), "versions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]VersionMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !validVersion(e.Name()) {
			continue
		}
		vm, err := s.readVersionMetaUnlocked(id, e.Name())
		if err != nil {
			continue
		}
		out = append(out, vm)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return versionNum(out[i].Version) > versionNum(out[j].Version)
	})
	return out, nil
}

// GetVersionContent 读取指定版本内容。
func (s *Store) GetVersionContent(id, version string) (string, VersionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return "", VersionMeta{}, err
	}
	if meta.Status != StatusActive {
		return "", VersionMeta{}, fmt.Errorf("脚本不存在")
	}
	if !validVersion(version) {
		return "", VersionMeta{}, fmt.Errorf("版本号非法")
	}
	vm, err := s.readVersionMetaUnlocked(id, version)
	if err != nil {
		return "", VersionMeta{}, err
	}
	content, err := s.readVersionContentUnlocked(id, version, meta.EntryFile)
	if err != nil {
		return "", VersionMeta{}, err
	}
	return content, vm, nil
}

// ActivateVersion 将 current 指针切到已有版本（不复制内容）。
func (s *Store) ActivateVersion(id, version string) (Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaUnlocked(id)
	if err != nil {
		return Script{}, err
	}
	if meta.Status != StatusActive {
		return Script{}, fmt.Errorf("脚本不可用")
	}
	vm, err := s.readVersionMetaUnlocked(id, version)
	if err != nil {
		return Script{}, err
	}
	meta.CurrentVersion = version
	meta.ContentSHA256 = vm.ContentSHA256
	meta.Risk = vm.Risk
	meta.SizeBytes = vm.SizeBytes
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaUnlocked(meta); err != nil {
		return Script{}, err
	}
	return s.toScript(meta), nil
}

// ResolveEntryPath 返回脚本当前（或指定）版本入口绝对路径。
func (s *Store) ResolveEntryPath(id, version string) (path string, meta Script, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readMetaUnlocked(id)
	if err != nil {
		return "", Script{}, err
	}
	if m.Status != StatusActive {
		return "", Script{}, fmt.Errorf("脚本不可用")
	}
	ver := strings.TrimSpace(version)
	if ver == "" || ver == "current" {
		ver = m.CurrentVersion
	}
	if !validVersion(ver) {
		return "", Script{}, fmt.Errorf("版本号非法")
	}
	p := filepath.Join(s.versionDir(id, ver), m.EntryFile)
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", Script{}, fmt.Errorf("脚本入口不存在")
	}
	libraryResolved, err := filepath.EvalSymlinks(s.root())
	if err != nil {
		return "", Script{}, fmt.Errorf("脚本库路径不可用")
	}
	if !pathWithinRoot(resolved, libraryResolved) {
		return "", Script{}, fmt.Errorf("脚本入口真实路径越出脚本库")
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", Script{}, fmt.Errorf("脚本入口不可读")
	}
	vm, err := s.readVersionMetaUnlocked(id, ver)
	if err != nil {
		return "", Script{}, err
	}
	actualHash := sha256Hex(string(content))
	if actualHash != vm.ContentSHA256 {
		return "", Script{}, fmt.Errorf("脚本文件内容与版本元数据不一致，拒绝启动")
	}
	sc := s.toScript(m)
	sc.ContentSHA256 = vm.ContentSHA256
	sc.Risk = vm.Risk
	sc.SizeBytes = vm.SizeBytes
	sc.CurrentVersion = ver
	sc.EntryPath = resolved
	return resolved, sc, nil
}

func (s *Store) toScript(meta scriptMetaFile) Script {
	entryPath := ""
	if meta.CurrentVersion != "" && meta.EntryFile != "" {
		entryPath = filepath.Join(s.versionDir(meta.ID, meta.CurrentVersion), meta.EntryFile)
	}
	tags := meta.Tags
	if tags == nil {
		tags = []string{}
	}
	return Script{
		ID:             meta.ID,
		Name:           meta.Name,
		Description:    meta.Description,
		Language:       meta.Language,
		Runtime:        meta.Runtime,
		EntryFile:      meta.EntryFile,
		Tags:           tags,
		Status:         meta.Status,
		CurrentVersion: meta.CurrentVersion,
		ContentSHA256:  meta.ContentSHA256,
		Risk:           meta.Risk,
		SizeBytes:      meta.SizeBytes,
		CreatedAt:      meta.CreatedAt,
		UpdatedAt:      meta.UpdatedAt,
		EntryPath:      entryPath,
	}
}

func (s *Store) readMetaUnlocked(id string) (scriptMetaFile, error) {
	if !validScriptID(id) {
		return scriptMetaFile{}, fmt.Errorf("脚本不存在")
	}
	b, err := readBoundedFile(s.metaPath(id), 64*1024)
	if err != nil {
		if os.IsNotExist(err) {
			return scriptMetaFile{}, fmt.Errorf("脚本不存在")
		}
		return scriptMetaFile{}, err
	}
	var meta scriptMetaFile
	if err := json.Unmarshal(b, &meta); err != nil {
		return scriptMetaFile{}, fmt.Errorf("脚本元数据损坏")
	}
	if err := validateStoredMeta(id, meta); err != nil {
		return scriptMetaFile{}, err
	}
	return meta, nil
}

func (s *Store) writeMetaUnlocked(meta scriptMetaFile) error {
	if err := os.MkdirAll(s.scriptDir(meta.ID), 0o755); err != nil {
		return err
	}
	return writeJSONAtomic(s.metaPath(meta.ID), meta)
}

func (s *Store) writeVersionUnlocked(id, version, entryFile, content string, risk RiskReport, note string, at time.Time) error {
	dir := s.versionDir(id, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	entryPath := filepath.Join(dir, entryFile)
	if err := os.WriteFile(entryPath, []byte(content), 0o644); err != nil {
		return err
	}
	vm := versionMetaFile{
		Version:       version,
		ContentSHA256: sha256Hex(content),
		SizeBytes:     int64(len(content)),
		Risk:          risk,
		Note:          note,
		CreatedAt:     at,
		EntryFile:     entryFile,
	}
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), vm); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (s *Store) readVersionMetaUnlocked(id, version string) (VersionMeta, error) {
	if !validScriptID(id) || !validVersion(version) {
		return VersionMeta{}, fmt.Errorf("版本不存在")
	}
	b, err := readBoundedFile(filepath.Join(s.versionDir(id, version), "meta.json"), 64*1024)
	if err != nil {
		if os.IsNotExist(err) {
			return VersionMeta{}, fmt.Errorf("版本不存在")
		}
		return VersionMeta{}, err
	}
	var vm versionMetaFile
	if err := json.Unmarshal(b, &vm); err != nil {
		return VersionMeta{}, fmt.Errorf("版本元数据损坏")
	}
	if vm.Version != version || !ValidSHA256(vm.ContentSHA256) || !validEntryFile(vm.EntryFile) || vm.SizeBytes < 0 || vm.SizeBytes > MaxScriptBytes {
		return VersionMeta{}, fmt.Errorf("版本元数据损坏")
	}
	return VersionMeta(vm), nil
}

func (s *Store) readVersionContentUnlocked(id, version, entryFile string) (string, error) {
	if !validScriptID(id) || !validVersion(version) || !validEntryFile(entryFile) {
		return "", fmt.Errorf("脚本内容不存在")
	}
	root := s.versionDir(id, version)
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("脚本内容不存在")
	}
	pathResolved, err := filepath.EvalSymlinks(filepath.Join(root, entryFile))
	if err != nil || !pathWithinRoot(pathResolved, rootResolved) {
		return "", fmt.Errorf("脚本内容路径非法")
	}
	b, err := readBoundedFile(pathResolved, MaxScriptBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("脚本内容不存在")
		}
		return "", err
	}
	return string(b), nil
}

func validateStoredMeta(id string, meta scriptMetaFile) error {
	if meta.ID != id || !validScriptID(meta.ID) || !validVersion(meta.CurrentVersion) || !validEntryFile(meta.EntryFile) || !ValidSHA256(meta.ContentSHA256) {
		return fmt.Errorf("脚本元数据损坏")
	}
	// SoftDelete 会把状态写为 trash；允许读取以便完成迁移/清理，上层再按状态拒绝业务访问。
	if meta.Status != StatusActive && meta.Status != StatusTrash {
		return fmt.Errorf("脚本元数据损坏")
	}
	if meta.SizeBytes < 0 || meta.SizeBytes > MaxScriptBytes {
		return fmt.Errorf("脚本元数据损坏")
	}
	if err := ValidateScriptName(meta.Name); err != nil {
		return fmt.Errorf("脚本元数据损坏")
	}
	if _, err := NormalizeRuntime(meta.Runtime, meta.Language); err != nil {
		return fmt.Errorf("脚本元数据损坏")
	}
	return nil
}

func validEntryFile(name string) bool {
	name = strings.TrimSpace(name)
	return name == "main.py" || name == "index.js"
}

func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("文件超过允许大小")
	}
	return b, nil
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	// Unix Rename 原子覆盖；Windows 若目标存在会失败，再做受控回退。
	if err := os.Rename(tmp, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newScriptID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "scr_" + hex.EncodeToString(b[:]), nil
}

func (s *Store) scriptCountUnlocked() (int, error) {
	entries, err := os.ReadDir(s.root())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && validScriptID(entry.Name()) {
			count++
		}
	}
	return count, nil
}

func (s *Store) nameExistsUnlocked(name, exceptID string) (bool, error) {
	entries, err := os.ReadDir(s.root())
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	key := strings.ToLower(strings.TrimSpace(name))
	for _, entry := range entries {
		if !entry.IsDir() || !validScriptID(entry.Name()) || entry.Name() == exceptID {
			continue
		}
		meta, err := s.readMetaUnlocked(entry.Name())
		if err != nil || meta.Status != StatusActive {
			continue
		}
		if strings.ToLower(strings.TrimSpace(meta.Name)) == key {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) versionCountUnlocked(id string) (int, error) {
	entries, err := os.ReadDir(filepath.Join(s.scriptDir(id), "versions"))
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && validVersion(entry.Name()) {
			count++
		}
	}
	return count, nil
}

func (s *Store) nextAvailableVersionUnlocked(id, current string) (string, error) {
	n := versionNum(current)
	if n < 1 {
		n = 0
	}
	for i := 0; i < MaxVersions+10000; i++ {
		n++
		candidate := fmt.Sprintf("v%d", n)
		if _, err := os.Stat(s.versionDir(id, candidate)); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("无法分配新版本号")
}

func versionNum(v string) int {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		return 0
	}
	n := 0
	for _, c := range v[1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

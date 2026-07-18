package scripts

import (
	"fmt"
	"strings"
)

// Service 脚本应用服务。
type Service struct {
	store *Store
}

// NewService 构造脚本服务。
func NewService(dataDir string) *Service {
	_ = EnsureLayout(dataDir)
	return &Service{store: NewStore(dataDir)}
}

func (s *Service) List() ([]Script, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("脚本服务未就绪")
	}
	return s.store.List()
}

func (s *Service) Get(id string) (Script, error) {
	return s.store.Get(strings.TrimSpace(id))
}

func (s *Service) GetDetail(id string) (ScriptDetail, error) {
	return s.store.GetDetail(strings.TrimSpace(id))
}

func (s *Service) Create(in CreateInput) (ScriptDetail, error) {
	return s.store.Create(in)
}

func (s *Service) UpdateMeta(id string, in UpdateMetaInput) (Script, error) {
	return s.store.UpdateMeta(strings.TrimSpace(id), in)
}

func (s *Service) SaveContent(id string, in SaveContentInput) (ScriptDetail, error) {
	return s.store.SaveContent(strings.TrimSpace(id), in)
}

func (s *Service) Delete(id string) error {
	return s.store.SoftDelete(strings.TrimSpace(id))
}

func (s *Service) ListVersions(id string) ([]VersionMeta, error) {
	return s.store.ListVersions(strings.TrimSpace(id))
}

func (s *Service) GetVersion(id, version string) (content string, meta VersionMeta, err error) {
	return s.store.GetVersionContent(strings.TrimSpace(id), strings.TrimSpace(version))
}

func (s *Service) ActivateVersion(id, version string) (Script, error) {
	return s.store.ActivateVersion(strings.TrimSpace(id), strings.TrimSpace(version))
}

func (s *Service) Analyze(content string) (RiskReport, error) {
	if err := ValidateContent(content); err != nil {
		return RiskReport{}, err
	}
	return AnalyzeContent(content), nil
}

// DiffVersions 对比两个版本（或与当前未保存内容由上层自行传文本）。
func (s *Service) DiffVersions(id, leftVer, rightVer string) (DiffResult, error) {
	id = strings.TrimSpace(id)
	left, _, err := s.store.GetVersionContent(id, strings.TrimSpace(leftVer))
	if err != nil {
		return DiffResult{}, err
	}
	right, _, err := s.store.GetVersionContent(id, strings.TrimSpace(rightVer))
	if err != nil {
		return DiffResult{}, err
	}
	return DiffText(leftVer, left, rightVer, right), nil
}

// BuildLaunchBinding 生成上游可绑定的 scriptRef + 建议 command/args/cwd。
func (s *Service) BuildLaunchBinding(id, version string) (bind LaunchBinding, command string, args []string, cwd string, err error) {
	path, meta, err := s.store.ResolveEntryPath(strings.TrimSpace(id), strings.TrimSpace(version))
	if err != nil {
		return LaunchBinding{}, "", nil, "", err
	}
	bind = LaunchBinding{
		ScriptID:      meta.ID,
		Version:       meta.CurrentVersion,
		EntryFile:     meta.EntryFile,
		ContentSHA256: meta.ContentSHA256,
		Runtime:       meta.Runtime,
		EntryPath:     path,
		RiskLevel:     meta.Risk.Level,
		RiskScore:     meta.Risk.Score,
	}
	return bind, meta.Runtime, []string{path}, filepathDir(path), nil
}

func filepathDir(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 {
		return p
	}
	return p[:i]
}

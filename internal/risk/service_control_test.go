package risk

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type controlTestJobs struct {
	mu      sync.Mutex
	active  bool
	creates int
}

func (j *controlTestJobs) Create(_ context.Context, job AssessmentJob) (AssessmentJob, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.creates++
	j.active = true
	job.ID = "job-1"
	job.Status = JobQueued
	return job, nil
}

func (j *controlTestJobs) HasActiveForProvider(context.Context, string) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.active, nil
}

func (j *controlTestJobs) Get(context.Context, string) (AssessmentJob, error) {
	return AssessmentJob{ID: "job-1", Status: JobCancelled}, nil
}

func (j *controlTestJobs) List(context.Context, int) ([]AssessmentJob, error)  { return nil, nil }
func (j *controlTestJobs) ListQueued(context.Context) ([]AssessmentJob, error) { return nil, nil }
func (j *controlTestJobs) Cancel(context.Context, string) error                { return nil }
func (j *controlTestJobs) RecoverRunning(context.Context) error                { return nil }
func (j *controlTestJobs) SetRunning(context.Context, string) error {
	return domain.NewError(domain.CodeConflict, "测试中不启动 worker")
}
func (j *controlTestJobs) UpdateProgress(context.Context, AssessmentJob) error { return nil }

func (j *controlTestJobs) createCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.creates
}

type controlTestCatalog struct {
	items []Assessment
}

func (c controlTestCatalog) Get(context.Context, string, string) (Assessment, error) {
	return Assessment{}, nil
}
func (c controlTestCatalog) ListAssessable(context.Context, int) ([]Assessment, error) {
	return c.items, nil
}
func (c controlTestCatalog) ListNeedsReview(context.Context, int) ([]Assessment, error) {
	return c.items, nil
}
func (c controlTestCatalog) ApplyAIResult(context.Context, string, string, AIResult, Provider) (Assessment, error) {
	return Assessment{}, nil
}
func (c controlTestCatalog) MarkAIError(context.Context, string, string, string) error { return nil }

func TestCreateProviderWithoutEncryptionKeyReturnsServiceUnavailable(t *testing.T) {
	providers := &observerTestProviders{}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil, nil)

	_, err := service.CreateProvider(context.Background(), ProviderInput{
		Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "model", APIKey: "secret", Enabled: true,
	})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望领域错误，实际 %T: %v", err, err)
	}
	if apiErr.Code != domain.CodeServiceUnavailable {
		t.Fatalf("期望 SERVICE_UNAVAILABLE，实际 %s", apiErr.Code)
	}
	if apiErr.Message != encryptionUnavailableMessage {
		t.Fatalf("安全提示不符：%q", apiErr.Message)
	}
	if service.ProviderEncryptionReady() {
		t.Fatal("未注入 Cipher 时加密能力不应就绪")
	}
}

func TestProviderWithoutAPIKeyStillSavesWithoutEncryptionKey(t *testing.T) {
	providers := &observerTestProviders{}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil, nil)

	created, err := service.CreateProvider(context.Background(), ProviderInput{
		Name: "Local", BaseURL: "http://127.0.0.1:11434/v1", Model: "local", Enabled: true,
	})
	if err != nil {
		t.Fatalf("无鉴权本地 Provider 不应依赖主加密密钥：%v", err)
	}
	if created.Name != "Local" || len(created.APIKeyCiphertext) != 0 {
		t.Fatalf("保存结果不符：%+v", created)
	}
}

func TestUpdateProviderWithNewAPIKeyWithoutEncryptionKeyReturnsServiceUnavailable(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{ID: "provider-1", Active: true}}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil, nil)

	_, err := service.UpdateProvider(context.Background(), "provider-1", ProviderInput{
		Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "model", APIKey: "new-secret", Enabled: true,
	})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeServiceUnavailable {
		t.Fatalf("更新新密钥时应返回 SERVICE_UNAVAILABLE，实际 %v", err)
	}
}

func TestQueueAssessmentSkipsExistingActiveJob(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{ID: "provider-1", Enabled: true}}
	jobs := &controlTestJobs{active: true}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs, nil)

	_, err := service.QueueAssessment(context.Background(), 500)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeConflict {
		t.Fatalf("已有活动任务时应返回 CONFLICT，实际 %v", err)
	}
	if jobs.createCount() != 0 {
		t.Fatalf("已有活动任务时不应写入 queued 任务，实际创建 %d 次", jobs.createCount())
	}
}

func TestTriggerAutoAssessmentCoalescesConcurrentNotifications(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{
		ID: "provider-1", Enabled: true, Active: true, AutoAssess: true, BatchSize: 10, MaxConcurrency: 1,
	}}
	jobs := &controlTestJobs{}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs, nil)

	const triggers = 8
	start := make(chan struct{})
	errs := make(chan error, triggers)
	var wg sync.WaitGroup
	for range triggers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- service.TriggerAutoAssessment(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("自动评级触发不应失败：%v", err)
		}
	}
	if jobs.createCount() != 1 {
		t.Fatalf("并发通知应合并为一个任务，实际创建 %d 次", jobs.createCount())
	}
}

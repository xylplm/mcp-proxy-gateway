package risk

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type controlTestJobs struct {
	mu                sync.Mutex
	statuses          []JobStatus
	creates           int
	statusReads       int
	runAllowed        bool
	setRunningErr     error
	progressUpdates   []AssessmentJob
	statusReadStarted chan struct{}
	releaseStatusRead chan struct{}
}

func (j *controlTestJobs) Create(_ context.Context, job AssessmentJob) (AssessmentJob, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.creates++
	j.statuses = append(j.statuses, JobQueued)
	job.ID = "job-1"
	job.Status = JobQueued
	return job, nil
}

func (j *controlTestJobs) ActiveStatusesForProvider(context.Context, string) ([]JobStatus, error) {
	j.mu.Lock()
	j.statusReads++
	read := j.statusReads
	statuses := append([]JobStatus(nil), j.statuses...)
	started, release := j.statusReadStarted, j.releaseStatusRead
	j.mu.Unlock()
	if read == 1 && started != nil && release != nil {
		close(started)
		<-release
	}
	return statuses, nil
}

func (j *controlTestJobs) Get(context.Context, string) (AssessmentJob, error) {
	return AssessmentJob{ID: "job-1", Status: JobCancelled}, nil
}

func (j *controlTestJobs) List(context.Context, int) ([]AssessmentJob, error)  { return nil, nil }
func (j *controlTestJobs) ListQueued(context.Context) ([]AssessmentJob, error) { return nil, nil }
func (j *controlTestJobs) Cancel(context.Context, string) error                { return nil }
func (j *controlTestJobs) RecoverRunning(context.Context) error                { return nil }
func (j *controlTestJobs) SetRunning(context.Context, string) error {
	if j.setRunningErr != nil {
		return j.setRunningErr
	}
	if j.runAllowed {
		return nil
	}
	return domain.NewError(domain.CodeConflict, "测试中不启动 worker")
}
func (j *controlTestJobs) UpdateProgress(_ context.Context, job AssessmentJob) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progressUpdates = append(j.progressUpdates, job)
	return nil
}

func (j *controlTestJobs) createCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.creates
}

func (j *controlTestJobs) statusReadCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.statusReads
}

func (j *controlTestJobs) updates() []AssessmentJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]AssessmentJob(nil), j.progressUpdates...)
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

type activationGuardTestProviders struct {
	*observerTestProviders
	activateCalls int
}

func (p *activationGuardTestProviders) Activate(context.Context, string) error {
	p.activateCalls++
	return nil
}

func TestCreateProviderStoresPlaintextAPIKey(t *testing.T) {
	providers := &observerTestProviders{}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil)

	created, err := service.CreateProvider(context.Background(), ProviderInput{
		Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "model", APIKey: "secret", Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建明文密钥 Provider 失败：%v", err)
	}
	if created.APIKey != "secret" || providers.provider.APIKey != "secret" {
		t.Fatalf("API Key 未按原值保存：created=%q stored=%q", created.APIKey, providers.provider.APIKey)
	}
}

func TestUpdateProviderReplacesAndClearsPlaintextAPIKey(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{ID: "provider-1", Active: true, APIKey: "old-secret"}}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil)

	input := ProviderInput{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "model", APIKey: "new-secret", Enabled: true}
	updated, err := service.UpdateProvider(context.Background(), "provider-1", input)
	if err != nil {
		t.Fatalf("更新明文密钥失败：%v", err)
	}
	if updated.APIKey != "new-secret" || providers.provider.APIKey != "new-secret" {
		t.Fatalf("API Key 未被替换：updated=%q stored=%q", updated.APIKey, providers.provider.APIKey)
	}

	input.APIKey = ""
	cleared, err := service.UpdateProvider(context.Background(), "provider-1", input)
	if err != nil {
		t.Fatalf("清空明文密钥失败：%v", err)
	}
	if cleared.APIKey != "" || providers.provider.APIKey != "" {
		t.Fatalf("空 API Key 应清除已保存密钥：updated=%q stored=%q", cleared.APIKey, providers.provider.APIKey)
	}
}

func TestProviderWithPlaintextAPIKeyReturnsKeyForRequests(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{ID: "provider-1", APIKey: "plain-secret"}}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil)

	items, err := service.ListProviders(context.Background())
	if err != nil || len(items) != 1 || items[0].APIKey != "plain-secret" {
		t.Fatalf("管理端应返回完整密钥：items=%+v err=%v", items, err)
	}
	_, key, err := service.providerWithKey(context.Background(), "provider-1")
	if err != nil || key != "plain-secret" {
		t.Fatalf("请求应使用保存的明文密钥：key=%q err=%v", key, err)
	}
}

func TestActivateProviderPassesThroughPlaintextKey(t *testing.T) {
	providers := &activationGuardTestProviders{observerTestProviders: &observerTestProviders{provider: Provider{
		ID: "provider-1", Enabled: true, APIKey: "plain-secret",
	}}}
	service := NewGovernanceService(providers, controlTestCatalog{}, nil)

	err := service.ActivateProvider(context.Background(), "provider-1")
	if err != nil {
		t.Fatalf("明文密钥 Provider 应可设为活动：%v", err)
	}
	if providers.activateCalls != 1 {
		t.Fatalf("应调用仓储激活，实际调用 %d 次", providers.activateCalls)
	}
}

func TestQueueAssessmentSkipsExistingActiveJob(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{ID: "provider-1", Enabled: true}}
	jobs := &controlTestJobs{statuses: []JobStatus{JobRunning}}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs)

	_, err := service.QueueAssessment(context.Background(), 500)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeConflict {
		t.Fatalf("已有活动任务时应返回 CONFLICT，实际 %v", err)
	}
	if jobs.createCount() != 0 {
		t.Fatalf("已有活动任务时不应写入 queued 任务，实际创建 %d 次", jobs.createCount())
	}
}

func TestRunJobDoesNotOverwriteJobClaimedByAnotherWorker(t *testing.T) {
	jobs := &controlTestJobs{setRunningErr: domain.NewError(domain.CodeConflict, "评级任务不处于等待状态")}
	service := NewGovernanceService(&observerTestProviders{}, controlTestCatalog{}, jobs)

	err := service.runJob(context.Background(), AssessmentJob{ID: "job-1", ProviderID: "provider-1"})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeConflict {
		t.Fatalf("任务已被其他 worker 认领时应返回冲突，实际 %v", err)
	}
	if got := jobs.updates(); len(got) != 0 {
		t.Fatalf("认领冲突不能覆盖已有任务状态，实际写入 %+v", got)
	}
}

func TestRunJobCancelsQueuedWorkWhenProviderDisabled(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{ID: "provider-1", Enabled: false}}
	jobs := &controlTestJobs{runAllowed: true}
	service := NewGovernanceService(providers, controlTestCatalog{}, jobs)

	if err := service.runJob(context.Background(), AssessmentJob{ID: "job-1", ProviderID: "provider-1"}); err != nil {
		t.Fatalf("停用 Provider 的等待任务应正常取消，实际 %v", err)
	}
	updates := jobs.updates()
	if len(updates) != 1 || updates[0].Status != JobCancelled {
		t.Fatalf("停用 Provider 应取消尚未开始的任务，实际 %+v", updates)
	}
}

func TestAutoAssessmentKeepsOneFollowUpForRunningJob(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{
		ID: "provider-1", Enabled: true, Active: true, AutoAssess: true, BatchSize: 10, MaxConcurrency: 1,
	}}
	jobs := &controlTestJobs{statuses: []JobStatus{JobRunning}}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs)

	if err := service.triggerAutoAssessmentNow(context.Background()); err != nil {
		t.Fatalf("运行中的任务应允许自动创建一个跟进任务：%v", err)
	}
	if err := service.triggerAutoAssessmentNow(context.Background()); err != nil {
		t.Fatalf("已有跟进任务时自动触发不应失败：%v", err)
	}
	if got := jobs.createCount(); got != 1 {
		t.Fatalf("自动同步最多应保留一个跟进任务，实际创建 %d 个", got)
	}
}

func TestAutoAssessmentSkipsProviderWithAutoAssessDisabled(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{
		ID: "provider-1", Enabled: true, Active: true, AutoAssess: false, BatchSize: 10, MaxConcurrency: 1,
	}}
	jobs := &controlTestJobs{}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs)

	if err := service.triggerAutoAssessmentNow(context.Background()); err != nil {
		t.Fatalf("未开启自动评级时不应报错：%v", err)
	}
	if got := jobs.createCount(); got != 0 {
		t.Fatalf("未开启自动评级时不应创建任务，实际创建 %d 个", got)
	}
}

func TestTriggerAutoAssessmentSeparatesNotificationsArrivingDuringQueueing(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{
		ID: "provider-1", Enabled: true, Active: true, AutoAssess: true, BatchSize: 10, MaxConcurrency: 1,
	}}
	jobs := &controlTestJobs{
		statusReadStarted: make(chan struct{}),
		releaseStatusRead: make(chan struct{}),
	}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs)

	firstDone := make(chan error, 1)
	go func() { firstDone <- service.TriggerAutoAssessment(context.Background()) }()
	select {
	case <-jobs.statusReadStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("首个自动触发未进入创建任务阶段")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- service.TriggerAutoAssessment(context.Background()) }()
	close(jobs.releaseStatusRead)
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("自动触发不应失败：%v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("自动触发未在预期时间内结束")
		}
	}
	if got := jobs.statusReadCount(); got != 2 {
		t.Fatalf("运行期到达的通知应单独进入下一轮，实际只检查了 %d 次任务状态", got)
	}
}

func TestTriggerAutoAssessmentCoalescesConcurrentNotifications(t *testing.T) {
	providers := &observerTestProviders{provider: Provider{
		ID: "provider-1", Enabled: true, Active: true, AutoAssess: true, BatchSize: 10, MaxConcurrency: 1,
	}}
	jobs := &controlTestJobs{}
	service := NewGovernanceService(providers, controlTestCatalog{
		items: []Assessment{{UpstreamID: "up-1", OriginalName: "read"}},
	}, jobs)

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

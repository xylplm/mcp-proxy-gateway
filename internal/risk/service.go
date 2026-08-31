package risk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type ProviderRepository interface {
	Create(context.Context, Provider) (Provider, error)
	Update(context.Context, Provider) (Provider, error)
	Get(context.Context, string) (Provider, error)
	Active(context.Context) (Provider, error)
	List(context.Context) ([]Provider, error)
	Activate(context.Context, string) error
	Delete(context.Context, string) error
}

type CatalogRepository interface {
	Get(context.Context, string, string) (Assessment, error)
	ListAssessable(context.Context, int) ([]Assessment, error)
	ListNeedsReview(context.Context, int) ([]Assessment, error)
	ApplyAIResult(context.Context, string, string, AIResult, Provider) (Assessment, error)
	MarkAIError(context.Context, string, string, string) error
}

type JobRepository interface {
	Create(context.Context, AssessmentJob) (AssessmentJob, error)
	ActiveStatusesForProvider(context.Context, string) ([]JobStatus, error)
	Get(context.Context, string) (AssessmentJob, error)
	List(context.Context, int) ([]AssessmentJob, error)
	ListQueued(context.Context) ([]AssessmentJob, error)
	Cancel(context.Context, string) error
	RecoverRunning(context.Context) error
	SetRunning(context.Context, string) error
	UpdateProgress(context.Context, AssessmentJob) error
}

type ProviderInput struct {
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	APIStyle       APIStyle `json:"apiStyle"`
	Model          string   `json:"model"`
	APIKey         string   `json:"apiKey"`
	Enabled        bool     `json:"enabled"`
	TimeoutS       int      `json:"timeoutS"`
	BatchSize      int      `json:"batchSize"`
	MaxConcurrency int      `json:"maxConcurrency"`
	AutoAssess     bool     `json:"autoAssess"`
}

type GovernanceService struct {
	providers       ProviderRepository
	catalog         CatalogRepository
	client          *OpenAIClient
	jobs            JobRepository
	running         sync.Map
	queueMu         sync.Mutex
	autoTriggerMu   sync.Mutex
	autoTrigger     *autoAssessmentTrigger
	onCatalogChange func()
}

type autoAssessmentTrigger struct {
	done chan struct{}
	err  error
}

const autoAssessmentDebounce = 300 * time.Millisecond

// GovernanceOption configures an optional integration point without widening
// the catalog and job repository contracts.
type GovernanceOption func(*GovernanceService)

// WithCatalogChangeObserver runs after one or more risk assessments are
// successfully persisted. The callback must be non-blocking and safe for
// concurrent calls because batch workers can finish simultaneously.
func WithCatalogChangeObserver(observer func()) GovernanceOption {
	return func(service *GovernanceService) {
		service.onCatalogChange = observer
	}
}

func NewGovernanceService(providers ProviderRepository, catalog CatalogRepository, jobs JobRepository, options ...GovernanceOption) *GovernanceService {
	service := &GovernanceService{providers: providers, catalog: catalog, jobs: jobs, client: NewOpenAIClient()}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *GovernanceService) notifyCatalogChange() {
	if s.onCatalogChange != nil {
		s.onCatalogChange()
	}
}

func (s *GovernanceService) ListProviders(ctx context.Context) ([]Provider, error) {
	return s.providers.List(ctx)
}
func (s *GovernanceService) GetProvider(ctx context.Context, id string) (Provider, error) {
	return s.providers.Get(ctx, id)
}
func (s *GovernanceService) DeleteProvider(ctx context.Context, id string) error {
	return s.providers.Delete(ctx, id)
}
func (s *GovernanceService) ActivateProvider(ctx context.Context, id string) error {
	return s.providers.Activate(ctx, id)
}

func (s *GovernanceService) CreateProvider(ctx context.Context, in ProviderInput) (Provider, error) {
	p := providerFromInput(in)
	if err := ValidateProvider(p); err != nil {
		return Provider{}, fmt.Errorf("Provider 配置无效: %w", err)
	}
	return s.providers.Create(ctx, p)
}

func (s *GovernanceService) UpdateProvider(ctx context.Context, id string, in ProviderInput) (Provider, error) {
	current, err := s.providers.Get(ctx, id)
	if err != nil {
		return Provider{}, err
	}
	p := providerFromInput(in)
	p.ID, p.Active = id, current.Active
	if err := ValidateProvider(p); err != nil {
		return Provider{}, fmt.Errorf("Provider 配置无效: %w", err)
	}
	return s.providers.Update(ctx, p)
}

func (s *GovernanceService) TestProvider(ctx context.Context, id string) (int64, error) {
	p, key, err := s.providerWithKey(ctx, id)
	if err != nil {
		return 0, err
	}
	duration, err := s.client.TestConnection(ctx, p, key)
	return duration.Milliseconds(), err
}

type AssessSummary struct {
	Processed int `json:"processed"`
	Succeeded int `json:"succeeded"`
	Review    int `json:"needsReview"`
	Failed    int `json:"failed"`
}

func (s *GovernanceService) QueueAssessment(ctx context.Context, limit int) (AssessmentJob, error) {
	return s.queueAssessment(ctx, limit, "pending", false)
}

func (s *GovernanceService) QueueReviewAssessment(ctx context.Context, limit int) (AssessmentJob, error) {
	return s.queueAssessment(ctx, limit, "needs_review", false)
}

func (s *GovernanceService) queueAssessment(ctx context.Context, limit int, scope string, allowAutoFollowUp bool) (AssessmentJob, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	p, err := s.providers.Active(ctx)
	if err != nil {
		return AssessmentJob{}, err
	}
	if allowAutoFollowUp && !p.AutoAssess {
		return AssessmentJob{}, domain.NewError(domain.CodeConflict, "当前活动 Provider 未开启自动评级")
	}
	activeStatuses, err := s.jobs.ActiveStatusesForProvider(ctx, p.ID)
	if err != nil {
		return AssessmentJob{}, err
	}
	hasQueued, hasRunning := false, false
	for _, status := range activeStatuses {
		hasQueued = hasQueued || status == JobQueued
		hasRunning = hasRunning || status == JobRunning
	}
	// 管理员手动发起时始终保持单任务。自动同步只在已有运行任务且尚无等待任务时
	// 保留一个跟进任务，确保任务执行期间新同步进来的工具不会被漏掉，也不会堆积队列。
	if hasQueued || (hasRunning && (!allowAutoFollowUp || scope != "pending")) {
		return AssessmentJob{}, domain.NewError(domain.CodeConflict, "当前活动 Provider 已有等待中或运行中的评级任务")
	}
	var items []Assessment
	if scope == "needs_review" {
		items, err = s.catalog.ListNeedsReview(ctx, limit)
	} else {
		items, err = s.catalog.ListAssessable(ctx, limit)
	}
	if err != nil {
		return AssessmentJob{}, err
	}
	if len(items) == 0 {
		if scope == "needs_review" {
			return AssessmentJob{}, domain.NewError(domain.CodeConflict, "没有未人工确认的待复核工具")
		}
		return AssessmentJob{}, domain.NewError(domain.CodeConflict, "没有待评级或需重试的工具")
	}
	job, err := s.jobs.Create(ctx, AssessmentJob{ProviderID: p.ID, Scope: scope, ScopePayload: map[string]any{
		"limit": limit, "batchSize": p.BatchSize, "maxConcurrency": p.MaxConcurrency,
	}, RequestedCount: len(items), ErrorCounts: map[string]int{}})
	if err != nil {
		return AssessmentJob{}, err
	}
	s.launchJob(context.WithoutCancel(ctx), job)
	return job, nil
}

func (s *GovernanceService) ListJobs(ctx context.Context, limit int) ([]AssessmentJob, error) {
	return s.jobs.List(ctx, limit)
}
func (s *GovernanceService) GetJob(ctx context.Context, id string) (AssessmentJob, error) {
	return s.jobs.Get(ctx, id)
}
func (s *GovernanceService) CancelJob(ctx context.Context, id string) error {
	return s.jobs.Cancel(ctx, id)
}

func (s *GovernanceService) ReassessTool(ctx context.Context, upstreamID, originalName string) (Assessment, error) {
	item, err := s.catalog.Get(ctx, upstreamID, originalName)
	if err != nil {
		return Assessment{}, err
	}
	p, key, err := s.activeProviderWithKey(ctx)
	if err != nil {
		return Assessment{}, err
	}
	input := AssessmentInput{ToolID: item.UpstreamID + ":" + item.OriginalName, OriginalName: item.OriginalName,
		ExposedName: item.ExposedName, Description: item.Description, InputSchema: item.InputSchema}
	results, _, err := s.assessWithRetry(ctx, p, key, []AssessmentInput{input})
	if err != nil {
		return Assessment{}, err
	}
	updated, err := s.catalog.ApplyAIResult(ctx, item.UpstreamID, item.OriginalName, results[0], p)
	if err == nil {
		s.notifyCatalogChange()
	}
	return updated, err
}

func (s *GovernanceService) Resume(ctx context.Context) error {
	if err := s.jobs.RecoverRunning(ctx); err != nil {
		return err
	}
	jobs, err := s.jobs.ListQueued(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		s.launchJob(context.WithoutCancel(ctx), job)
	}
	return nil
}

func (s *GovernanceService) TriggerAutoAssessment(ctx context.Context) error {
	s.autoTriggerMu.Lock()
	trigger := s.autoTrigger
	if trigger == nil {
		trigger = &autoAssessmentTrigger{done: make(chan struct{})}
		s.autoTrigger = trigger
		time.AfterFunc(autoAssessmentDebounce, func() {
			s.autoTriggerMu.Lock()
			if s.autoTrigger == trigger {
				s.autoTrigger = nil
			}
			s.autoTriggerMu.Unlock()

			// 定时器开始执行后来的同步事件应进入下一轮，而不是和正在创建的任务共用
			// 同一结果；这样运行期新增工具会得到一个受控的跟进任务。
			trigger.err = s.triggerAutoAssessmentNow(context.WithoutCancel(ctx))
			close(trigger.done)
		})
	}
	s.autoTriggerMu.Unlock()

	select {
	case <-trigger.done:
		return trigger.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *GovernanceService) triggerAutoAssessmentNow(ctx context.Context) error {
	_, err := s.queueAssessment(ctx, 500, "pending", true)
	if err != nil {
		if apiErr, ok := err.(*domain.APIError); ok && (apiErr.Code == domain.CodeConflict || apiErr.Code == domain.CodeNotFound) {
			// 没有活动 Provider、未开启自动评级、没有待评级工具或已有受控跟进任务时，
			// 自动触发无需向同步流程报告错误。
			return nil
		}
	}
	return err
}

func (s *GovernanceService) launchJob(ctx context.Context, job AssessmentJob) {
	if _, loaded := s.running.LoadOrStore(job.ProviderID, job.ID); loaded {
		return
	}
	go func() {
		defer func() {
			s.running.Delete(job.ProviderID)
			queued, err := s.jobs.ListQueued(ctx)
			if err != nil {
				return
			}
			for _, next := range queued {
				if next.ProviderID == job.ProviderID {
					s.launchJob(ctx, next)
					return
				}
			}
		}()
		_ = s.runJob(ctx, job)
	}()
}

func (s *GovernanceService) runJob(ctx context.Context, job AssessmentJob) error {
	if err := s.jobs.SetRunning(ctx, job.ID); err != nil {
		var apiErr *domain.APIError
		if errors.As(err, &apiErr) && apiErr.Code == domain.CodeConflict {
			// 任务可能已被另一实例认领或被管理员取消。此时不能再写入失败状态，
			// 否则会覆盖实际仍在执行的任务状态。
			return err
		}
		// SetRunning 失败（DB 短暂不可用）时，将 job 标记为失败，避免永久卡在 queued 状态。
		job.Status, job.LastError = JobFailed, err.Error()
		_ = s.jobs.UpdateProgress(ctx, job)
		return err
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	job.Status = JobRunning
	limit := 500
	if raw, ok := job.ScopePayload["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	}
	p, key, err := s.providerWithKey(ctx, job.ProviderID)
	if err != nil {
		job.Status, job.LastError = JobFailed, err.Error()
		_ = s.jobs.UpdateProgress(ctx, job)
		return err
	}
	if !p.Enabled {
		// Provider 被停用后，已排队的任务不能继续消耗该 Provider；正在执行中的
		// 任务会持有启动时的快照并自行完成，这里只阻止尚未开始的新任务。
		job.Status, job.LastError = JobCancelled, "Provider 已停用，已取消评级任务"
		_ = s.jobs.UpdateProgress(ctx, job)
		return nil
	}
	var items []Assessment
	if job.Scope == "needs_review" {
		items, err = s.catalog.ListNeedsReview(ctx, limit)
	} else {
		items, err = s.catalog.ListAssessable(ctx, limit)
	}
	if err != nil {
		job.Status, job.LastError = JobFailed, err.Error()
		_ = s.jobs.UpdateProgress(ctx, job)
		return err
	}
	if job.ErrorCounts == nil {
		job.ErrorCounts = map[string]int{}
	}
	batches := make(chan []Assessment)
	outcomes := make(chan batchOutcome)
	workerCount := min(max(p.MaxConcurrency, 1), 3)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for batch := range batches {
				outcome := s.processBatch(jobCtx, p, key, batch, true)
				select {
				case outcomes <- outcome:
				case <-jobCtx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(batches)
		for start := 0; start < len(items); start += p.BatchSize {
			end := min(start+p.BatchSize, len(items))
			select {
			case batches <- items[start:end]:
			case <-jobCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(outcomes)
	}()
	cancelled := false
	for outcome := range outcomes {
		if cancelled {
			continue
		}
		current, getErr := s.jobs.Get(ctx, job.ID)
		if getErr != nil {
			cancel()
			return getErr
		}
		if current.Status == JobCancelled {
			cancelled = true
			cancel()
			continue
		}
		job.ProcessedCount += outcome.processed
		job.SuccessCount += outcome.success
		job.ReviewCount += outcome.review
		job.FailureCount += outcome.failure
		job.RetryCount += outcome.retries
		job.SplitCount += outcome.splits
		if outcome.lastError != "" {
			job.LastError = outcome.lastError
		}
		for category, count := range outcome.errors {
			job.ErrorCounts[category] += count
		}
		if outcome.success > 0 {
			s.notifyCatalogChange()
		}
		_ = s.jobs.UpdateProgress(ctx, job)
	}
	if cancelled {
		return nil
	}
	switch {
	case job.FailureCount == 0:
		job.Status = JobCompleted
	case job.SuccessCount > 0:
		job.Status = JobPartial
	default:
		job.Status = JobFailed
	}
	return s.jobs.UpdateProgress(ctx, job)
}

func (s *GovernanceService) AssessPending(ctx context.Context, limit int) (AssessSummary, error) {
	p, key, err := s.activeProviderWithKey(ctx)
	if err != nil {
		return AssessSummary{}, err
	}
	items, err := s.catalog.ListAssessable(ctx, limit)
	if err != nil {
		return AssessSummary{}, err
	}
	summary := AssessSummary{}
	batchSize := p.BatchSize
	for start := 0; start < len(items); start += batchSize {
		end := min(start+batchSize, len(items))
		batch := items[start:end]
		inputs := make([]AssessmentInput, 0, len(batch))
		for _, item := range batch {
			inputs = append(inputs, AssessmentInput{ToolID: item.UpstreamID + ":" + item.OriginalName, OriginalName: item.OriginalName, ExposedName: item.ExposedName, Description: item.Description, InputSchema: item.InputSchema})
		}
		results, _, assessErr := s.assessWithRetry(ctx, p, key, inputs)
		if assessErr != nil {
			for _, item := range batch {
				_ = s.catalog.MarkAIError(ctx, item.UpstreamID, item.OriginalName, assessErr.Error())
				summary.Failed++
				summary.Processed++
			}
			continue
		}
		byID := make(map[string]Assessment, len(batch))
		for _, item := range batch {
			byID[item.UpstreamID+":"+item.OriginalName] = item
		}
		for _, result := range results {
			item := byID[result.ToolID]
			updated, updateErr := s.catalog.ApplyAIResult(ctx, item.UpstreamID, item.OriginalName, result, p)
			summary.Processed++
			if updateErr != nil {
				summary.Failed++
				continue
			}
			summary.Succeeded++
			if updated.Status == StatusNeedsReview {
				summary.Review++
			}
		}
	}
	if summary.Succeeded > 0 {
		s.notifyCatalogChange()
	}
	return summary, nil
}

type batchOutcome struct {
	processed int
	success   int
	review    int
	failure   int
	retries   int
	splits    int
	errors    map[string]int
	lastError string
}

func (o *batchOutcome) add(other batchOutcome) {
	o.processed += other.processed
	o.success += other.success
	o.review += other.review
	o.failure += other.failure
	o.retries += other.retries
	o.splits += other.splits
	if other.lastError != "" {
		o.lastError = other.lastError
	}
	if o.errors == nil {
		o.errors = map[string]int{}
	}
	for category, count := range other.errors {
		o.errors[category] += count
	}
}

func (s *GovernanceService) processBatch(ctx context.Context, p Provider, key string, batch []Assessment, allowSplit bool) batchOutcome {
	inputs := make([]AssessmentInput, 0, len(batch))
	byID := make(map[string]Assessment, len(batch))
	for _, item := range batch {
		id := item.UpstreamID + ":" + item.OriginalName
		byID[id] = item
		inputs = append(inputs, AssessmentInput{ToolID: id, OriginalName: item.OriginalName, ExposedName: item.ExposedName, Description: item.Description, InputSchema: item.InputSchema})
	}
	results, retries, assessErr := s.assessWithRetry(ctx, p, key, inputs)
	if errors.Is(assessErr, context.Canceled) || errors.Is(assessErr, context.DeadlineExceeded) {
		return batchOutcome{}
	}
	if assessErr != nil && allowSplit && len(batch) > 1 && isRetryableProviderError(assessErr) {
		middle := len(batch) / 2
		outcome := batchOutcome{retries: retries, splits: 1, errors: map[string]int{}}
		outcome.add(s.processBatch(ctx, p, key, batch[:middle], false))
		outcome.add(s.processBatch(ctx, p, key, batch[middle:], false))
		return outcome
	}
	if assessErr != nil {
		category := assessmentErrorCategory(assessErr)
		for _, item := range batch {
			_ = s.catalog.MarkAIError(ctx, item.UpstreamID, item.OriginalName, assessErr.Error())
		}
		return batchOutcome{processed: len(batch), failure: len(batch), retries: retries,
			errors: map[string]int{category: len(batch)}, lastError: assessErr.Error()}
	}
	outcome := batchOutcome{retries: retries, errors: map[string]int{}}
	for _, result := range results {
		item := byID[result.ToolID]
		updated, updateErr := s.catalog.ApplyAIResult(ctx, item.UpstreamID, item.OriginalName, result, p)
		outcome.processed++
		if updateErr != nil {
			outcome.failure++
			outcome.lastError = updateErr.Error()
			outcome.errors["storage"]++
			continue
		}
		outcome.success++
		if updated.Status == StatusNeedsReview {
			outcome.review++
		}
	}
	return outcome
}

func isRetryableProviderError(err error) bool {
	var requestErr *ProviderRequestError
	return errors.As(err, &requestErr) && requestErr.Retryable
}

func assessmentErrorCategory(err error) string {
	var requestErr *ProviderRequestError
	if errors.As(err, &requestErr) {
		if requestErr.StatusCode != 0 {
			return fmt.Sprintf("http_%d", requestErr.StatusCode)
		}
		return "network"
	}
	return "response_validation"
}

func (s *GovernanceService) assessWithRetry(ctx context.Context, p Provider, key string, inputs []AssessmentInput) ([]AIResult, int, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		results, err := s.client.Assess(ctx, p, key, inputs)
		if err == nil {
			return results, attempt, nil
		}
		last = err
		var requestErr *ProviderRequestError
		if !errors.As(err, &requestErr) || !requestErr.Retryable || attempt == 2 {
			return nil, attempt, err
		}
		delay := min(time.Duration(500*(1<<attempt))*time.Millisecond, 4*time.Second)
		if requestErr.RetryAfter > delay {
			delay = min(requestErr.RetryAfter, 30*time.Second)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, 2, last
}

func (s *GovernanceService) providerWithKey(ctx context.Context, id string) (Provider, string, error) {
	p, err := s.providers.Get(ctx, id)
	if err != nil {
		return Provider{}, "", err
	}
	return p, p.APIKey, nil
}
func (s *GovernanceService) activeProviderWithKey(ctx context.Context) (Provider, string, error) {
	p, err := s.providers.Active(ctx)
	if err != nil {
		return Provider{}, "", err
	}
	return p, p.APIKey, nil
}

func providerFromInput(in ProviderInput) Provider {
	style := in.APIStyle
	if style == "" {
		style = APIStyleChatCompletions
	}
	timeout := in.TimeoutS
	if timeout == 0 {
		timeout = 60
	}
	batch := in.BatchSize
	if batch == 0 {
		batch = 10
	}
	concurrency := in.MaxConcurrency
	if concurrency == 0 {
		concurrency = 1
	}
	return Provider{Name: strings.TrimSpace(in.Name), BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), APIStyle: style,
		Model: strings.TrimSpace(in.Model), APIKey: in.APIKey, Enabled: in.Enabled,
		TimeoutS: timeout, BatchSize: batch, MaxConcurrency: concurrency, AutoAssess: in.AutoAssess}
}

package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

const (
	EventAuthFailed = "auth_failed"
	EventACLDenied  = "acl_denied"
	EventBlocked    = "blocked"
	EventReleased   = "released"

	SubjectIP             = "ip"
	SubjectKeyFingerprint = "key_fingerprint"
	SubjectAPIKeyIP       = "api_key_ip"

	reasonMissingKey = "missing_key"
	reasonInvalidKey = "invalid_key"
	reasonACLDenied  = "acl_denied"
)

type ConfigProvider interface {
	Config() config.YAMLConfig
}

type Repository interface {
	InsertEvent(ctx context.Context, ev store.SecurityEvent) (store.SecurityEvent, error)
	CreateBlock(ctx context.Context, block store.SecurityBlock) (store.SecurityBlock, error)
	ListEvents(ctx context.Context, query store.SecurityEventQuery) ([]store.SecurityEvent, error)
	ListBlocks(ctx context.Context, query store.SecurityBlockQuery) ([]store.SecurityBlock, error)
	ReleaseBlock(ctx context.Context, id string, releasedAt time.Time) (store.SecurityBlock, error)
	MarkExpiredBlocks(ctx context.Context, now time.Time) (int64, error)
	ListActiveBlocks(ctx context.Context, now time.Time) ([]store.SecurityBlock, error)
	CountBlocksBySubjectSince(ctx context.Context, subjectType, subject string, since time.Time) (int64, error)
	Summary(ctx context.Context, now time.Time) (store.SecuritySummary, error)
}

type Cache interface {
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	SetBlock(ctx context.Context, subjectType, subject string, ttl time.Duration) error
	IsBlocked(ctx context.Context, subjectType, subject string) (bool, error)
	DeleteBlock(ctx context.Context, subjectType, subject string) error
}

type APIKeyResolver func(c *gin.Context) (id, prefix string, ok bool)

type Guard struct {
	repo   Repository
	cache  Cache
	cfg    ConfigProvider
	logger *slog.Logger
	now    func() time.Time
}

func NewGuard(repo Repository, cache Cache, cfg ConfigProvider, logger *slog.Logger) *Guard {
	if logger == nil {
		logger = slog.Default()
	}
	return &Guard{repo: repo, cache: cache, cfg: cfg, logger: logger, now: time.Now}
}

func (g *Guard) PreAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if g == nil || !g.enforcing() {
			c.Next()
			return
		}

		cfg := g.securityConfig()
		clientIP := g.clientIP(c, cfg)
		if g.exempt(clientIP, cfg) {
			c.Next()
			return
		}
		if g.isBlocked(c.Request.Context(), SubjectIP, clientIP) {
			g.abortBlocked(c)
			return
		}
		if plaintext, ok := apikey.ExtractAPIKey(c); ok {
			fp := g.keyFingerprint(plaintext)
			if fp != "" && g.isBlocked(c.Request.Context(), SubjectKeyFingerprint, fp) {
				g.abortBlocked(c)
				return
			}
		}
		c.Next()
	}
}

func (g *Guard) PostAuthMiddleware(resolve APIKeyResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if g == nil || !g.enforcing() || resolve == nil {
			c.Next()
			return
		}
		apiKeyID, _, ok := resolve(c)
		if !ok || apiKeyID == "" {
			c.Next()
			return
		}
		clientIP := g.clientIP(c, g.securityConfig())
		if g.isBlocked(c.Request.Context(), SubjectAPIKeyIP, apiKeyIPSubject(apiKeyID, clientIP)) {
			g.abortBlocked(c)
			return
		}
		c.Next()
	}
}

func (g *Guard) RecordAuthFailure(c *gin.Context, plaintext, reason string) {
	if g == nil || !g.recording() {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = reasonInvalidKey
	}
	cfg := g.securityConfig()
	clientIP := g.clientIP(c, cfg)
	fingerprint := ""
	if plaintext != "" {
		fingerprint = g.keyFingerprint(plaintext)
	}

	ipCount := g.increment(c.Request.Context(), countKey(SubjectIP, clientIP, g.windowStart(cfg)), time.Duration(cfg.FailureWindowS)*time.Second)
	g.recordSampledEvent(c, store.SecurityEvent{
		EventType:      EventAuthFailed,
		SubjectType:    SubjectIP,
		Subject:        clientIP,
		ClientIP:       clientIP,
		KeyFingerprint: fingerprint,
		Reason:         reason,
		Count:          int(ipCount),
	})
	if g.shouldBlock(ipCount, cfg.MaxFailuresPerIP) && !g.exempt(clientIP, cfg) {
		g.block(c, SubjectIP, clientIP, clientIP, "", "", fingerprint, reason, int(ipCount), cfg)
	}

	if fingerprint == "" {
		return
	}
	fpCount := g.increment(c.Request.Context(), countKey(SubjectKeyFingerprint, fingerprint, g.windowStart(cfg)), time.Duration(cfg.FailureWindowS)*time.Second)
	if g.shouldRecordEvent(fpCount, cfg.MaxFailuresPerKeyFingerprint) {
		g.insertEvent(c.Request.Context(), withRequest(c, store.SecurityEvent{
			EventType:      EventAuthFailed,
			SubjectType:    SubjectKeyFingerprint,
			Subject:        fingerprint,
			ClientIP:       clientIP,
			KeyFingerprint: fingerprint,
			Reason:         reason,
			Count:          int(fpCount),
		}))
	}
	if g.shouldBlock(fpCount, cfg.MaxFailuresPerKeyFingerprint) && !g.exempt(clientIP, cfg) {
		g.block(c, SubjectKeyFingerprint, fingerprint, clientIP, "", "", fingerprint, reason, int(fpCount), cfg)
	}
}

func (g *Guard) RecordACLDenied(c *gin.Context, apiKeyID, keyPrefix string) {
	if g == nil || !g.recording() || strings.TrimSpace(apiKeyID) == "" {
		return
	}
	cfg := g.securityConfig()
	clientIP := g.clientIP(c, cfg)
	subject := apiKeyIPSubject(apiKeyID, clientIP)
	n := g.increment(c.Request.Context(), countKey(SubjectAPIKeyIP, subject, g.windowStart(cfg)), time.Duration(cfg.FailureWindowS)*time.Second)
	g.recordSampledEvent(c, store.SecurityEvent{
		EventType:    EventACLDenied,
		SubjectType:  SubjectAPIKeyIP,
		Subject:      subject,
		ClientIP:     clientIP,
		APIKeyID:     apiKeyID,
		APIKeyPrefix: keyPrefix,
		Reason:       reasonACLDenied,
		Count:        int(n),
	})
	if g.shouldBlock(n, cfg.MaxACLDeniesPerKeyIP) && !g.exempt(clientIP, cfg) {
		g.block(c, SubjectAPIKeyIP, subject, clientIP, apiKeyID, keyPrefix, "", reasonACLDenied, int(n), cfg)
	}
}

func (g *Guard) Summary(ctx context.Context) (store.SecuritySummary, error) {
	if g.repo == nil {
		return store.SecuritySummary{}, domain.NewError(domain.CodeValidation, "安全中心服务未就绪")
	}
	now := g.now()
	_, _ = g.repo.MarkExpiredBlocks(ctx, now)
	return g.repo.Summary(ctx, now)
}

func (g *Guard) ListEvents(ctx context.Context, query store.SecurityEventQuery) ([]store.SecurityEvent, error) {
	if g.repo == nil {
		return nil, domain.NewError(domain.CodeValidation, "安全中心服务未就绪")
	}
	return g.repo.ListEvents(ctx, query)
}

func (g *Guard) ListBlocks(ctx context.Context, query store.SecurityBlockQuery) ([]store.SecurityBlock, error) {
	if g.repo == nil {
		return nil, domain.NewError(domain.CodeValidation, "安全中心服务未就绪")
	}
	_, _ = g.repo.MarkExpiredBlocks(ctx, g.now())
	return g.repo.ListBlocks(ctx, query)
}

func (g *Guard) ReleaseBlock(ctx context.Context, id string) (store.SecurityBlock, error) {
	if g.repo == nil {
		return store.SecurityBlock{}, domain.NewError(domain.CodeValidation, "安全中心服务未就绪")
	}
	released, err := g.repo.ReleaseBlock(ctx, id, g.now())
	if err != nil {
		return store.SecurityBlock{}, err
	}
	if g.cache != nil {
		if err := g.cache.DeleteBlock(ctx, released.SubjectType, released.Subject); err != nil {
			g.logger.Warn("删除安全封禁缓存失败", "blockID", id, "error", err)
		}
	}
	_, _ = g.repo.InsertEvent(ctx, store.SecurityEvent{
		EventType:      EventReleased,
		SubjectType:    released.SubjectType,
		Subject:        released.Subject,
		ClientIP:       released.ClientIP,
		APIKeyID:       released.APIKeyID,
		APIKeyPrefix:   released.APIKeyPrefix,
		KeyFingerprint: released.KeyFingerprint,
		Reason:         released.Reason,
		Count:          released.FailureCount,
		CreatedAt:      g.now(),
	})
	return released, nil
}

func (g *Guard) RestoreActiveBlocks(ctx context.Context) error {
	if g == nil || g.repo == nil || g.cache == nil {
		return nil
	}
	now := g.now()
	_, _ = g.repo.MarkExpiredBlocks(ctx, now)
	blocks, err := g.repo.ListActiveBlocks(ctx, now)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		if block.Subject == "" {
			continue
		}
		ttl := time.Hour
		if block.BlockedUntil != nil {
			ttl = block.BlockedUntil.Sub(now)
		}
		if ttl <= 0 {
			continue
		}
		if err := g.cache.SetBlock(ctx, block.SubjectType, block.Subject, ttl); err != nil {
			g.logger.Warn("恢复安全封禁缓存失败", "blockID", block.ID, "error", err)
		}
	}
	return nil
}

func (g *Guard) recording() bool {
	mode := g.securityConfig().Mode
	return mode == config.SecurityModeMonitor || mode == config.SecurityModeEnforce
}

func (g *Guard) enforcing() bool {
	return g.securityConfig().Mode == config.SecurityModeEnforce
}

func (g *Guard) securityConfig() config.SecurityConfig {
	if g == nil || g.cfg == nil {
		return config.DefaultYAMLConfig().Security
	}
	return config.NormalizeYAMLConfig(g.cfg.Config()).Security
}

func (g *Guard) increment(ctx context.Context, key string, ttl time.Duration) int64 {
	if g.cache == nil {
		return 0
	}
	n, err := g.cache.Incr(ctx, key, ttl)
	if err != nil {
		g.logger.Warn("安全计数写入失败", "error", err)
		return 0
	}
	return n
}

func (g *Guard) shouldBlock(count int64, threshold int) bool {
	return g.enforcing() && threshold > 0 && count >= int64(threshold)
}

func (g *Guard) shouldRecordEvent(count int64, threshold int) bool {
	if count <= 1 {
		return true
	}
	if threshold > 0 && count == int64(threshold) {
		return true
	}
	return count > 0 && count%10 == 0
}

func (g *Guard) recordSampledEvent(c *gin.Context, event store.SecurityEvent) {
	threshold := 0
	switch event.SubjectType {
	case SubjectIP:
		threshold = g.securityConfig().MaxFailuresPerIP
	case SubjectKeyFingerprint:
		threshold = g.securityConfig().MaxFailuresPerKeyFingerprint
	case SubjectAPIKeyIP:
		threshold = g.securityConfig().MaxACLDeniesPerKeyIP
	}
	if g.shouldRecordEvent(int64(event.Count), threshold) {
		g.insertEvent(c.Request.Context(), withRequest(c, event))
	}
}

func (g *Guard) insertEvent(ctx context.Context, event store.SecurityEvent) {
	if g.repo == nil {
		return
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = g.now()
	}
	if _, err := g.repo.InsertEvent(ctx, event); err != nil {
		g.logger.Warn("记录安全事件失败", "eventType", event.EventType, "error", err)
	}
}

func (g *Guard) block(c *gin.Context, subjectType, subject, clientIP, apiKeyID, apiKeyPrefix, fingerprint, reason string, failureCount int, cfg config.SecurityConfig) {
	duration := g.blockDuration(c.Request.Context(), subjectType, subject, cfg)
	until := g.now().Add(duration)
	if g.cache != nil {
		if err := g.cache.SetBlock(c.Request.Context(), subjectType, subject, duration); err != nil {
			g.logger.Warn("写入安全封禁缓存失败", "subjectType", subjectType, "subject", subject, "error", err)
		}
	}
	block := store.SecurityBlock{
		SubjectType:    subjectType,
		Subject:        subject,
		ClientIP:       clientIP,
		APIKeyID:       apiKeyID,
		APIKeyPrefix:   apiKeyPrefix,
		KeyFingerprint: fingerprint,
		Reason:         reason,
		FailureCount:   failureCount,
		Status:         store.SecurityBlockStatusActive,
		BlockedUntil:   &until,
	}
	if g.repo != nil {
		created, err := g.repo.CreateBlock(c.Request.Context(), block)
		if err != nil {
			g.logger.Warn("创建安全封禁记录失败", "subjectType", subjectType, "subject", subject, "error", err)
		} else {
			block = created
		}
	}
	g.insertEvent(c.Request.Context(), withRequest(c, store.SecurityEvent{
		EventType:      EventBlocked,
		SubjectType:    subjectType,
		Subject:        subject,
		ClientIP:       clientIP,
		APIKeyID:       apiKeyID,
		APIKeyPrefix:   apiKeyPrefix,
		KeyFingerprint: fingerprint,
		Reason:         reason,
		Count:          failureCount,
	}))
}

func (g *Guard) blockDuration(ctx context.Context, subjectType, subject string, cfg config.SecurityConfig) time.Duration {
	base := time.Duration(cfg.FirstBlockDurationS) * time.Second
	maxDuration := time.Duration(cfg.MaxBlockDurationS) * time.Second
	if base <= 0 {
		base = 15 * time.Minute
	}
	if maxDuration < base {
		maxDuration = base
	}
	var previous int64
	if g.repo != nil && cfg.EscalationWindowS > 0 {
		n, err := g.repo.CountBlocksBySubjectSince(ctx, subjectType, subject, g.now().Add(-time.Duration(cfg.EscalationWindowS)*time.Second))
		if err != nil {
			g.logger.Warn("查询重复封禁次数失败", "subjectType", subjectType, "subject", subject, "error", err)
		} else {
			previous = n
		}
	}
	multiplier := math.Pow(2, float64(previous))
	duration := time.Duration(float64(base) * multiplier)
	if duration > maxDuration || duration <= 0 {
		return maxDuration
	}
	return duration
}

func (g *Guard) isBlocked(ctx context.Context, subjectType, subject string) bool {
	if g.cache == nil || subject == "" {
		return false
	}
	blocked, err := g.cache.IsBlocked(ctx, subjectType, subject)
	if err != nil {
		g.logger.Warn("读取安全封禁缓存失败", "subjectType", subjectType, "error", err)
		return false
	}
	return blocked
}

func (g *Guard) abortBlocked(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, domain.NewError(domain.CodeForbidden, "访问暂时受限，请稍后再试"))
}

func (g *Guard) windowStart(cfg config.SecurityConfig) int64 {
	window := int64(cfg.FailureWindowS)
	if window <= 0 {
		window = 300
	}
	now := g.now().Unix()
	return (now / window) * window
}

func countKey(subjectType, subject string, windowStart int64) string {
	return fmt.Sprintf("mpg:sec:cnt:%s:%s:%d", subjectType, subjectHash(subject), windowStart)
}

func apiKeyIPSubject(apiKeyID, clientIP string) string {
	return apiKeyID + "|" + clientIP
}

func subjectHash(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:16])
}

func (g *Guard) keyFingerprint(plaintext string) string {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return ""
	}
	secret := ""
	if g.cfg != nil {
		secret = g.cfg.Config().JWTSecret
	}
	if secret == "" {
		sum := sha256.Sum256([]byte(plaintext))
		return hex.EncodeToString(sum[:16])
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func withRequest(c *gin.Context, event store.SecurityEvent) store.SecurityEvent {
	event.Method = c.Request.Method
	event.Path = c.Request.URL.Path
	event.UserAgent = truncate(c.Request.UserAgent(), 512)
	return event
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func (g *Guard) clientIP(c *gin.Context, cfg config.SecurityConfig) string {
	remoteIP := remoteAddrIP(c.Request.RemoteAddr)
	if remoteIP == "" {
		remoteIP = c.ClientIP()
	}
	if remoteIP != "" && matchCIDRs(remoteIP, cfg.TrustedProxyCIDRs) {
		if forwarded := firstForwardedIP(c.GetHeader("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
		if realIP := strings.TrimSpace(c.GetHeader("X-Real-IP")); realIP != "" && parseAddr(realIP) != "" {
			return parseAddr(realIP)
		}
	}
	return parseAddr(remoteIP)
}

func (g *Guard) exempt(clientIP string, cfg config.SecurityConfig) bool {
	return clientIP != "" && matchCIDRs(clientIP, cfg.ExemptCIDRs)
}

func remoteAddrIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return parseAddr(host)
	}
	return parseAddr(remoteAddr)
}

func firstForwardedIP(header string) string {
	for _, part := range strings.Split(header, ",") {
		if ip := parseAddr(strings.TrimSpace(part)); ip != "" {
			return ip
		}
	}
	return ""
}

func parseAddr(s string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return addr.Unmap().String()
}

func matchCIDRs(ip string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Masked().Contains(addr) {
			return true
		}
		if single, err := netip.ParseAddr(raw); err == nil && single.Unmap() == addr {
			return true
		}
	}
	return false
}

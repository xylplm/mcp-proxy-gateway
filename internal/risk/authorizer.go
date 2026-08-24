package risk

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type ProfileReader interface {
	RiskProfile(ctx context.Context, apiKeyID string) (Profile, error)
}

type AssessmentReader interface {
	Get(ctx context.Context, upstreamID, originalName string) (Assessment, error)
	ListByUpstream(ctx context.Context, upstreamID string) ([]Assessment, error)
}

type Authorizer struct {
	profiles    ProfileReader
	assessments AssessmentReader
}

func NewAuthorizer(profiles ProfileReader, assessments AssessmentReader) *Authorizer {
	return &Authorizer{profiles: profiles, assessments: assessments}
}

func (a *Authorizer) FilterSources(ctx context.Context, apiKeyID, upstreamID string, tools []domain.ToolDef) ([]domain.ToolDef, error) {
	if apiKeyID == "" {
		return append([]domain.ToolDef(nil), tools...), nil
	}
	profile, err := a.profiles.RiskProfile(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if profile == ProfileLegacy {
		return append([]domain.ToolDef(nil), tools...), nil
	}
	assessments, err := a.assessments.ListByUpstream(ctx, upstreamID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Assessment, len(assessments))
	for _, assessment := range assessments {
		byName[assessment.OriginalName] = assessment
	}
	out := make([]domain.ToolDef, 0, len(tools))
	for _, tool := range tools {
		assessment, ok := byName[tool.OriginalName]
		level := LevelHigh
		if ok {
			level = EffectiveLevel(assessment)
		}
		if ProfileAllows(profile, level) {
			out = append(out, tool)
		}
	}
	return out, nil
}

func (a *Authorizer) AuthorizeSource(ctx context.Context, apiKeyID, upstreamID, originalName string) error {
	if apiKeyID == "" {
		return nil
	}
	profile, err := a.profiles.RiskProfile(ctx, apiKeyID)
	if err != nil {
		return err
	}
	if profile == ProfileLegacy {
		return nil
	}
	level := LevelHigh
	assessment, err := a.assessments.Get(ctx, upstreamID, originalName)
	if err == nil {
		level = EffectiveLevel(assessment)
	} else if apiErr, ok := err.(*domain.APIError); !ok || apiErr.Code != domain.CodeNotFound {
		return err
	}
	if !ProfileAllows(profile, level) {
		return domain.NewError(domain.CodeToolRiskForbidden, "当前 API Key 风险档案不允许调用该工具")
	}
	return nil
}

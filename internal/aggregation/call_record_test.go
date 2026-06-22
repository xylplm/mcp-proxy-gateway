package aggregation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type callRecordRecorder struct {
	rec store.CallStatRecord
}

func (r *callRecordRecorder) RecordAsync(_ context.Context, rec store.CallStatRecord) {
	r.rec = rec
}

func TestRecordCallStoresTimeoutFailureDetail(t *testing.T) {
	recorder := &callRecordRecorder{}
	svc := &Service{recorder: recorder}

	svc.recordCall(
		context.Background(),
		"key-1",
		"slow_tool",
		ToolCandidate{UpstreamID: "up-1", OriginalName: "slow_tool"},
		time.Now().Add(-time.Second),
		json.RawMessage(`{"q":"x"}`),
		domain.ToolResult{},
		domain.NewError(domain.CodeUpstreamTimeout, "timeout"),
	)

	if recorder.rec.Success {
		t.Fatal("timeout call should not be successful")
	}
	if recorder.rec.Status != store.CallStatusFailed {
		t.Fatalf("status mismatch: got=%q want=%q", recorder.rec.Status, store.CallStatusFailed)
	}
	if recorder.rec.ErrorMessage == "" {
		t.Fatal("timeout call should keep error message")
	}
	var detail struct {
		Code         string `json:"code"`
		HTTPStatus   int    `json:"httpStatus"`
		BusinessCode int    `json:"businessCode"`
		Timeout      bool   `json:"timeout"`
	}
	if err := json.Unmarshal(recorder.rec.FailureDetail, &detail); err != nil {
		t.Fatalf("failure detail should be valid JSON: %v", err)
	}
	if detail.Code != string(domain.CodeUpstreamTimeout) || detail.HTTPStatus != 504 || detail.BusinessCode != 50400 || !detail.Timeout {
		t.Fatalf("timeout failure detail incomplete: %+v", detail)
	}
}

func TestRecordCallStoresUpstreamErrorStatus(t *testing.T) {
	recorder := &callRecordRecorder{}
	svc := &Service{recorder: recorder}

	svc.recordCall(context.Background(), "", "search", ToolCandidate{UpstreamID: "up-1", OriginalName: "search"}, time.Now(), json.RawMessage(`{}`), domain.ToolResult{
		IsError: true,
		Content: json.RawMessage(`[{"type":"text","text":"bad request"}]`),
	}, nil)

	if recorder.rec.Success {
		t.Fatal("upstream error result should not be successful")
	}
	if recorder.rec.Status != store.CallStatusUpstreamError {
		t.Fatalf("status mismatch: got=%q want=%q", recorder.rec.Status, store.CallStatusUpstreamError)
	}
	if len(recorder.rec.ResponseResult) == 0 {
		t.Fatal("upstream error should keep response result")
	}
	if len(recorder.rec.FailureDetail) == 0 {
		t.Fatal("upstream error should keep failure detail")
	}
}

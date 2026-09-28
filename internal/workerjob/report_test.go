//go:build linux || darwin

package workerjob

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func TestReportingDoesNotChangeExecutionOrCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		result := Result{CampaignID: "campaign-1", Phase: "finished", Code: "execution_finished", CompletionRecording: "recorded", Execution: &hostworker.Result{ContainerCleanup: "removed"}}
		finishReport("/state", &result, func(ctx context.Context, root, id, output string) (reporting.Receipt, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Minute || ctx.Err() != nil || root != "/state" || id != "campaign-1" || output != "" {
				t.Fatal("incorrect report context")
			}
			if fail {
				return reporting.Receipt{}, errors.New("report disk failure")
			}
			return reporting.Receipt{CampaignID: id}, nil
		})
		if result.Phase != "finished" || result.Code != "execution_finished" || result.CompletionRecording != "recorded" {
			t.Fatal("report changed execution", result)
		}
		if fail {
			if result.Reporting != "report_failed" || result.Report != nil {
				t.Fatal(result)
			}
		} else if result.Reporting != "generated" || result.Report == nil {
			t.Fatal(result)
		}
	}
	result := Result{}
	finishReport("/state", &result, func(context.Context, string, string, string) (reporting.Receipt, error) {
		t.Fatal("report before execution")
		return reporting.Receipt{}, nil
	})
	if result.Reporting != "not_started" {
		t.Fatal(result)
	}
}

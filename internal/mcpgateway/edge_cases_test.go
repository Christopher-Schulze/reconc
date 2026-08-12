package mcpgateway

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
)

type blockingDiagnosticWriter struct {
	firstEntered  chan struct{}
	secondEntered chan struct{}
	release       chan struct{}
	writes        int
}

func (w *blockingDiagnosticWriter) Write(payload []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		close(w.firstEntered)
	} else {
		close(w.secondEntered)
	}
	<-w.release
	return len(payload), nil
}

func TestProtocolRequestMethodPreservesOnlyTheMethodComponent(t *testing.T) {
	for _, test := range []struct {
		request string
		want    string
	}{
		{request: "tools/call\x00canonical-params", want: "tools/call"},
		{request: "tools/list", want: "tools/list"},
		{request: "\x00params", want: ""},
	} {
		if got := protocolRequestMethod(test.request); got != test.want {
			t.Errorf("protocolRequestMethod(%q) = %q, want %q", test.request, got, test.want)
		}
	}
}

func TestFailureHelpersKeepOnlySafeExactSemantics(t *testing.T) {
	for _, reason := range []action.ReasonCode{
		action.ReasonInspectionIncomplete,
		action.ReasonUnsupportedContent,
		action.ReasonSchemaInvalid,
		action.ReasonLimitExceeded,
		action.ReasonInvalidUTF8,
		action.ReasonCancelled,
		action.ReasonDeadlineExceeded,
	} {
		if got := inspectionFailureReason(reason); got != reason {
			t.Errorf("inspectionFailureReason(%s) = %s", reason, got)
		}
	}
	if got := inspectionFailureReason(action.ReasonPolicyStale); got != action.ReasonInspectionIncomplete {
		t.Fatalf("unsafe inspection reason mapped to %s", got)
	}

	if got := postFailureSchemaStatus(nil); got != action.InspectionSchemaNotDeclared {
		t.Fatalf("nil output schema status = %s", got)
	}
	call := &gatewayCall{contract: ToolContract{OutputSchema: &actioninspect.OutputSchema{}}}
	if got := postFailureSchemaStatus(call); got != action.InspectionSchemaRequired {
		t.Fatalf("declared output schema status = %s", got)
	}

	result := incompletePermittingDecision(
		action.EvaluationResult{Completeness: action.CompleteEvidence()},
		action.ReasonStateUnavailable,
	)
	if result.Completeness.PhaseComplete || len(result.Completeness.Missing) != 1 ||
		result.Completeness.Missing[0].Field != action.EvidencePhase ||
		result.Completeness.Missing[0].Reason != action.ReasonStateUnavailable {
		t.Fatalf("incomplete permitting decision = %#v", result.Completeness)
	}
}

func TestGatewayBookkeepingHelpersAreBoundedAndPermanent(t *testing.T) {
	progress := &callProgress{}
	progress.stop()
	if got := progress.admit(context.Background(), ProgressEvent{FrameBytes: 1}); got != action.ReasonLimitExceeded {
		t.Fatalf("stopped progress admitted with reason %s", got)
	}
	decision := progressFailureDecision(
		&gatewayCall{decision: action.EvaluationResult{Completeness: action.CompleteEvidence()}},
		action.ReasonProtocolError,
	)
	if decision.Decision != action.DecisionBlock || decision.PhaseOutcome != action.OutcomeSuppressed ||
		decision.Completeness.PhaseComplete {
		t.Fatalf("progress failure decision = %#v", decision)
	}

	gateway := &Gateway{pending: map[string]pendingApproval{"sealed": {callID: "act_test"}}}
	gateway.removePending("sealed")
	if len(gateway.pending) != 0 {
		t.Fatalf("pending approval was not removed: %#v", gateway.pending)
	}

	diagnostics := &bytes.Buffer{}
	gateway.config.Diagnostics = diagnostics
	gateway.diagnostic("bounded diagnostic")
	if diagnostics.String() != "bounded diagnostic\n" {
		t.Fatalf("diagnostic output = %q", diagnostics.String())
	}
	gateway.config.Diagnostics = nil
	gateway.diagnostic("suppressed")

	diagnostics.Reset()
	gateway.config.Diagnostics = diagnostics
	gateway.diagnostic("first\nsecond\x00third " + string([]byte{0xff}) + strings.Repeat("x", MaxDiagnosticBytes))
	written := strings.TrimSuffix(diagnostics.String(), "\n")
	if !utf8.ValidString(written) || strings.ContainsAny(written, "\r\n\x00") ||
		len(written) > MaxDiagnosticBytes {
		t.Fatalf("diagnostic was not one bounded UTF-8 line: %q", written)
	}
}

func TestGatewaySerializesConcurrentDiagnostics(t *testing.T) {
	writer := &blockingDiagnosticWriter{
		firstEntered: make(chan struct{}), secondEntered: make(chan struct{}),
		release: make(chan struct{}),
	}
	gateway := &Gateway{config: Config{Diagnostics: writer}}
	firstDone := make(chan struct{})
	go func() {
		gateway.diagnostic("first")
		close(firstDone)
	}()
	<-writer.firstEntered
	secondStarted := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		close(secondStarted)
		gateway.diagnostic("second")
		close(secondDone)
	}()
	<-secondStarted
	select {
	case <-writer.secondEntered:
		t.Fatal("diagnostic writer was entered concurrently")
	case <-time.After(25 * time.Millisecond):
	}
	close(writer.release)
	for _, done := range []<-chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("serialized diagnostic write did not finish")
		}
	}
	if writer.writes != 2 {
		t.Fatalf("diagnostic writes = %d, want 2", writer.writes)
	}
}

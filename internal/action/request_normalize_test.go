package action

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeRequestCanonicalizesEquivalentArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "ordered", body: `{"a":1.0,"z":10e-1}`},
		{name: "reordered", body: `{"z":1,"a":10e-1}`},
	}
	digests := make([]string, len(tests))
	for index, test := range tests {
		request, err := NormalizeRequest(testRawRequest([]byte(test.body)))
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		digests[index], err = RequestDigest(request)
		if err != nil {
			t.Fatalf("%s digest: %v", test.name, err)
		}
	}
	if digests[0] != digests[1] {
		t.Fatalf("equivalent request digests differ: %s != %s", digests[0], digests[1])
	}
}

func TestNormalizeRequestAcceptsEveryCanonicalPhaseShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		phase    Phase
		payload  []byte
		validate func(Request) bool
	}{
		{name: "pre call", phase: PhasePreCall, payload: []byte(`{"x":1}`), validate: func(value Request) bool {
			return value.Arguments != nil && value.Result == nil && value.Progress == nil
		}},
		{name: "post result", phase: PhasePostResult, payload: []byte(`{"ok":true}`), validate: func(value Request) bool {
			return value.Arguments == nil && value.Result != nil && value.Progress == nil
		}},
		{name: "progress", phase: PhaseProgress, payload: []byte(`{"percent":50}`), validate: func(value Request) bool {
			return value.Arguments == nil && value.Result == nil && value.Progress != nil
		}},
		{name: "observation", phase: PhaseObservation, validate: func(value Request) bool {
			return value.Arguments == nil && value.Result == nil && value.Progress == nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := testRawRequest(nil)
			raw.Phase = test.phase
			switch test.phase {
			case PhasePreCall:
				raw.Arguments = test.payload
			case PhasePostResult:
				raw.Result = test.payload
			case PhaseProgress:
				raw.Progress = test.payload
			}
			normalized, err := NormalizeRequest(raw)
			if err != nil || !test.validate(normalized) {
				t.Fatalf("phase %s normalization = %#v, %v", test.phase, normalized, err)
			}
		})
	}
}

func TestNormalizeRequestRejectsUnsafeJSONWithStableCodes(t *testing.T) {
	t.Parallel()
	oversizedString := []byte(`{"x":"` + strings.Repeat("x", MaxJSONStringBytes+1) + `"}`)
	tests := []struct {
		name string
		body []byte
		code ReasonCode
	}{
		{name: "duplicate", body: []byte(`{"a":{"x":1,"x":2}}`), code: ReasonDuplicateKey},
		{name: "invalid utf8", body: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, code: ReasonInvalidUTF8},
		{name: "unpaired surrogate", body: []byte(`{"x":"\ud800"}`), code: ReasonInvalidUTF8},
		{name: "trailing", body: []byte(`{} {}`), code: ReasonInvalidRequest},
		{name: "non object", body: []byte(`[]`), code: ReasonInvalidRequest},
		{name: "empty", body: []byte{}, code: ReasonInvalidRequest},
		{name: "oversized string", body: oversizedString, code: ReasonLimitExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeRequest(testRawRequest(test.body))
			var requestError *RequestError
			if !errors.As(err, &requestError) || requestError.Code != test.code {
				t.Fatalf("error = %v, want RequestError %s", err, test.code)
			}
		})
	}
}

func TestTypedUnavailableContextRejectsHiddenValue(t *testing.T) {
	t.Parallel()
	request := testRawRequest([]byte(`{}`))
	normalized, err := NormalizeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	normalized.Context = []ContextValue{{
		Name: "role", Value: testStringValue(t, "hidden"),
		Provenance: ProvenanceHostObserved, Available: false,
	}}
	_, err = validateAndCloneRequest(normalized)
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.Code != ReasonInvalidRequest {
		t.Fatalf("hidden unavailable value error = %v", err)
	}
}

func TestNormalizeRequestRejectsContextAndPhaseAmbiguity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*RawRequest)
		code   ReasonCode
	}{
		{
			name: "duplicate context",
			mutate: func(request *RawRequest) {
				request.Context = []RawContextValue{
					{Name: "role", Value: []byte(`"operator"`), Provenance: ProvenanceOperatorBound, Available: true},
					{Name: "role", Value: []byte(`"guest"`), Provenance: ProvenanceAgentSupplied, Available: true},
				}
			},
			code: ReasonDuplicateKey,
		},
		{
			name: "unavailable value carries bytes",
			mutate: func(request *RawRequest) {
				request.Context = []RawContextValue{{Name: "role", Value: []byte(`"operator"`), Provenance: ProvenanceOperatorBound}}
			},
			code: ReasonInvalidRequest,
		},
		{
			name:   "post payload during pre call",
			mutate: func(request *RawRequest) { request.Result = []byte(`null`) },
			code:   ReasonUnsupportedPhase,
		},
		{
			name:   "missing completeness reason",
			mutate: func(request *RawRequest) { request.Completeness.ContextComplete = false },
			code:   ReasonInvalidRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := testRawRequest([]byte(`{}`))
			test.mutate(&request)
			_, err := NormalizeRequest(request)
			var requestError *RequestError
			if !errors.As(err, &requestError) || requestError.Code != test.code {
				t.Fatalf("error = %v, want RequestError %s", err, test.code)
			}
		})
	}
}

func TestNormalizeRequestAggregateValueByteBoundary(t *testing.T) {
	first := []byte(`"` + strings.Repeat("x", MaxArgumentBytes/2-2) + `"`)
	tests := []struct {
		name    string
		delta   int
		wantErr bool
	}{
		{name: "minus one", delta: -1},
		{name: "exact", delta: 0},
		{name: "plus one", delta: 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			secondLength := MaxArgumentBytes - len([]byte(`{}`)) - len(first) + test.delta
			second := []byte(`"` + strings.Repeat("y", secondLength-2) + `"`)
			request := testRawRequest([]byte(`{}`))
			request.Context = []RawContextValue{
				{Name: "first", Value: first, Provenance: ProvenanceHostObserved, Available: true},
				{Name: "second", Value: second, Provenance: ProvenanceHostObserved, Available: true},
			}
			_, err := NormalizeRequest(request)
			var requestError *RequestError
			if test.wantErr {
				if !errors.As(err, &requestError) || requestError.Code != ReasonLimitExceeded {
					t.Fatalf("error = %v, want aggregate limit rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("boundary request rejected: %v", err)
			}
		})
	}
}

func TestTypedRequestCannotBypassAggregateItemLimit(t *testing.T) {
	t.Parallel()
	left := make([]Value, MaxJSONItems/2)
	right := make([]Value, MaxJSONItems/2)
	for index := range left {
		left[index] = Null()
		right[index] = Null()
	}
	leftValue, err := Array(left)
	if err != nil {
		t.Fatal(err)
	}
	rightValue, err := Array(right)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := Array([]Value{leftValue, rightValue})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := Object([]Member{{Name: "groups", Value: groups}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := validateAndCloneRequest(Request{
		FormatVersion: RequestFormatVersion, CallID: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Transport: TransportMCPStdio, ServerLabel: "database",
		ServerFingerprint: testServerFingerprint, Tool: "execute",
		ToolContractDigest: testToolContractDigest, Phase: PhasePreCall,
		RepositoryIdentity: testRepositoryIdentity, PolicyDigest: testPolicyDigest,
		LockDigest: testLockDigest, AuthorityMode: AuthorityOperatorPinned,
		Arguments: &arguments, Context: []ContextValue{}, Completeness: CompleteEvidence(),
		Deadline: DeadlineReady, StateVersion: "state-v1",
	})
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.Code != ReasonLimitExceeded {
		t.Fatalf("typed aggregate limit result = %#v, %v", request, err)
	}
}

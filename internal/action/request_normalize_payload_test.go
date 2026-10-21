package action

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// A caller that already parsed the phase payload gets the request
// NormalizeRequest builds from the same bytes, with the same rejections.
func TestNormalizeRequestWithPayloadMatchesNormalizeRequest(t *testing.T) {
	phaseRequest := func(phase Phase, payload string) RawRequest {
		raw := testRawRequest(nil)
		raw.Phase = phase
		switch phase {
		case PhasePreCall:
			raw.Arguments = json.RawMessage(payload)
		case PhasePostResult:
			raw.Result = json.RawMessage(payload)
		case PhaseProgress:
			raw.Progress = json.RawMessage(payload)
		}
		return raw
	}
	for _, test := range []struct {
		name    string
		phase   Phase
		payload string
	}{
		{name: "arguments", phase: PhasePreCall, payload: `{"query":"select 1","limit":[1,2.5,"x"]}`},
		{name: "result", phase: PhasePostResult, payload: `{"content":[{"type":"text","text":"ok"}],"isError":false}`},
		{name: "progress", phase: PhaseProgress, payload: `{"progress":1,"progressToken":"t"}`},
		{name: "non-object arguments", phase: PhasePreCall, payload: `["not","an","object"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := phaseRequest(test.phase, test.payload)
			want, wantErr := NormalizeRequest(raw)
			parsed, err := ParseJSON([]byte(test.payload))
			if err != nil {
				t.Fatal(err)
			}
			got, gotErr := NormalizeRequestWithPayload(raw, parsed)
			if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
				t.Fatalf("with payload = %+v, %v; want %+v, %v", got, gotErr, want, wantErr)
			}
			var gotReason, wantReason *RequestError
			if wantErr != nil && (!errors.As(gotErr, &gotReason) || !errors.As(wantErr, &wantReason) || gotReason.Code != wantReason.Code) {
				t.Fatalf("with payload rejected with %v, want %v", gotErr, wantErr)
			}
		})
	}
}

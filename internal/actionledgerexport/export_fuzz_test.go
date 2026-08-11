package actionledgerexport

import (
	"encoding/json"
	"testing"
)

func FuzzMarshal(f *testing.F) {
	seed, err := Marshal(EmptyReport())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"format_version":"reconc.action-ledger-impact-export/v1"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			input = input[:1<<20]
		}
		var report Report
		if err := json.Unmarshal(input, &report); err != nil {
			return
		}
		output, err := Marshal(report)
		if err != nil {
			return
		}
		var decoded Report
		if err := json.Unmarshal(output, &decoded); err != nil || decoded.ReplayComplete {
			t.Fatalf("valid export encoding is contradictory: %v", err)
		}
	})
}

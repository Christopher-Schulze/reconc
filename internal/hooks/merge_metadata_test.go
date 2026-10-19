package hooks

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMergeReportsOrPreservesModifiedHookMetadata(t *testing.T) {
	for _, keep := range []bool{false, true} {
		for _, field := range []string{"matcher", "timeout"} {
			t.Run(field+map[bool]string{false: "/replace", true: "/keep"}[keep], func(t *testing.T) {
				canonical := reconcHookEntry("claude-stop")
				body, err := json.Marshal(canonical)
				if err != nil {
					t.Fatal(err)
				}
				var modified map[string]interface{}
				if err := json.Unmarshal(body, &modified); err != nil {
					t.Fatal(err)
				}
				if field == "matcher" {
					modified[field] = "custom"
				} else {
					modified["hooks"].([]interface{})[0].(map[string]interface{})[field] = float64(99)
				}
				destination := map[string]interface{}{"Stop": []interface{}{modified}}
				diff := mergeReconcHookMaps(destination, map[string]interface{}{"Stop": []interface{}{canonical}}, MergeOptions{KeepUserEdits: keep})
				if !keep && len(diff.Removed) != 1 || keep && len(diff.Kept) != 1 {
					t.Fatalf("metadata replacement was silent: %+v", diff)
				}
				if keep && !reflect.DeepEqual(destination["Stop"].([]interface{})[0], modified) {
					t.Fatal("requested local settings were not preserved")
				}
			})
		}
	}
}

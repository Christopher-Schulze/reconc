package runtime

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeRuleCapacityHintPreservesDecoding(t *testing.T) {
	for _, body := range []string{
		`[]`, `null`, `{}`, `[null]`, `[{}]`, `[{"id":"a","paths":["x/**"]}]`,
		`[{"id":"a","unknown":true}]`, `[`, `[] {}`, `[{"id":5}]`,
	} {
		for _, validatePresence := range []bool{false, true} {
			for _, hint := range []int{-1, 0, 1, 4096, int(^uint(0) >> 1)} {
				t.Run(fmt.Sprintf("%s/presence=%t/hint=%d", body, validatePresence, hint), func(t *testing.T) {
					want, wantErr := decodeRuntimeRulesTyped([]byte(body), validatePresence, 0)
					got, err := decodeRuntimeRulesTyped([]byte(body), validatePresence, hint)
					if fmt.Sprint(err) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
						t.Fatalf("capacity hint changed decoding: got=%+v/%v want=%+v/%v", got, err, want, wantErr)
					}
					if len(got) > 0 && cap(got) > min(max(hint, len(got)), len(body)/2, 4096) {
						t.Fatalf("unbounded speculative rule capacity: %d", cap(got))
					}
				})
			}
		}
	}
}

func TestRuntimeRulePreallocationAvoidsGrowthAndPreservesOrder(t *testing.T) {
	const count = 4096
	var body strings.Builder
	body.WriteByte('[')
	for index := range count {
		if index != 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":"rule-%d","paths":["generated/%d/**"]}`, index, index)
	}
	body.WriteByte(']')
	rules, err := decodeRuntimeRulesTyped([]byte(body.String()), false, count)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != count || cap(rules) != count {
		t.Fatalf("decoded rule allocation = %d/%d, want %d/%d", len(rules), cap(rules), count, count)
	}
	for index, rule := range rules {
		if rule.ID != fmt.Sprintf("rule-%d", index) || !reflect.DeepEqual(rule.Paths, []string{fmt.Sprintf("generated/%d/**", index)}) {
			t.Fatalf("rule %d lost identity or contents: %+v", index, rule)
		}
	}
}

package actioninspect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

type detectorCorpusCase struct {
	Name           string                    `json:"name"`
	Categories     []action.DetectorCategory `json:"categories"`
	ForbiddenTerms []string                  `json:"forbidden_terms"`
	Text           string                    `json:"text"`
	TextParts      []string                  `json:"text_parts"`
	WantRuleIDs    []string                  `json:"want_rule_ids"`
}

func TestBuiltinDetectorCorpus(t *testing.T) {
	t.Parallel()
	corpus := loadDetectorCorpus(t)
	pack, err := compileBuiltinPack()
	if err != nil {
		t.Fatal(err)
	}
	positiveRules := make(map[string]struct{}, len(pack.rules))
	negativeCategories := make(map[action.DetectorCategory]struct{})
	for _, test := range corpus {
		test := test
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			categories := make(map[action.DetectorCategory]struct{}, len(test.Categories))
			for _, category := range test.Categories {
				if !category.Valid() {
					t.Fatalf("invalid category %q", category)
				}
				categories[category] = struct{}{}
			}
			findings, err := pack.scan(context.Background(), test.Text, categories, test.ForbiddenTerms, action.MaxArgumentBytes)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(findings))
			for index, finding := range findings {
				got[index] = finding.RuleID
			}
			sort.Strings(got)
			sort.Strings(test.WantRuleIDs)
			if !reflect.DeepEqual(got, test.WantRuleIDs) {
				t.Fatalf("rule IDs = %v, want %v", got, test.WantRuleIDs)
			}
			evidence, err := json.Marshal(findings)
			if err != nil {
				t.Fatal(err)
			}
			if test.Text != "" && strings.Contains(string(evidence), test.Text) {
				t.Fatal("finding evidence retained inspected text")
			}
		})
		if len(test.WantRuleIDs) == 0 {
			for _, category := range test.Categories {
				negativeCategories[category] = struct{}{}
			}
		}
		for _, ruleID := range test.WantRuleIDs {
			positiveRules[ruleID] = struct{}{}
		}
	}
	for _, rule := range pack.rules {
		if _, covered := positiveRules[rule.rule.ID]; !covered {
			t.Errorf("detector rule %q has no positive corpus case", rule.rule.ID)
		}
		if _, covered := negativeCategories[rule.rule.Category]; !covered {
			t.Errorf("detector category %q has no negative corpus case", rule.rule.Category)
		}
	}
	if _, covered := positiveRules["forbidden-data-term"]; !covered {
		t.Error("forbidden-data detector has no positive corpus case")
	}
	if _, covered := negativeCategories[action.DetectorForbiddenData]; !covered {
		t.Error("forbidden-data detector has no negative corpus case")
	}
}

func loadDetectorCorpus(t testing.TB) []detectorCorpusCase {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "detector_corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus []detectorCorpusCase
	if err := json.Unmarshal(body, &corpus); err != nil {
		t.Fatal(err)
	}
	for index := range corpus {
		if (corpus[index].Text == "") == (len(corpus[index].TextParts) == 0) {
			t.Fatalf("detector corpus case %q must define exactly one of text or text_parts", corpus[index].Name)
		}
		if len(corpus[index].TextParts) > 0 {
			corpus[index].Text = strings.Join(corpus[index].TextParts, "")
			corpus[index].TextParts = nil
		}
	}
	return corpus
}

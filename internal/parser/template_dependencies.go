package parser

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

const maxTemplateSnapshotBytes = 64 << 20

type templateSnapshot struct {
	entries map[string]*templates.Template
	bytes   int
}

func (s *templateSnapshot) resolve(name string) (*templates.Template, error) {
	name = strings.TrimSpace(name)
	if resolved := s.entries[name]; resolved != nil {
		return resolved, nil
	}
	if len(s.entries) >= maxParserRules {
		return nil, fmt.Errorf("template dependencies exceed %d entries", maxParserRules)
	}
	resolved, err := templates.Resolve(name)
	if err != nil {
		return nil, err
	}
	if resolved.ContentBytes() > maxTemplateSnapshotBytes-s.bytes {
		return nil, fmt.Errorf("template dependencies exceed %d input bytes", maxTemplateSnapshotBytes)
	}
	if s.entries == nil {
		s.entries = make(map[string]*templates.Template)
	}
	s.entries[name] = resolved
	s.bytes += resolved.ContentBytes()
	return resolved, nil
}

// ResolveTemplateDependencies resolves only references in rule-bearing source
// documents, including scopes. Candidate bundles are scanned from their own
// source bytes rather than inheriting a discovered repository's dependencies.
func ResolveTemplateDependencies(bundle *ingest.SourceBundle) ([]templates.Dependency, error) {
	return ResolveTemplateDependenciesWithContext(context.Background(), bundle)
}

// ResolveTemplateDependenciesWithContext scans bounded source documents while
// honoring the caller lifecycle between sources and scoped rule collections.
func ResolveTemplateDependenciesWithContext(ctx context.Context, bundle *ingest.SourceBundle) ([]templates.Dependency, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("template dependency source bundle is nil")
	}
	cache := &templateSnapshot{}
	for _, source := range bundle.Sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch source.Kind {
		case policy.SourceClaudeMD, policy.SourceAgentsMD, policy.SourceStartMD, policy.SourceCustomRuntime:
			continue
		}
		document, err := decodeRuleSourceDocumentBounded(source)
		if err != nil {
			return nil, err
		}
		if err := collectTemplateReferences(document.mapping["rules"], cache); err != nil {
			return nil, fmt.Errorf("template dependency in %s: %w", source.Path, err)
		}
		scopes, _ := document.mapping["scopes"].([]interface{})
		for _, raw := range scopes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			scope, _ := raw.(map[string]interface{})
			if err := collectTemplateReferences(scope["rules"], cache); err != nil {
				return nil, fmt.Errorf("scoped template dependency in %s: %w", source.Path, err)
			}
		}
	}
	if err := validateTemplateCache(cache); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return templateDependencies(cache), nil
}

func collectTemplateReferences(raw interface{}, cache *templateSnapshot) error {
	rules, _ := raw.([]interface{})
	for _, rawRule := range rules {
		rule, _ := rawRule.(map[string]interface{})
		name, _ := rule["template"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		_, err := cache.resolve(name)
		if err != nil {
			return err
		}
	}
	return nil
}

func templateDependencies(cache *templateSnapshot) []templates.Dependency {
	if len(cache.entries) == 0 {
		return nil
	}
	names := make([]string, 0, len(cache.entries))
	for name := range cache.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	dependencies := make([]templates.Dependency, 0, len(names))
	for _, name := range names {
		dependencies = append(dependencies, cache.entries[name].Dependency())
	}
	return dependencies
}

package schema

import (
	"time"

	"github.com/dlclark/regexp2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaRegexpTimeout = 100 * time.Millisecond

type boundedECMAScriptRegexp regexp2.Regexp

func (regexp *boundedECMAScriptRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(regexp).MatchString(value)
	return err == nil && matched
}

func (regexp *boundedECMAScriptRegexp) String() string {
	return (*regexp2.Regexp)(regexp).String()
}

// CompileBoundedECMAScriptRegexp compiles the shared finite-time schema adapter.
func CompileBoundedECMAScriptRegexp(expression string) (jsonschema.Regexp, error) {
	compiled, err := regexp2.Compile(expression, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	compiled.MatchTimeout = schemaRegexpTimeout
	return (*boundedECMAScriptRegexp)(compiled), nil
}

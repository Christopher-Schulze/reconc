package shellcommand

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ExtendsSuccessArguments accepts only static arguments appended to the final
// invocation of an explicitly expected command. Shell control-flow suffixes
// cannot substitute another operation's exit status for the required command.
// Callers remove supported trailing redirects separately.
func ExtendsSuccessArguments(command, expected string) bool {
	if expected == "" || len(command) > maxCommandBytes || !strings.HasPrefix(command, expected+" ") {
		return false
	}
	state := newParserState()
	file, err := state.parse(command, "success-prefix")
	if err != nil {
		return false
	}
	matched := false
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if int(call.End().Offset()) != len(command) {
			return false
		}
		for index, word := range call.Args {
			if int(word.End().Offset()) != len(expected) || index+1 == len(call.Args) {
				continue
			}
			for _, argument := range call.Args[index+1:] {
				if _, static := staticWordParts(argument.Parts); !static {
					return false
				}
			}
			matched = true
			break
		}
		return false
	})
	return matched
}

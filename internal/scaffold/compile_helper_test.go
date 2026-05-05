package scaffold

import "reconc.dev/reconc/internal/compiler"

// compileForTest runs the compiler in tests so we can verify that an
// `init`-scaffolded repo actually compiles. Lives in a _test.go so it
// isn't part of the production binary.
func compileForTest(repo string) (interface{}, error) {
	return compiler.CompileRepoPolicy(repo, "0.1.0-test")
}

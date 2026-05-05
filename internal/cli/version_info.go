package cli

import "runtime"

// goRuntimeVersion captures the Go toolchain version at compile time.
// Pulled into its own file so tests can shadow it without dragging
// the full "runtime" import surface around cli.go.
var goRuntimeVersion = runtime.Version()

package hooks

// MinRuntimeSupportedVersion is the first reconc version whose binary
// implements `reconc hook runtime ...` for installed agent hook
// configs. Older binaries may have generated configs that reference
// the subcommand but cannot execute it.
const MinRuntimeSupportedVersion = "0.4.0"

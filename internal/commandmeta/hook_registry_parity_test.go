package commandmeta_test

import (
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/hooks"
)

func TestHookKindMetadataMatchesRegistry(t *testing.T) {
	hook := lookupCommand(t, "hook")
	for _, test := range []struct {
		subcommand string
		want       []string
	}{
		{subcommand: "generate", want: hooks.SupportedKinds()},
		{subcommand: "install", want: hooks.InstallableKinds()},
		{subcommand: "uninstall", want: hooks.InstallableKinds()},
	} {
		subcommand := lookupSubcommand(t, hook.Subcommands, test.subcommand)
		if len(subcommand.Arguments) != 1 || subcommand.Arguments[0].Name != "kind" ||
			!reflect.DeepEqual(subcommand.Arguments[0].Values, test.want) {
			t.Fatalf("hook %s kind values = %+v, want %v", test.subcommand, subcommand.Arguments, test.want)
		}
	}
	verify := lookupSubcommand(t, hook.Subcommands, "verify")
	if got := lookupFlag(t, verify.Flags, "--host").Values; !reflect.DeepEqual(got, hooks.SupportedKinds()) {
		t.Fatalf("hook verify --host values = %v, want %v", got, hooks.SupportedKinds())
	}

	initCommand := lookupCommand(t, "init")
	if got := lookupFlag(t, initCommand.Flags, "--hook").Values; !reflect.DeepEqual(got, hooks.BootstrapKinds()) {
		t.Fatalf("init --hook values = %v, want %v", got, hooks.BootstrapKinds())
	}
	bootstrap := lookupCommand(t, "bootstrap")
	for _, name := range []string{"plan", "apply"} {
		subcommand := lookupSubcommand(t, bootstrap.Subcommands, name)
		if got := lookupFlag(t, subcommand.Flags, "--hook").Values; !reflect.DeepEqual(got, hooks.BootstrapKinds()) {
			t.Fatalf("bootstrap %s --hook values = %v, want %v", name, got, hooks.BootstrapKinds())
		}
	}
}

func lookupCommand(t *testing.T, name string) commandmeta.Command {
	t.Helper()
	command, ok := commandmeta.Lookup(name)
	if !ok {
		t.Fatalf("command %q is missing", name)
	}
	return command
}

func lookupSubcommand(t *testing.T, subcommands []commandmeta.Subcommand, name string) commandmeta.Subcommand {
	t.Helper()
	for _, subcommand := range subcommands {
		if subcommand.Name == name {
			return subcommand
		}
	}
	t.Fatalf("subcommand %q is missing", name)
	return commandmeta.Subcommand{}
}

func lookupFlag(t *testing.T, flags []commandmeta.Flag, name string) commandmeta.Flag {
	t.Helper()
	for _, flag := range flags {
		if flag.Name == name {
			return flag
		}
	}
	t.Fatalf("flag %q is missing", name)
	return commandmeta.Flag{}
}

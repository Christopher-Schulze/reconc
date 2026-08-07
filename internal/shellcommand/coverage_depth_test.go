package shellcommand

import (
	"reflect"
	"testing"
)

func TestInvocationsResolvesSupportedWrapperOptionContracts(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    [][]string
	}{
		{name: "command query remains the command", command: "command -v git", want: [][]string{{"command", "-v", "git"}}},
		{name: "builtin", command: "builtin printf ready", want: [][]string{{"printf", "ready"}}},
		{name: "exec argv zero argument", command: "exec -a reconc -- git status", want: [][]string{{"git", "status"}}},
		{name: "exec argv zero inline and flags", command: "exec --argv0=reconc -cl git status", want: [][]string{{"git", "status"}}},
		{name: "env separate options", command: "env -i -u OLD --chdir /tmp MODE=1 git status", want: [][]string{{"git", "status"}}},
		{name: "env inline options", command: "env --unset=OLD --chdir=/tmp --debug MODE=1 git status", want: [][]string{{"git", "status"}}},
		{name: "env dynamic assignment", command: `env MODE="$MODE" git status`, want: [][]string{{"git", "status"}}},
		{name: "sudo separate options", command: "sudo -u root -g wheel --preserve-env -- git status", want: [][]string{{"git", "status"}}},
		{name: "sudo inline options", command: "sudo --user=root --group=wheel --non-interactive git status", want: [][]string{{"git", "status"}}},
		{name: "nohup delimiter", command: "nohup -- git status", want: [][]string{{"git", "status"}}},
		{name: "rtk", command: "rtk git status", want: [][]string{{"git", "status"}}},
		{name: "nice negative adjustment", command: "nice -5 git status", want: [][]string{{"git", "status"}}},
		{name: "nice separate adjustment", command: "nice --adjustment 5 git status", want: [][]string{{"git", "status"}}},
		{name: "nice inline adjustment", command: "nice --adjustment=5 git status", want: [][]string{{"git", "status"}}},
		{name: "timeout separate options", command: "timeout -k 1s -s TERM --verbose 5s git status", want: [][]string{{"git", "status"}}},
		{name: "timeout inline options", command: "timeout --kill-after=1s --signal=TERM --foreground -- 5s git status", want: [][]string{{"git", "status"}}},
		{name: "setsid", command: "setsid --ctty --fork --wait -- git status", want: [][]string{{"git", "status"}}},
		{name: "stdbuf separate options", command: "stdbuf -i 0 --output L --error 0 git status", want: [][]string{{"git", "status"}}},
		{name: "stdbuf inline options", command: "stdbuf -i0 --output=L --error=0 -- git status", want: [][]string{{"git", "status"}}},
		{name: "time separate options", command: "/usr/bin/time -o timing.txt -f %e --append -- git status", want: [][]string{{"git", "status"}}},
		{name: "time inline options", command: "/usr/bin/time --output=timing.txt --format=%e --portability git status", want: [][]string{{"git", "status"}}},
		{name: "chroot separate options", command: "chroot --userspec root:wheel --groups wheel /srv git status", want: [][]string{{"git", "status"}}},
		{name: "chroot inline options", command: "chroot --userspec=root:wheel --groups=wheel --skip-chdir /srv git status", want: [][]string{{"git", "status"}}},
		{name: "leading exact redirections", command: "2> errors.log < input.txt git status", want: [][]string{{"git", "status"}}},
		{name: "leading inline redirections", command: "2>errors.log <input.txt git status", want: [][]string{{"git", "status"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, reason := InvocationsWithReason(test.command, 16)
			if reason != IncompleteNone {
				t.Fatalf("InvocationsWithReason(%q) reason = %q, want complete", test.command, reason)
			}
			got := invocationWords(invocations)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("InvocationsWithReason(%q) words = %#v, want %#v", test.command, got, test.want)
			}
		})
	}
}

func TestInvocationsRejectsUnsupportedOrIncompleteWrapperOptions(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "exec missing argv zero", command: "exec -a"},
		{name: "exec unknown option", command: "exec --unknown git status"},
		{name: "env split string", command: `env -S "git status"`},
		{name: "env missing unset value", command: "env --unset"},
		{name: "env unknown option", command: "env --unknown git status"},
		{name: "sudo missing user", command: "sudo --user"},
		{name: "sudo unknown option", command: "sudo --unknown git status"},
		{name: "nohup unknown option", command: "nohup --unknown git status"},
		{name: "nice missing adjustment", command: "nice --adjustment"},
		{name: "nice malformed negative adjustment", command: "nice -x git status"},
		{name: "timeout missing duration", command: "timeout --verbose"},
		{name: "timeout unknown option", command: "timeout --unknown 5s git status"},
		{name: "setsid unknown option", command: "setsid --unknown git status"},
		{name: "stdbuf missing output mode", command: "stdbuf --output"},
		{name: "stdbuf unknown option", command: "stdbuf --unknown git status"},
		{name: "time missing format", command: "/usr/bin/time --format"},
		{name: "time unknown option", command: "/usr/bin/time --unknown git status"},
		{name: "chroot missing root", command: "chroot --userspec=root"},
		{name: "chroot root without command", command: "chroot /srv"},
		{name: "chroot unknown option", command: "chroot --unknown /srv git status"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, reason := InvocationsWithReason(test.command, 16)
			if reason != IncompleteDynamicCommand {
				t.Fatalf("InvocationsWithReason(%q) reason = %q, want %q", test.command, reason, IncompleteDynamicCommand)
			}
		})
	}
}

func TestInvocationsDiscoversEverySupportedLauncherShape(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    [][]string
	}{
		{
			name:    "find exec variants",
			command: `find . -execdir git status \; -ok git diff \; -okdir git log -1 +`,
			want: [][]string{
				{"find", ".", "-execdir", "git", "status", ";", "-ok", "git", "diff", ";", "-okdir", "git", "log", "-1", "+"},
				{"git", "status"},
				{"git", "diff"},
				{"git", "log", "-1"},
			},
		},
		{
			name:    "xargs separate options",
			command: "xargs -a args.txt -E stop -I token -L 2 -n 3 -P 4 -s 128 -d , -- git status",
			want:    [][]string{{"xargs", "-a", "args.txt", "-E", "stop", "-I", "token", "-L", "2", "-n", "3", "-P", "4", "-s", "128", "-d", ",", "--", "git", "status"}, {"git", "status"}},
		},
		{
			name:    "xargs inline options",
			command: "xargs --arg-file=args.txt --eof=stop --replace=token --max-lines=2 --max-args=3 --max-procs=4 --max-chars=128 --delimiter=, --null --no-run-if-empty git status",
			want:    [][]string{{"xargs", "--arg-file=args.txt", "--eof=stop", "--replace=token", "--max-lines=2", "--max-args=3", "--max-procs=4", "--max-chars=128", "--delimiter=,", "--null", "--no-run-if-empty", "git", "status"}, {"git", "status"}},
		},
		{
			name:    "flock separate options",
			command: "flock -w 2 -E 75 --shared --close /tmp/reconc.lock git status",
			want:    [][]string{{"flock", "-w", "2", "-E", "75", "--shared", "--close", "/tmp/reconc.lock", "git", "status"}, {"git", "status"}},
		},
		{
			name:    "flock inline options and shell command",
			command: `flock --wait=2 --conflict-exit-code=75 /tmp/reconc.lock --command "git status"`,
			want:    [][]string{{"flock", "--wait=2", "--conflict-exit-code=75", "/tmp/reconc.lock", "--command", "git status"}, {"sh", "-c", "git status"}, {"git", "status"}},
		},
		{
			name:    "watch separate options",
			command: "watch -n 2 -q 3 --shotsdir shots --color --exec -- git status",
			want:    [][]string{{"watch", "-n", "2", "-q", "3", "--shotsdir", "shots", "--color", "--exec", "--", "git", "status"}, {"git", "status"}},
		},
		{
			name:    "watch inline options",
			command: "watch --interval=2 --equexit=3 --shotsdir=shots --no-title git status",
			want:    [][]string{{"watch", "--interval=2", "--equexit=3", "--shotsdir=shots", "--no-title", "git", "status"}, {"git", "status"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, reason := InvocationsWithReason(test.command, 16)
			if reason != IncompleteNone {
				t.Fatalf("InvocationsWithReason(%q) reason = %q, want complete", test.command, reason)
			}
			got := invocationWords(invocations)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("InvocationsWithReason(%q) words = %#v, want %#v", test.command, got, test.want)
			}
		})
	}
}

func TestInvocationsRejectsIncompleteLauncherShapes(t *testing.T) {
	for _, command := range []string{
		`find . -exec \;`,
		`find . -exec "$COMMAND" \;`,
		"xargs -n",
		"xargs --unknown git status",
		`xargs "$COMMAND"`,
		"flock -w",
		"flock --unknown /tmp/reconc.lock git status",
		"flock /tmp/reconc.lock --command",
		`flock /tmp/reconc.lock "$COMMAND"`,
		"watch --interval",
		"watch --unknown git status",
		`watch "$COMMAND"`,
	} {
		t.Run(command, func(t *testing.T) {
			_, reason := InvocationsWithReason(command, 16)
			if reason != IncompleteDynamicCommand {
				t.Fatalf("InvocationsWithReason(%q) reason = %q, want %q", command, reason, IncompleteDynamicCommand)
			}
		})
	}
}

func TestMatchRejectsInvalidExpectedCommandsAndDynamicSuffixes(t *testing.T) {
	static := Invocation{Words: []string{"git", "status"}, DynamicWords: []bool{false, false}}
	for _, expected := range []string{"", `git "status`, "$COMMAND status", "git status && git diff"} {
		matched, uncertain := Match(static, expected, false)
		if matched || !uncertain {
			t.Fatalf("Match(static, %q, false) = (%t, %t), want (false, true)", expected, matched, uncertain)
		}
	}

	dynamicSuffix := Invocation{
		Words:        []string{"git", "status", `"$FORMAT"`},
		DynamicWords: []bool{false, false, true},
	}
	matched, uncertain := Match(dynamicSuffix, "git status", false)
	if matched || !uncertain {
		t.Fatalf("dynamic suffix exact match = (%t, %t), want (false, true)", matched, uncertain)
	}
}

func TestStripLineContinuationsPreservesSingleQuotedAndEscapedText(t *testing.T) {
	input := "git \\\r\nstatus 'literal \\\ntext' \"double \\\nquoted\" escaped\\\\\n"
	want := "git status 'literal \\\ntext' \"double quoted\" escaped\\\\\n"
	if got := StripLineContinuations(input); got != want {
		t.Fatalf("StripLineContinuations() = %q, want %q", got, want)
	}
}

func invocationWords(invocations []Invocation) [][]string {
	words := make([][]string, 0, len(invocations))
	for _, invocation := range invocations {
		words = append(words, invocation.Words)
	}
	return words
}

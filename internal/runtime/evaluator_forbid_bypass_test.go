package runtime

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
)

// TestForbidCommandResistsShellEvasion drives the deny matcher with the
// evasions an agent can reach for. Each one runs the forbidden program, so each
// one must be reported. Wrapper, grouping, and substitution shapes are covered
// by the shell analyser; this test pins the two that live in word resolution:
// the alias-bypass backslash and a case change on a case-insensitive host.
func TestForbidCommandResistsShellEvasion(t *testing.T) {
	forbidden := []string{"rm -rf /"}
	evasions := []string{
		`rm -rf /`,
		`rm  -rf  /`,
		`\rm -rf /`,
		`\r\m -rf /`,
		`r''m -rf /`,
		`"rm" -rf /`,
		`RM -rf /`,
		`/bin/rm -rf /`,
		`command rm -rf /`,
		`env rm -rf /`,
		`sudo rm -rf /`,
		`exec rm -rf /`,
		`sh -c 'rm -rf /'`,
		`eval 'rm -rf /'`,
		`true && rm -rf /`,
		`echo x; rm -rf /`,
		`(rm -rf /)`,
		`{ rm -rf /; }`,
		`$(rm -rf /)`,
		"`rm -rf /`",
		`if true; then rm -rf /; fi`,
		`while true; do rm -rf /; done`,
		`timeout 5 rm -rf /`,
		`rm -rf / &`,
		`rm -rf / | cat`,
		`rm $(echo -rf) /`,
		`$'\x72\x6d' -rf /`,
	}
	for _, command := range evasions {
		if len(matchingForbiddenCommands([]string{command}, forbidden, ".", policy.CommandMatchExact)) == 0 {
			t.Fatalf("forbidden command escaped the gate: %q", command)
		}
	}
}

// TestForbidCommandStillDistinguishesUnrelatedCommands keeps the hardening from
// degenerating into "block everything": a different program, and a different
// argument, must stay allowed.
func TestForbidCommandStillDistinguishesUnrelatedCommands(t *testing.T) {
	forbidden := []string{"rm -rf /"}
	allowed := []string{
		`rm -rf build`,
		`rmdir /`,
		`git rm -rf /`,
		`echo rm -rf /`,
		`trm -rf /`,
	}
	for _, command := range allowed {
		if hits := matchingForbiddenCommands([]string{command}, forbidden, ".", policy.CommandMatchExact); len(hits) != 0 {
			t.Fatalf("unrelated command was blocked: %q -> %v", command, hits)
		}
	}
}

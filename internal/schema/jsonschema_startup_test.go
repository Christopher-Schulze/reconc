package schema

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The in-repository jsonschema copy compiles its draft metaschemas on first
// use, so a process that never validates a schema, such as every hook, does
// not pay for them at startup.
func TestJSONSchemaInitializationDefersDraftMetaschemas(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^$")
	command.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("trace package initialization: %v\n%s", err, output)
	}
	allocated := regexp.MustCompile(` ([0-9]+) bytes,`)
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "init github.com/santhosh-tekuri/jsonschema/v6 ") {
			continue
		}
		match := allocated.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("unexpected initialization trace %q", line)
		}
		bytes, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatal(err)
		}
		if bytes > 64<<10 {
			t.Fatalf("jsonschema initialization allocated %d bytes; its draft metaschemas must compile on first use", bytes)
		}
		return
	}
	t.Fatalf("no jsonschema initialization trace in:\n%s", output)
}

package mcpgateway

import (
	"reflect"
	"testing"
)

// The operator context of every request is built once per gateway and equals
// a fresh build from the unchanged configuration.
func TestOperatorRequestContextIsBuiltOnce(t *testing.T) {
	gateway := &Gateway{config: Config{
		Principal: "operator", ServerLabel: "local", Role: "reviewer",
		CredentialLabels: []string{"b", "a"}, RunID: "run-1",
	}}
	first, err := gateway.operatorRequestContext()
	if err != nil {
		t.Fatal(err)
	}
	second, err := gateway.operatorRequestContext()
	if err != nil || len(first) == 0 || &first[0] != &second[0] {
		t.Fatalf("second request context = %v, %v; want the first build", second, err)
	}
	fresh, err := gateway.requestContext()
	if err != nil || !reflect.DeepEqual(first, fresh) {
		t.Fatalf("cached context %v differs from a fresh build %v (%v)", first, fresh, err)
	}
}

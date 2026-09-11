package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEmbeddedPolicyConfigSchemasMatchContractBytes(t *testing.T) {
	want := map[string]string{
		"2": "e5856413af32bea5f8b0fc108b3e5dcdfc84faf9d5e7e09bada79e7bdb5cad03",
		"4": "b6f9f2df0b1a88cdbee23953d8c4afc026ecc15e4dc84f1d4259d92afd5cb3d1",
	}
	for version, digest := range want {
		body, err := PolicyConfig(version)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != digest {
			t.Fatalf("policy-config v%s digest = %s, want %s", version, got, digest)
		}
	}
	if _, err := PolicyConfig("3"); err == nil {
		t.Fatal("unembedded policy-config version was accepted")
	}
}

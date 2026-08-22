package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEmbeddedPolicyConfigSchemasMatchPublishedBytes(t *testing.T) {
	want := map[string]string{
		"2": "e5856413af32bea5f8b0fc108b3e5dcdfc84faf9d5e7e09bada79e7bdb5cad03",
		"4": "fe87ab8b32ece847df6974cbacdcac3ca9aafac85c04d028928ea4f5e91b4b0f",
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

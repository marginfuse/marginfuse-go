package marginfuse_test

// The exported contract version has to be the one this build was actually
// verified against, or it is a claim rather than a fact.

import (
	"encoding/json"
	"os"
	"testing"

	marginfuse "github.com/marginfuse/marginfuse-go"
)

func TestContractVersionMatchesThePinnedContract(t *testing.T) {
	raw, err := os.ReadFile("contract/conformance/behavior-scenarios.json")
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var pinned struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &pinned); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	if marginfuse.ContractVersion != pinned.Version {
		t.Errorf("ContractVersion = %d, pinned contract is %d",
			marginfuse.ContractVersion, pinned.Version)
	}
}

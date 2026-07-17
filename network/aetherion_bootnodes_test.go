package network

import "testing"

func TestMergeAetherionBootnodes(t *testing.T) {
	// genesis-supplied entries keep priority and are not duplicated
	got := mergeAetherionBootnodes([]string{aetherionBootnodes[2], "/ip4/1.2.3.4/tcp/1478/p2p/16UiuCustom"})
	if got[0] != aetherionBootnodes[2] || got[1] != "/ip4/1.2.3.4/tcp/1478/p2p/16UiuCustom" {
		t.Fatalf("configured order not preserved: %v", got[:2])
	}
	if len(got) != 5 {
		t.Fatalf("want 5 (4 builtin + 1 custom, deduped), got %d: %v", len(got), got)
	}
	// an empty genesis list still yields the full network
	if len(mergeAetherionBootnodes(nil)) != 4 {
		t.Fatal("builtin bootnodes lost when genesis has none")
	}
}

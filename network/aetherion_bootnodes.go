package network

// The Aetherion Network's own bootnodes, compiled in.
//
// A node reads its bootnode list from genesis.json and nowhere else, which makes joining
// only as reliable as whatever copy of that file the operator happens to hold. An old
// copy listing one or two nodes leaves a newcomer dialing a short list, and if those
// happen to be down it cannot join at all, despite the network being perfectly healthy.
//
// These are merged with whatever genesis provides rather than replacing it: genesis can
// still add bootnodes, it just cannot silently take these away. Duplicates and this
// node's own address are dropped, so a bootnode running with this list does not try to
// dial itself.
//
// This is a list of addresses to knock on, not a list of who is trusted. A bootnode only
// introduces peers; it cannot vouch for a block. Being here grants no authority over the
// chain, which is why hardcoding it costs nothing.
var aetherionBootnodes = []string{
	"/ip4/89.167.111.230/tcp/1478/p2p/16Uiu2HAmLoUGNMxjpdZfPuq6NGhSCiZivGQw9GEh8BaMXA3vUwW4",
	"/ip4/46.224.18.225/tcp/1478/p2p/16Uiu2HAkzpcTyxTZG92G3P53xatp8BAXucakaTPmQHL6ErHF992z",
	"/ip4/95.216.190.151/tcp/1478/p2p/16Uiu2HAmFVKkxvYMicKoTCD466s1tvpJ3vzK5saVHWWULjoxVak5",
	"/ip4/62.238.20.59/tcp/1478/p2p/16Uiu2HAm1zbzE9tx4xN6ig6tSSAoScKn4t7avupQjcnKs34FkdDo",
}

// mergeAetherionBootnodes returns the configured bootnodes plus the built-in ones, in
// that order and without duplicates. Configuration wins on ordering so an operator who
// runs their own bootnode still dials it first.
func mergeAetherionBootnodes(configured []string) []string {
	merged := make([]string, 0, len(configured)+len(aetherionBootnodes))
	seen := make(map[string]struct{}, len(configured)+len(aetherionBootnodes))

	for _, addr := range append(append([]string{}, configured...), aetherionBootnodes...) {
		if addr == "" {
			continue
		}

		if _, dup := seen[addr]; dup {
			continue
		}

		seen[addr] = struct{}{}

		merged = append(merged, addr)
	}

	return merged
}

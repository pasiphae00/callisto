package assets

import (
	"sort"
	"strings"
)

// Sort orders assets for display deterministically: the native asset always first,
// then tokens alphabetically by symbol (case-insensitive), with the contract
// address as a stable tie-breaker. Discovered tokens arrive in map/scan order, so
// this keeps the list from reshuffling between reloads.
func Sort(list []Asset) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.Kind == Native) != (b.Kind == Native) {
			return a.Kind == Native // native sorts before any token
		}
		ai, bi := strings.ToLower(a.Symbol), strings.ToLower(b.Symbol)
		if ai != bi {
			return ai < bi
		}
		return a.Contract.Hex() < b.Contract.Hex()
	})
}

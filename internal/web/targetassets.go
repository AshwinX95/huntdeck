package web

import (
	"strings"

	"github.com/mdpsec/huntdeck/internal/scope"
)

// Targets also lists assets that aren't addresses (apps, executables,
// hardware, …), from their scope entries (store/assets.go): ?kind= takes
// each asset type, and app for Android and iOS apps together, as the hunt
// board's Apps chip.

// assetKind is a ?kind= listing scope entries of some asset types.
type assetKind struct {
	Key, Label string
	Types      []string
}

var assetKinds = []assetKind{
	{scope.KindApp, "Apps", []string{scope.AssetAndroid, scope.AssetIOS}},
	{scope.AssetAndroid, "Android apps", []string{scope.AssetAndroid}},
	{scope.AssetIOS, "iOS apps", []string{scope.AssetIOS}},
	{scope.AssetWindows, "Windows apps", []string{scope.AssetWindows}},
	{scope.AssetExecutable, "Executables", []string{scope.AssetExecutable}},
	{scope.AssetSourceCode, "Source code", []string{scope.AssetSourceCode}},
	{scope.AssetHardware, "Hardware", []string{scope.AssetHardware}},
	{scope.AssetSmartContract, "Smart contracts", []string{scope.AssetSmartContract}},
	{scope.AssetAIModel, "AI models", []string{scope.AssetAIModel}},
	{scope.AssetOther, "Other assets", []string{scope.AssetOther}},
}

// assetKindOf reads a ?kind= naming assets: a key, its plural, or with
// dashes ("source-code"); nil for anything else.
func assetKindOf(v string) *assetKind {
	v = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(v)), "-", "_")
	for _, try := range []string{v, strings.TrimSuffix(v, "s")} {
		for i, k := range assetKinds {
			if try == k.Key {
				return &assetKinds[i]
			}
		}
	}
	return nil
}

// assetGroup is the rail's other assets: the kinds with any (as filtered),
// and the one chosen.
func (d targetsData) assetGroup() railGroup {
	l, g := d.list(), railGroup{Title: "Other assets"}
	for _, k := range assetKinds {
		n := 0
		for _, t := range k.Types {
			n += d.Facets.Assets[t]
		}
		if on := d.KindParam == k.Key; on || (n > 0 && d.Counted) {
			o := l.choice(k.Label, "", "kind", k.Key, on, n, d.Counted)
			if k.Key == scope.KindApp {
				o.Title = "Android and iOS apps together"
			}
			g.Options = append(g.Options, o)
		}
	}
	return g
}

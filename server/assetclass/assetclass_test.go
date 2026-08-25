package assetclass

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

var update = flag.Bool("update", false, "rewrite the golden tree in testdata")

// Every value in the generated enum appears exactly once in the parent map, so
// a value added to the proto without a place in the tree fails here rather than
// silently answering no to every question asked of it.
func TestParentMap_CoversEveryEnumValue(t *testing.T) {
	for i, name := range typev1.AssetClass_name {
		v := typev1.AssetClass(i)
		if v == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
			if _, ok := parent[v]; ok {
				t.Errorf("parent map names %s, which is not a tree node", name)
			}
			continue
		}
		if _, ok := parent[v]; !ok {
			t.Errorf("enum value %s missing from the parent map", name)
		}
	}
	for v := range parent {
		if _, ok := typev1.AssetClass_name[int32(v)]; !ok || v == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
			t.Errorf("parent map names %v, which is not an enum value", v)
		}
	}
}

// The golden tree is what client/lib/asset-class.ts is checked against, so the
// Go and TypeScript spellings of the hierarchy cannot drift apart.
// Refresh with: go test ./server/assetclass -update
func TestGoldenTree(t *testing.T) {
	tree := map[string]string{}
	for v, p := range parent {
		name := typev1.AssetClass_name[int32(v)]
		if p == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
			tree[name] = ""
			continue
		}
		tree[name] = typev1.AssetClass_name[int32(p)]
	}
	// encoding/json sorts map keys, so the golden bytes are stable.
	got, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		t.Fatalf("marshal tree: %v", err)
	}
	got = append(got, '\n')
	const path = "testdata/tree.json"
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(want) != string(got) {
		t.Errorf("%s is stale (refresh with -update):\n want %s\n got  %s", path, want, got)
	}
}

// Exactly one node has no parent. A second root would make two values
// incomparable while both looked like ordinary members of the tree.
func TestSingleRoot(t *testing.T) {
	var roots []string
	for v, p := range parent {
		if p == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
			roots = append(roots, typev1.AssetClass_name[int32(v)])
		}
	}
	if len(roots) != 1 || roots[0] != "UNKNOWN" {
		t.Errorf("roots = %v, want [UNKNOWN]", roots)
	}
}

func TestUnder(t *testing.T) {
	tests := []struct {
		name string
		c, x typev1.AssetClass
		want bool
	}{
		{"leaf under its branch", typev1.AssetClass_ETF, typev1.AssetClass_EQUITY, true},
		{"leaf under the root", typev1.AssetClass_ETF, typev1.AssetClass_UNKNOWN, true},
		{"leaf under a grandparent", typev1.AssetClass_OPTION, typev1.AssetClass_SECURITY, true},
		{"node under itself", typev1.AssetClass_EQUITY, typev1.AssetClass_EQUITY, true},
		{"branch not under its leaf", typev1.AssetClass_EQUITY, typev1.AssetClass_ETF, false},
		{"siblings", typev1.AssetClass_STOCK, typev1.AssetClass_ETF, false},
		{"across branches", typev1.AssetClass_CASH, typev1.AssetClass_SECURITY, false},
		{"unspecified is under nothing", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, typev1.AssetClass_UNKNOWN, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := under(tt.c, tt.x); got != tt.want {
				t.Errorf("under(%v, %v) = %v, want %v", tt.c, tt.x, got, tt.want)
			}
		})
	}
}

func TestBelow(t *testing.T) {
	tests := []struct {
		name string
		c, x typev1.AssetClass
		want bool
	}{
		{"a leaf is below its branch", typev1.AssetClass_OPTION, typev1.AssetClass_DERIVATIVE, true},
		{"a node is not below itself", typev1.AssetClass_DERIVATIVE, typev1.AssetClass_DERIVATIVE, false},
		{"a leaf is below a grandparent", typev1.AssetClass_ETF, typev1.AssetClass_SECURITY, true},
		{"siblings", typev1.AssetClass_OPTION, typev1.AssetClass_FUTURE, false},
		{"a branch is not below its leaf", typev1.AssetClass_DERIVATIVE, typev1.AssetClass_OPTION, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Below(tt.c, tt.x); got != tt.want {
				t.Errorf("Below(%v, %v) = %v, want %v", tt.c, tt.x, got, tt.want)
			}
		})
	}
}

func TestMustBe(t *testing.T) {
	tests := []struct {
		name string
		c    typev1.AssetClass
		xs   []typev1.AssetClass
		want bool
	}{
		{"exact", typev1.AssetClass_STOCK, []typev1.AssetClass{typev1.AssetClass_STOCK}, true},
		{"leaf satisfies its branch", typev1.AssetClass_OPTION, []typev1.AssetClass{typev1.AssetClass_DERIVATIVE}, true},
		{"a coarse value does not satisfy a specific one", typev1.AssetClass_EQUITY, []typev1.AssetClass{typev1.AssetClass_ETF}, false},
		{"a security of unstated class is not a derivative", typev1.AssetClass_SECURITY, []typev1.AssetClass{typev1.AssetClass_DERIVATIVE}, false},
		{"siblings do not corroborate", typev1.AssetClass_STOCK, []typev1.AssetClass{typev1.AssetClass_ETF}, false},
		{"any of several", typev1.AssetClass_FUTURE, []typev1.AssetClass{typev1.AssetClass_CASH, typev1.AssetClass_DERIVATIVE}, true},
		{"none of several", typev1.AssetClass_CASH, []typev1.AssetClass{typev1.AssetClass_EQUITY, typev1.AssetClass_DERIVATIVE}, false},
		{"unspecified is nothing", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, []typev1.AssetClass{typev1.AssetClass_UNKNOWN}, false},
		{"no candidates", typev1.AssetClass_STOCK, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MustBe(tt.c, tt.xs...); got != tt.want {
				t.Errorf("MustBe(%v, %v) = %v, want %v", tt.c, tt.xs, got, tt.want)
			}
		})
	}
}

func TestMayBe(t *testing.T) {
	tests := []struct {
		name string
		c    typev1.AssetClass
		xs   []typev1.AssetClass
		want bool
	}{
		{"exact", typev1.AssetClass_STOCK, []typev1.AssetClass{typev1.AssetClass_STOCK}, true},
		{"a coarse value may be a specific one", typev1.AssetClass_EQUITY, []typev1.AssetClass{typev1.AssetClass_ETF}, true},
		{"and the other way round", typev1.AssetClass_ETF, []typev1.AssetClass{typev1.AssetClass_EQUITY}, true},
		{"siblings are disjoint", typev1.AssetClass_STOCK, []typev1.AssetClass{typev1.AssetClass_ETF}, false},
		{"a stated security may be a stock", typev1.AssetClass_SECURITY, []typev1.AssetClass{typev1.AssetClass_STOCK}, true},
		{"a stated security is not money", typev1.AssetClass_SECURITY, []typev1.AssetClass{typev1.AssetClass_CASH}, false},
		{"the root admits everything", typev1.AssetClass_UNKNOWN, []typev1.AssetClass{typev1.AssetClass_CASH}, true},
		{"any of several", typev1.AssetClass_EQUITY, []typev1.AssetClass{typev1.AssetClass_CASH, typev1.AssetClass_STOCK}, true},
		{"unspecified is nothing", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, []typev1.AssetClass{typev1.AssetClass_UNKNOWN}, false},
		{"no candidates", typev1.AssetClass_STOCK, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MayBe(tt.c, tt.xs...); got != tt.want {
				t.Errorf("MayBe(%v, %v) = %v, want %v", tt.c, tt.xs, got, tt.want)
			}
		})
	}
}

func TestContradicts(t *testing.T) {
	tests := []struct {
		name             string
		stated, resolved typev1.AssetClass
		want             bool
	}{
		{"siblings", typev1.AssetClass_STOCK, typev1.AssetClass_ETF, true},
		{"a coarse claim admits its leaf", typev1.AssetClass_EQUITY, typev1.AssetClass_ETF, false},
		{"and the leaf admits the coarse claim", typev1.AssetClass_ETF, typev1.AssetClass_EQUITY, false},
		{"money against a security", typev1.AssetClass_CASH, typev1.AssetClass_STOCK, true},
		{"a security of unstated class against money", typev1.AssetClass_SECURITY, typev1.AssetClass_CASH, true},
		{"the root rules nothing out", typev1.AssetClass_UNKNOWN, typev1.AssetClass_CASH, false},
		{"silence on the left", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, typev1.AssetClass_CASH, false},
		{"silence on the right", typev1.AssetClass_CASH, typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Contradicts(tt.stated, tt.resolved); got != tt.want {
				t.Errorf("Contradicts(%v, %v) = %v, want %v", tt.stated, tt.resolved, got, tt.want)
			}
		})
	}
}

func TestCorroborates(t *testing.T) {
	tests := []struct {
		name             string
		stated, resolved typev1.AssetClass
		want             bool
	}{
		{"exact", typev1.AssetClass_STOCK, typev1.AssetClass_STOCK, true},
		{"an answer inside the claim", typev1.AssetClass_EQUITY, typev1.AssetClass_ETF, true},
		{"an answer coarser than the claim never reached the question",
			typev1.AssetClass_STOCK, typev1.AssetClass_EQUITY, false},
		{"siblings", typev1.AssetClass_STOCK, typev1.AssetClass_ETF, false},
		{"a claim of the root rules nothing out", typev1.AssetClass_UNKNOWN, typev1.AssetClass_STOCK, false},
		{"silence claims nothing", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, typev1.AssetClass_STOCK, false},
		{"silence confirms nothing", typev1.AssetClass_STOCK, typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Corroborates(tt.stated, tt.resolved); got != tt.want {
				t.Errorf("Corroborates(%v, %v) = %v, want %v", tt.stated, tt.resolved, got, tt.want)
			}
		})
	}
}

// Corroboration is the stronger claim: whatever it holds for, a contradiction
// cannot also hold for. The reverse does not follow, and that gap is the point
// -- an answer that says almost nothing contradicts almost nothing.
func TestCorroboratesImpliesNoContradiction(t *testing.T) {
	for i := range typev1.AssetClass_name {
		for j := range typev1.AssetClass_name {
			a, b := typev1.AssetClass(i), typev1.AssetClass(j)
			if Corroborates(a, b) && Contradicts(a, b) {
				t.Errorf("%v/%v both corroborates and contradicts", a, b)
			}
		}
	}
}

// MayBe is symmetric, which is what lets a contradiction test read as !MayBe.
func TestMayBe_Symmetric(t *testing.T) {
	for i := range typev1.AssetClass_name {
		for j := range typev1.AssetClass_name {
			a, b := typev1.AssetClass(i), typev1.AssetClass(j)
			if MayBe(a, b) != MayBe(b, a) {
				t.Errorf("MayBe(%v, %v) = %v but MayBe(%v, %v) = %v", a, b, MayBe(a, b), b, a, MayBe(b, a))
			}
		}
	}
}

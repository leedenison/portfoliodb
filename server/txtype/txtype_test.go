package txtype

import (
	"encoding/json"
	"flag"
	"os"
	"sort"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

var update = flag.Bool("update", false, "rewrite the golden tree in testdata")

// Every value in the generated enum appears exactly once in the parent map, so
// a value added to the proto without a place in the tree fails here rather than
// silently resolving to nothing.
func TestParentMap_CoversEveryEnumValue(t *testing.T) {
	for i, name := range typev1.TxType_name {
		v := typev1.TxType(i)
		if v == typev1.TxType_TX_TYPE_UNSPECIFIED {
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
		if _, ok := typev1.TxType_name[int32(v)]; !ok || v == typev1.TxType_TX_TYPE_UNSPECIFIED {
			t.Errorf("parent map names %v, which is not an enum value", v)
		}
	}
}

// The golden tree is what client/lib/tx-type.ts is checked against, so the Go
// and TypeScript spellings of the hierarchy cannot drift apart.
// Refresh with: go test ./server/txtype -update
func TestGoldenTree(t *testing.T) {
	tree := map[string]string{}
	for v, p := range parent {
		name := typev1.TxType_name[int32(v)]
		if p == typev1.TxType_TX_TYPE_UNSPECIFIED {
			tree[name] = ""
			continue
		}
		tree[name] = typev1.TxType_name[int32(p)]
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

func TestUnder(t *testing.T) {
	tests := []struct {
		name string
		t, x typev1.TxType
		want bool
	}{
		{"leaf under its branch", typev1.TxType_DIVIDEND, typev1.TxType_INCOME, true},
		{"leaf under the root", typev1.TxType_DIVIDEND, typev1.TxType_AMBIGUOUS, true},
		{"node under itself", typev1.TxType_TRANSFER, typev1.TxType_TRANSFER, true},
		{"branch not under its leaf", typev1.TxType_INCOME, typev1.TxType_DIVIDEND, false},
		{"cross branch", typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := under(tc.t, tc.x); got != tc.want {
				t.Fatalf("under(%v, %v) = %v, want %v", tc.t, tc.x, got, tc.want)
			}
		})
	}
}

func TestMustBe(t *testing.T) {
	tests := []struct {
		name string
		set  []typev1.TxType
		xs   []typev1.TxType
		want bool
	}{
		{"singleton leaf", []typev1.TxType{typev1.TxType_DIVIDEND}, []typev1.TxType{typev1.TxType_INCOME}, true},
		{"every member must hold", []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, []typev1.TxType{typev1.TxType_TRANSFER}, false},
		{"set within one branch", []typev1.TxType{typev1.TxType_DIVIDEND, typev1.TxType_INTEREST}, []typev1.TxType{typev1.TxType_INCOME}, true},
		{"internal node is not its leaf", []typev1.TxType{typev1.TxType_INCOME}, []typev1.TxType{typev1.TxType_DIVIDEND}, false},
		{"several targets", []typev1.TxType{typev1.TxType_DIVIDEND, typev1.TxType_TRANSACTION_COST}, []typev1.TxType{typev1.TxType_INCOME, typev1.TxType_EXPENSE}, true},
		{"empty set holds nothing", nil, []typev1.TxType{typev1.TxType_INCOME}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MustBe(tc.set, tc.xs...); got != tc.want {
				t.Fatalf("MustBe(%v, %v) = %v, want %v", tc.set, tc.xs, got, tc.want)
			}
		})
	}
}

func TestMayBe(t *testing.T) {
	tests := []struct {
		name string
		set  []typev1.TxType
		xs   []typev1.TxType
		want bool
	}{
		{"one candidate suffices", []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, []typev1.TxType{typev1.TxType_TRANSFER}, true},
		{"ancestor may be its leaf", []typev1.TxType{typev1.TxType_INCOME}, []typev1.TxType{typev1.TxType_DIVIDEND}, true},
		{"cross branch is not", []typev1.TxType{typev1.TxType_TRADE_ASSET}, []typev1.TxType{typev1.TxType_TRANSFER}, false},
		{"leaf may be itself", []typev1.TxType{typev1.TxType_DIVIDEND}, []typev1.TxType{typev1.TxType_DIVIDEND}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MayBe(tc.set, tc.xs...); got != tc.want {
				t.Fatalf("MayBe(%v, %v) = %v, want %v", tc.set, tc.xs, got, tc.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		set  []typev1.TxType
		want typev1.TxType
	}{
		{"singleton resolves to itself", []typev1.TxType{typev1.TxType_TRADE_ASSET}, typev1.TxType_TRADE_ASSET},
		{"siblings resolve to the branch", []typev1.TxType{typev1.TxType_DIVIDEND, typev1.TxType_INTEREST}, typev1.TxType_INCOME},
		{"cross branch resolves to the root", []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, typev1.TxType_AMBIGUOUS},
		{"internal node resolves to itself", []typev1.TxType{typev1.TxType_EXPENSE}, typev1.TxType_EXPENSE},
		{"empty set resolves to unspecified", nil, typev1.TxType_TX_TYPE_UNSPECIFIED},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.set); got != tc.want {
				t.Fatalf("Resolve(%v) = %v, want %v", tc.set, got, tc.want)
			}
		})
	}
}

func TestAntichain(t *testing.T) {
	tests := []struct {
		name string
		set  []typev1.TxType
		want bool
	}{
		{"cross branch", []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, true},
		{"ancestor and descendant", []typev1.TxType{typev1.TxType_TRANSFER, typev1.TxType_TRANSFER_INTERNAL}, false},
		{"duplicate", []typev1.TxType{typev1.TxType_DIVIDEND, typev1.TxType_DIVIDEND}, false},
		{"singleton", []typev1.TxType{typev1.TxType_INCOME}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Antichain(tc.set); got != tc.want {
				t.Fatalf("Antichain(%v) = %v, want %v", tc.set, got, tc.want)
			}
		})
	}
}

// Every enum value is reachable from the root, so the tree has no orphaned
// subtree and Resolve terminates from any member.
func TestTreeIsRooted(t *testing.T) {
	var names []string
	for v := range parent {
		if !under(v, typev1.TxType_AMBIGUOUS) {
			names = append(names, typev1.TxType_name[int32(v)])
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		t.Fatalf("values not under the root: %v", names)
	}
}

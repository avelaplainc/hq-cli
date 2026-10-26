package query

import "hq/pkg/tree"

// newFixture returns a shared sample tree. The tests construct nodes
// directly, so the tree package parser plays no part here.
func newFixture() *tree.Node {
	return &tree.Node{
		Kind: tree.KindObject,
		Fields: []tree.Field{
			{Key: "meta", Val: &tree.Node{
				Kind:   tree.KindObject,
				Fields: []tree.Field{{Key: "count", Val: &tree.Node{Kind: tree.KindNumber, Num: "2"}}},
			}},
			{Key: "labels", Val: &tree.Node{
				Kind: tree.KindArray,
				Items: []*tree.Node{
					{Kind: tree.KindString, Str: "a"},
					{Kind: tree.KindString, Str: "b"},
				},
			}},
			{Key: "terraform", Val: &tree.Node{
				Kind:   tree.KindObject,
				Fields: []tree.Field{{Key: "key.with.dots", Val: &tree.Node{Kind: tree.KindString, Str: "x"}}},
			}},
		},
	}
}

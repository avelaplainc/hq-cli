package query

import (
	"reflect"
	"strings"
	"testing"

	"hq/pkg/tree"
)

func TestEval(t *testing.T) {
	fix := newFixture()
	scalar := &tree.Node{Kind: tree.KindString, Str: "s"}

	tests := []struct {
		name        string
		expr        string
		root        *tree.Node
		want        []*tree.Node // nil with wantNoMatch true means expect empty result
		wantNoMatch bool
		wantErr     string
	}{
		{name: "dot selects root", expr: ".", root: fix, want: []*tree.Node{fix}},
		{name: "field chain", expr: ".meta.count", root: fix, want: []*tree.Node{fix.Fields[0].Val.Fields[0].Val}},
		{name: "bare identifier start", expr: "meta.count", root: fix, want: []*tree.Node{fix.Fields[0].Val.Fields[0].Val}},
		{name: "splat array", expr: ".labels[]", root: fix, want: fix.Fields[1].Val.Items},
		{name: "splat array with dot", expr: ".labels.*", root: fix, want: fix.Fields[1].Val.Items},
		{name: "splat object values", expr: ".meta.*", root: fix, want: []*tree.Node{fix.Fields[0].Val.Fields[0].Val}},
		{name: "root splat array", expr: "[]", root: fix.Fields[1].Val, want: fix.Fields[1].Val.Items},
		{name: "root splat star", expr: "*", root: fix, want: []*tree.Node{
			fix.Fields[0].Val, fix.Fields[1].Val, fix.Fields[2].Val,
		}},
		{name: "index", expr: ".labels[0]", root: fix, want: []*tree.Node{fix.Fields[1].Val.Items[0]}},
		{name: "negative index", expr: ".labels[-1]", root: fix, want: []*tree.Node{fix.Fields[1].Val.Items[1]}},
		{name: "index out of range", expr: ".labels[9]", root: fix, wantNoMatch: true},
		{name: "negative index out of range", expr: ".labels[-3]", root: fix, wantNoMatch: true},
		{name: "bracket string key with dots", expr: `["key.with.dots"]`, root: fix.Fields[2].Val, want: []*tree.Node{fix.Fields[2].Val.Fields[0].Val}},
		{name: "bracket key after path", expr: `.terraform["key.with.dots"]`, root: fix, want: []*tree.Node{fix.Fields[2].Val.Fields[0].Val}},
		{name: "bracket key escape", expr: `["a\"b\\c"]`, root: &tree.Node{
			Kind:   tree.KindObject,
			Fields: []tree.Field{{Key: `a"b\c`, Val: scalar}},
		}, want: []*tree.Node{scalar}},
		{name: "splat then field", expr: ".labels[].x", root: fix, wantNoMatch: true},
		{name: "pipe length object", expr: ".meta | length", root: fix, want: []*tree.Node{{Kind: tree.KindNumber, Num: "1"}}},
		{name: "pipe length array", expr: ".labels | length", root: fix, want: []*tree.Node{{Kind: tree.KindNumber, Num: "2"}}},
		{name: "pipe length scalar", expr: ".meta.count | length", root: fix, want: []*tree.Node{{Kind: tree.KindNumber, Num: "1"}}},
		{name: "pipe keys", expr: ".meta | keys", root: fix, want: []*tree.Node{{
			Kind:  tree.KindArray,
			Items: []*tree.Node{{Kind: tree.KindString, Str: "count"}},
		}}},
		{name: "pipe keys then length", expr: ". | keys | length", root: fix, want: []*tree.Node{{Kind: tree.KindNumber, Num: "3"}}},
		{name: "keys on non-object drops", expr: ".labels | keys", root: fix, wantNoMatch: true},
		{name: "bare length alone acts as function", expr: "length", root: &tree.Node{
			Kind:   tree.KindObject,
			Fields: []tree.Field{{Key: "length", Val: scalar}},
		}, want: []*tree.Node{{Kind: tree.KindNumber, Num: "1"}}},
		{name: "missing key", expr: ".absent", root: fix, wantNoMatch: true},
		{name: "field on array drops", expr: ".labels.x", root: fix, wantNoMatch: true},
		{name: "index on object drops", expr: ".meta[0]", root: fix, wantNoMatch: true},
		{name: "whitespace around tokens", expr: " .meta  |  length ", root: fix, want: []*tree.Node{{Kind: tree.KindNumber, Num: "1"}}},

		{name: "empty expression", expr: "", root: fix, wantErr: "unexpected end of expression"},
		{name: "dot then name missing", expr: ".a.", root: fix, wantErr: "after '.'"},
		{name: "unclosed bracket", expr: ".labels[", root: fix, wantErr: "missing ']'"},
		{name: "unclosed string key", expr: `["x`, root: fix, wantErr: "unterminated string"},
		{name: "invalid escape", expr: `["x\q"]`, root: fix, wantErr: "invalid escape"},
		{name: "unexpected character", expr: ".a@", root: fix, wantErr: `unexpected character "@" at offset 2`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Eval(tt.expr, tt.root)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Eval(%q) error = nil, want %q", tt.expr, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Eval(%q) error = %v, want it to contain %q", tt.expr, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Eval(%q) unexpected error: %v", tt.expr, err)
			}
			if got == nil {
				t.Fatalf("Eval(%q) returned nil slice, want non-nil", tt.expr)
			}
			if tt.wantNoMatch {
				if len(got) != 0 {
					t.Fatalf("Eval(%q) = %v, want no match", tt.expr, got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Eval(%q) = %+v, want %+v", tt.expr, got, tt.want)
			}
		})
	}
}

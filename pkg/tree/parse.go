package tree

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// builder carries the parse state. It marks which object keys come from
// blocks, so that an attribute can not use the same key as a block.
type builder struct {
	src       []byte
	filename  string
	blockKeys map[*Node]map[string]bool
}

// Parse turns HCL source into an object tree. It returns an error when the
// source has a syntax error or when a block key collides with an attribute
// at the same level.
func Parse(src []byte, filename string) (*Node, error) {
	file, diags := hclsyntax.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, diagnosticsError(filename, diags)
	}
	b := &builder{src: src, filename: filename, blockKeys: map[*Node]map[string]bool{}}
	return b.bodyNode(file.Body.(*hclsyntax.Body))
}

// ParseFile reads a file and parses it as HCL.
func ParseFile(path string) (*Node, error) {
	src, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("%s: read failed: %v", path, err)
	}
	return Parse(src, path)
}

// diagnosticsError makes one compact line per error diagnostic.
func diagnosticsError(filename string, diags hcl.Diagnostics) error {
	msg := ""
	for _, diag := range diags {
		if diag.Severity != hcl.DiagError {
			continue
		}
		pos := hcl.Pos{Line: 1, Column: 1}
		if diag.Subject != nil {
			pos = diag.Subject.Start
		}
		if msg != "" {
			msg += "; "
		}
		if diag.Detail != "" {
			msg += fmt.Sprintf("%s:%d,%d: %s: %s", filename, pos.Line, pos.Column, diag.Summary, diag.Detail)
		} else {
			msg += fmt.Sprintf("%s:%d,%d: %s", filename, pos.Line, pos.Column, diag.Summary)
		}
	}
	return fmt.Errorf("%s", msg)
}

// bodyNode makes an object node from a body. Fields keep source order.
func (b *builder) bodyNode(body *hclsyntax.Body) (*Node, error) {
	obj := &Node{Kind: KindObject}

	attrs := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, attr := range body.Attributes {
		attrs = append(attrs, attr)
	}
	sort.Slice(attrs, func(i, j int) bool {
		return attrs[i].NameRange.Start.Byte < attrs[j].NameRange.Start.Byte
	})

	attrAt, blockAt := 0, 0
	for attrAt < len(attrs) || blockAt < len(body.Blocks) {
		nextAttr, nextBlock := int(^uint(0)>>1), int(^uint(0)>>1)
		if attrAt < len(attrs) {
			nextAttr = attrs[attrAt].NameRange.Start.Byte
		}
		if blockAt < len(body.Blocks) {
			nextBlock = body.Blocks[blockAt].TypeRange.Start.Byte
		}
		if nextAttr < nextBlock {
			attr := attrs[attrAt]
			attrAt++
			if b.blockKey(obj, attr.Name) {
				return nil, fmt.Errorf("%s: line %d: the key %q is already in use by a block",
					b.filename, attr.NameRange.Start.Line, attr.Name)
			}
			val, err := b.exprNode(attr.Expr)
			if err != nil {
				return nil, err
			}
			obj.Fields = append(obj.Fields, Field{Key: attr.Name, Val: val})
		} else {
			block := body.Blocks[blockAt]
			blockAt++
			if err := b.addBlock(obj, block); err != nil {
				return nil, err
			}
		}
	}
	return obj, nil
}

// addBlock inserts a block at the path of its type plus labels. A repeated
// path makes the leaf value an array. A key that an attribute already uses
// at the same level is an error.
func (b *builder) addBlock(obj *Node, block *hclsyntax.Block) error {
	path := append([]string{block.Type}, block.Labels...)
	lines := make([]int, len(path))
	lines[0] = block.TypeRange.Start.Line
	for i := 1; i < len(path); i++ {
		lines[i] = block.TypeRange.Start.Line
		if i-1 < len(block.LabelRanges) {
			lines[i] = block.LabelRanges[i-1].Start.Line
		}
	}

	cur := obj
	for i, seg := range path {
		last := i == len(path)-1
		idx := fieldIndex(cur, seg)
		if idx == -1 {
			if last {
				val, err := b.bodyNode(block.Body)
				if err != nil {
					return err
				}
				cur.Fields = append(cur.Fields, Field{Key: seg, Val: val})
				b.markBlockKey(cur, seg)
				return nil
			}
			next := &Node{Kind: KindObject}
			cur.Fields = append(cur.Fields, Field{Key: seg, Val: next})
			b.markBlockKey(cur, seg)
			cur = next
			continue
		}
		existing := cur.Fields[idx].Val
		if !b.blockKey(cur, seg) {
			return fmt.Errorf("%s: line %d: the key %q is already in use by an attribute",
				b.filename, lines[i], seg)
		}
		if last {
			val, err := b.bodyNode(block.Body)
			if err != nil {
				return err
			}
			if existing.Kind == KindArray {
				existing.Items = append(existing.Items, val)
			} else {
				cur.Fields[idx].Val = &Node{Kind: KindArray, Items: []*Node{existing, val}}
			}
			return nil
		}
		if existing.Kind != KindObject {
			return fmt.Errorf("%s: line %d: the key %q holds a value, so a block can not nest inside it",
				b.filename, lines[i], seg)
		}
		cur = existing
	}
	return nil
}

// exprNode values an expression with no variables. When the expression
// needs context, it falls back to the exact source text of the expression.
func (b *builder) exprNode(expr hclsyntax.Expression) (*Node, error) {
	// Object and tuple constructs that hold variables can not be valued.
	// Build them from the tree of expressions, so that queries reach
	// every item. Items then fall back to their source text.
	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		obj := &Node{Kind: KindObject}
		for _, item := range e.Items {
			key := b.exprKeyString(item.KeyExpr)
			val, err := b.exprNode(item.ValueExpr)
			if err != nil {
				return nil, err
			}
			obj.Fields = append(obj.Fields, Field{Key: key, Val: val})
		}
		return obj, nil
	case *hclsyntax.TupleConsExpr:
		arr := &Node{Kind: KindArray}
		for _, ex := range e.Exprs {
			item, err := b.exprNode(ex)
			if err != nil {
				return nil, err
			}
			arr.Items = append(arr.Items, item)
		}
		return arr, nil
	}
	val, diags := expr.Value(nil)
	if diags.HasErrors() {
		r := expr.Range()
		lo, hi := r.Start.Byte, r.End.Byte
		if lo < 0 || hi > len(b.src) || lo > hi {
			lo, hi = 0, 0
		}
		return &Node{Kind: KindString, Str: string(b.src[lo:hi])}, nil
	}
	return ctyNode(val), nil
}

// exprKeyString returns the object constructor key as a literal string.
// A key that needs context falls back to its source text.
func (b *builder) exprKeyString(key hclsyntax.Expression) string {
	if v, diags := key.Value(nil); !diags.HasErrors() && v.Type() == cty.String {
		return v.AsString()
	}
	r := key.Range()
	lo, hi := r.Start.Byte, r.End.Byte
	if lo < 0 || hi > len(b.src) || lo > hi {
		lo, hi = 0, 0
	}
	return string(b.src[lo:hi])
}

// ctyNode converts a cty value to a node. Tuple and map items keep
// iteration order.
func ctyNode(v cty.Value) *Node {
	if v.IsNull() {
		return &Node{Kind: KindNull}
	}
	t := v.Type()
	switch {
	case t == cty.Bool:
		return &Node{Kind: KindBool, Bool: v.True()}
	case t == cty.Number:
		return &Node{Kind: KindNumber, Num: json.Number(v.AsBigFloat().Text('f', -1))}
	case t == cty.String:
		return &Node{Kind: KindString, Str: v.AsString()}
	case t.IsListType() || t.IsSetType() || t.IsTupleType():
		n := &Node{Kind: KindArray}
		for it := v.ElementIterator(); it.Next(); {
			_, item := it.Element()
			n.Items = append(n.Items, ctyNode(item))
		}
		return n
	case t.IsMapType() || t.IsObjectType():
		n := &Node{Kind: KindObject}
		for it := v.ElementIterator(); it.Next(); {
			key, item := it.Element()
			n.Fields = append(n.Fields, Field{Key: key.AsString(), Val: ctyNode(item)})
		}
		return n
	default:
		return &Node{Kind: KindNull}
	}
}

// fieldIndex returns the index of the field with the key, or -1.
func fieldIndex(n *Node, key string) int {
	for i := range n.Fields {
		if n.Fields[i].Key == key {
			return i
		}
	}
	return -1
}

// blockKey reports whether a key of an object comes from a block.
func (b *builder) blockKey(n *Node, key string) bool {
	return b.blockKeys[n][key]
}

// markBlockKey records that a key of an object comes from a block.
func (b *builder) markBlockKey(n *Node, key string) {
	m, ok := b.blockKeys[n]
	if !ok {
		m = map[string]bool{}
		b.blockKeys[n] = m
	}
	m[key] = true
}

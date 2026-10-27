// Package query evaluates a small yq/jq style expression against a tree.Node.
package query

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"hq/pkg/tree"
)

// Eval parses expr and evaluates it against root.
// It returns the matched nodes in encounter order.
// An empty result means no match. It is not an error.
func Eval(expr string, root *tree.Node) ([]*tree.Node, error) {
	units, err := parseUnits(expr)
	if err != nil {
		return nil, err
	}
	ctx := []*tree.Node{root}
	for _, u := range units {
		var err error
		if u.fn != "" {
			ctx, err = applyFn(ctx, u.fn)
		} else {
			for _, s := range u.segs {
				if ctx, err = applySegment(ctx, s); err != nil {
					break
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if ctx == nil {
		ctx = []*tree.Node{}
	}
	return ctx, nil
}

type segKind int

const (
	segField segKind = iota
	segSplat
	segIndex
)

type segment struct {
	kind  segKind
	field string // segField
	index int    // segIndex
}

type unit struct {
	fn   string // "length", "keys", or "" for a path
	segs []segment
}

// parseUnits parses the full expression: unit ( '|' unit )*.
func parseUnits(expr string) ([]unit, error) {
	p := &parser{expr: expr}
	var units []unit
	for {
		u, err := p.parseUnit()
		if err != nil {
			return nil, err
		}
		p.afterPipe = false
		units = append(units, u)
		p.skipWS()
		if p.eof() {
			return units, nil
		}
		if p.cur() != '|' {
			return nil, p.unexpected()
		}
		p.pos++
		p.afterPipe = true
	}
}

// parseUnit parses one pipe unit: a path, 'length', or 'keys'.
func (p *parser) parseUnit() (unit, error) {
	var u unit
	p.skipWS()
	if p.eof() {
		return u, fmt.Errorf("query: unexpected end of expression at offset %d", p.pos)
	}
	switch c := p.cur(); {
	case c == '.':
		p.pos++
		if err := p.afterDot(&u, true); err != nil {
			return u, err
		}
	case c == '*':
		p.pos++
		u.segs = append(u.segs, segment{kind: segSplat})
	case c == '[':
		seg, err := p.bracketSegment()
		if err != nil {
			return u, err
		}
		u.segs = append(u.segs, seg)
	case isIdentStart(c):
		name := p.scanIdent()
		p.skipWS()
		if isFn(name) && (p.eof() || p.cur() == '|') {
			u.fn = name
			return u, nil
		}
		if p.afterPipe {
			// After a pipe, a bare name can only be a function.
			return u, fmt.Errorf("query: %s is not a query function", name)
		}
		u.segs = append(u.segs, segment{kind: segField, field: name})
	default:
		return u, p.unexpected()
	}
	for {
		p.skipWS()
		if p.eof() || p.cur() == '|' {
			return u, nil
		}
		switch p.cur() {
		case '.':
			p.pos++
			if err := p.afterDot(&u, false); err != nil {
				return u, err
			}
		case '[':
			seg, err := p.bracketSegment()
			if err != nil {
				return u, err
			}
			u.segs = append(u.segs, seg)
		default:
			return u, p.unexpected()
		}
	}
}

// afterDot parses what may follow a consumed '.'.
// A '.' at path start with nothing after it selects the root node.
// A '.' before '[' adds nothing. The caller parses the bracket.
func (p *parser) afterDot(u *unit, atStart bool) error {
	p.skipWS()
	if p.eof() || p.cur() == '|' {
		if atStart {
			return nil
		}
		return fmt.Errorf("query: expected name or '*' after '.' at offset %d", p.pos)
	}
	switch c := p.cur(); {
	case c == '*':
		p.pos++
		u.segs = append(u.segs, segment{kind: segSplat})
	case isIdentStart(c):
		u.segs = append(u.segs, segment{kind: segField, field: p.scanIdent()})
	case c == '[':
		// The caller's loop parses the bracket segment.
	default:
		return p.unexpected()
	}
	return nil
}

// bracketSegment parses one of '[]', '[N]', or '["key"]'.
func (p *parser) bracketSegment() (segment, error) {
	open := p.pos
	p.pos++ // consume '['
	p.skipWS()
	if p.eof() {
		return segment{}, fmt.Errorf("query: missing ']' for '[' at offset %d", open)
	}
	switch c := p.cur(); c {
	case ']':
		p.pos++
		return segment{kind: segSplat}, nil
	case '"':
		key, err := p.quotedKey()
		if err != nil {
			return segment{}, err
		}
		if err := p.expect(']'); err != nil {
			return segment{}, err
		}
		return segment{kind: segField, field: key}, nil
	default:
		start := p.pos
		if c == '-' {
			p.pos++
		}
		digits := p.pos
		for !p.eof() && isDigit(p.cur()) {
			p.pos++
		}
		if p.pos == digits {
			return segment{}, p.unexpected()
		}
		n, err := strconv.Atoi(p.expr[start:p.pos])
		if err != nil {
			return segment{}, fmt.Errorf("query: invalid index at offset %d", start)
		}
		if err := p.expect(']'); err != nil {
			return segment{}, err
		}
		return segment{kind: segIndex, index: n}, nil
	}
}

// quotedKey parses a double-quoted key. Only \\ and \" escapes are valid.
func (p *parser) quotedKey() (string, error) {
	p.pos++ // consume '"'
	var b strings.Builder
	for {
		if p.eof() {
			return "", fmt.Errorf("query: unterminated string at offset %d", p.pos)
		}
		c := p.cur()
		switch c {
		case '"':
			p.pos++
			return b.String(), nil
		case '\\':
			p.pos++
			if p.eof() || (p.cur() != '\\' && p.cur() != '"') {
				return "", fmt.Errorf("query: invalid escape at offset %d", p.pos-1)
			}
			b.WriteByte(p.cur())
			p.pos++
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
}

func (p *parser) expect(want byte) error {
	p.skipWS()
	if p.eof() || p.cur() != want {
		return fmt.Errorf("query: expected %q at offset %d", string(want), p.pos)
	}
	p.pos++
	return nil
}

func (p *parser) unexpected() error {
	return fmt.Errorf("query: unexpected character %q at offset %d", string(p.cur()), p.pos)
}

type parser struct {
	expr      string
	pos       int
	afterPipe bool // the unit that follows the last '|' has begun
}

// isFn tells if name is one of the pipe functions.
func isFn(name string) bool {
	return name == "length" || name == "keys" || name == "not"
}

func (p *parser) eof() bool { return p.pos >= len(p.expr) }

func (p *parser) cur() byte { return p.expr[p.pos] }

func (p *parser) skipWS() {
	for !p.eof() {
		switch p.cur() {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) scanIdent() string {
	start := p.pos
	for !p.eof() && isIdentChar(p.cur()) {
		p.pos++
	}
	return p.expr[start:p.pos]
}

func isIdentStart(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '-'
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// applySegment maps every context node through one path segment. A
// segment that does not fit a node, like an index on a string, is an
// error, like jq. A name that a node does not hold is no match.
func applySegment(ctx []*tree.Node, s segment) ([]*tree.Node, error) {
	out := make([]*tree.Node, 0, len(ctx))
	for _, n := range ctx {
		switch s.kind {
		case segField:
			if n.Kind == tree.KindArray {
				return nil, fmt.Errorf("query: can not index an array with the name %q", s.field)
			}
			if n.Kind != tree.KindObject {
				return nil, fmt.Errorf("query: can not index %s with %q", kindName(n), s.field)
			}
			if v := n.Get(s.field); v != nil {
				out = append(out, v)
			}
		case segIndex:
			if n.Kind != tree.KindArray {
				return nil, fmt.Errorf("query: can not index %s with a number", kindName(n))
			}
			i := s.index
			if i < 0 {
				i += len(n.Items)
			}
			if i >= 0 && i < len(n.Items) {
				out = append(out, n.Items[i])
			}
		case segSplat:
			switch n.Kind {
			case tree.KindArray:
				out = append(out, n.Items...)
			case tree.KindObject:
				for _, f := range n.Fields {
					out = append(out, f.Val)
				}
			default:
				return nil, fmt.Errorf("query: can not iterate over %s", kindName(n))
			}
		}
	}
	return out, nil
}

// applyFn maps every context node through 'length', 'keys', or 'not'.
// A non-object node yields no match for 'keys'.
func applyFn(ctx []*tree.Node, name string) ([]*tree.Node, error) {
	if !isFn(name) {
		return nil, fmt.Errorf("query: %s is not a query function", name)
	}
	out := make([]*tree.Node, 0, len(ctx))
	for _, n := range ctx {
		switch name {
		case "length":
			var count int
			switch n.Kind {
			case tree.KindObject:
				count = len(n.Fields)
			case tree.KindArray:
				count = len(n.Items)
			default:
				count = 1
			}
			out = append(out, &tree.Node{
				Kind: tree.KindNumber,
				Num:  json.Number(strconv.Itoa(count)),
			})
		case "keys":
			if n.Kind != tree.KindObject {
				continue
			}
			items := make([]*tree.Node, 0, len(n.Fields))
			for _, f := range n.Fields {
				items = append(items, &tree.Node{Kind: tree.KindString, Str: f.Key})
			}
			out = append(out, &tree.Node{Kind: tree.KindArray, Items: items})
		case "not":
			out = append(out, &tree.Node{Kind: tree.KindBool, Bool: !truthy(n)})
		}
	}
	return out, nil
}

// truthy follows jq: null and false are false, all else is true.
func truthy(n *tree.Node) bool {
	switch n.Kind {
	case tree.KindNull:
		return false
	case tree.KindBool:
		return n.Bool
	default:
		return true
	}
}

// kindName names a node for error messages.
func kindName(n *tree.Node) string {
	switch n.Kind {
	case tree.KindObject:
		return "an object"
	case tree.KindArray:
		return "an array"
	case tree.KindString:
		return "a string"
	case tree.KindNumber:
		return "a number"
	case tree.KindBool:
		return "a boolean"
	default:
		return "null"
	}
}

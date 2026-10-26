package tree

import (
	"bytes"
	"encoding/json"
)

// Kind tells which fields of Node hold data.
type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindObject
	KindArray
)

// Node is one value of a parsed HCL document.
type Node struct {
	Kind   Kind        // discriminates the fields below
	Bool   bool        // KindBool
	Num    json.Number // KindNumber
	Str    string      // KindString
	Fields []Field     // KindObject, insertion order preserved
	Items  []*Node     // KindArray
}

// Field is one key and value of an object node.
type Field struct {
	Key string
	Val *Node
}

// Get returns the field value for a key. It returns nil if the key is
// absent or if the node is not an object.
func (n *Node) Get(key string) *Node {
	if n == nil || n.Kind != KindObject {
		return nil
	}
	for i := range n.Fields {
		if n.Fields[i].Key == key {
			return n.Fields[i].Val
		}
	}
	return nil
}

// MarshalJSON writes the node as JSON. Object field order is kept.
func (n *Node) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := n.writeJSON(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (n *Node) writeJSON(buf *bytes.Buffer) error {
	if n == nil {
		buf.WriteString("null")
		return nil
	}
	switch n.Kind {
	case KindObject:
		buf.WriteByte('{')
		for i := range n.Fields {
			if i > 0 {
				buf.WriteByte(',')
			}
			appendJSONString(buf, n.Fields[i].Key)
			buf.WriteByte(':')
			if err := n.Fields[i].Val.writeJSON(buf); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case KindArray:
		buf.WriteByte('[')
		for i := range n.Items {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := n.Items[i].writeJSON(buf); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case KindNull:
		buf.WriteString("null")
	case KindBool:
		if n.Bool {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case KindNumber:
		if len(n.Num) == 0 {
			buf.WriteByte('0')
		} else {
			buf.WriteString(string(n.Num))
		}
	case KindString:
		appendJSONString(buf, n.Str)
	default:
		buf.WriteString("null")
	}
	return nil
}

// appendJSONString writes s as a JSON string. Unlike json.Marshal, it does
// not escape <, >, and &.
func appendJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString("\\\"")
		case '\\':
			buf.WriteString("\\\\")
		case '\n':
			buf.WriteString("\\n")
		case '\r':
			buf.WriteString("\\r")
		case '\t':
			buf.WriteString("\\t")
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				buf.WriteString("\\u00")
				buf.WriteByte(hex[r>>4])
				buf.WriteByte(hex[r&0xF])
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

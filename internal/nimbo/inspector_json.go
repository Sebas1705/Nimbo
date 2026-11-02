package nimbo

// Small JSON and XML helpers shared by the observe area (inspector, chaos, traces, kube). The Kotlin code reads
// emulator replies as loose JsonElement trees; this is the Go equivalent. Objects keep the order of their keys
// (Go maps do not), because the UI shows DynamoDB attributes in the order the emulator sent them.

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

// obsObject is a JSON object that remembers the order of its keys.
type obsObject struct {
	keys []string
	vals map[string]any
}

func newObsObject() *obsObject { return &obsObject{vals: map[string]any{}} }

// set keeps the first position of a key and the last value, like a LinkedHashMap.
func (o *obsObject) set(key string, v any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *obsObject) Get(key string) any {
	if o == nil {
		return nil
	}
	return o.vals[key]
}

func (o *obsObject) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

func (o *obsObject) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// obsParse reads one JSON document. Values are nil, bool, json.Number, string, []any or *obsObject.
func obsParse(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := obsValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func obsValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil // string, json.Number, bool or nil
	}
	switch d {
	case '{':
		obj := newObsObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, _ := kt.(string)
			val, err := obsValue(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key, val)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for dec.More() {
			val, err := obsValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, errors.New("unexpected JSON delimiter")
}

// obsParseObject reads a JSON object, treating a blank body as {} (several emulators answer 204 or nothing).
func obsParseObject(s string) (*obsObject, error) {
	if strings.TrimSpace(s) == "" {
		return newObsObject(), nil
	}
	v, err := obsParse(s)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*obsObject)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return o, nil
}

func obsObj(v any) *obsObject { o, _ := v.(*obsObject); return o }
func obsArr(v any) []any      { a, _ := v.([]any); return a }

// obsText is the content of a JSON primitive (Kotlin's contentOrNull): null, objects and arrays have none.
func obsText(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		return strconv.FormatBool(t), true
	}
	return "", false
}

// obsStrOr is the text of a key, or the fallback when it is missing or not a primitive.
func obsStrOr(o *obsObject, key, fallback string) string {
	if s, ok := obsText(o.Get(key)); ok {
		return s
	}
	return fallback
}

// obsInt64 is Kotlin's longOrNull: the content of a primitive read as a whole number.
func obsInt64(v any) (int64, bool) {
	s, ok := obsText(v)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// obsInt32 is Kotlin's intOrNull.
func obsInt32(v any) (int, bool) {
	s, ok := obsText(v)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 32)
	return int(n), err == nil
}

// obsAtoi32 is String.toIntOrNull for query parameters.
func obsAtoi32(s string) (int, bool) {
	n, err := strconv.ParseInt(s, 10, 32)
	return int(n), err == nil
}

// obsTake is Kotlin's String.take: at most n characters, never a broken one.
func obsTake(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n { // fewer bytes than n means fewer characters too
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func obsClamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func obsPtr[T any](v T) *T { return &v }

// ---- XML ----------------------------------------------------------------------------------------------------------

// obsXMLNode is just enough DOM for the S3 and SQS replies: names without namespaces, and text in document order.
type obsXMLNode struct {
	name string
	kids []any // string or *obsXMLNode
}

// elements are all descendants called tag, in document order (getElementsByTagName).
func (n *obsXMLNode) elements(tag string) []*obsXMLNode {
	var out []*obsXMLNode
	var walk func(*obsXMLNode)
	walk = func(p *obsXMLNode) {
		for _, k := range p.kids {
			if c, ok := k.(*obsXMLNode); ok {
				if c.name == tag {
					out = append(out, c)
				}
				walk(c)
			}
		}
	}
	walk(n)
	return out
}

func (n *obsXMLNode) content() string {
	var b strings.Builder
	for _, k := range n.kids {
		switch t := k.(type) {
		case string:
			b.WriteString(t)
		case *obsXMLNode:
			b.WriteString(t.content())
		}
	}
	return b.String()
}

// text is the text of the first descendant called tag.
func (n *obsXMLNode) text(tag string) (string, bool) {
	els := n.elements(tag)
	if len(els) == 0 {
		return "", false
	}
	return els[0].content(), true
}

func (n *obsXMLNode) textOr(tag string) string {
	s, _ := n.text(tag)
	return s
}

func (n *obsXMLNode) textPtr(tag string) *string {
	if s, ok := n.text(tag); ok {
		return &s
	}
	return nil
}

// obsParseXML refuses any document type declaration: entities are how XML bombs and file reads are done, and no
// emulator needs them.
func obsParseXML(body string) (*obsXMLNode, error) {
	dec := xml.NewDecoder(strings.NewReader(body))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var root *obsXMLNode
	var stack []*obsXMLNode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &obsXMLNode{name: t.Name.Local}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("more than one root element")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, string(t))
			} else if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("text outside the root element")
			}
		case xml.Directive:
			return nil, errors.New("document type declarations are not allowed")
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, errors.New("no complete root element")
	}
	return root, nil
}

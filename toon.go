package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// --toon emits the same report as --json in TOON (toonformat.dev): the format
// agents read cheapest — tables for uniform rows, no repeated keys, ~40%
// fewer tokens than the equivalent JSON. The report's schema is fixed, so the
// encoder only needs objects, scalars, primitive arrays, and arrays of
// uniform objects — the shapes this tool produces.
//
// Field order is preserved by decoding the marshaled JSON with a token
// stream instead of a map: a report that reorders its keys between runs would
// be a contract regression, and a map would hide it.

// toonObj is a JSON object with its key order kept.
type toonObj struct {
	keys []string
	vals map[string]any
}

// reportTOON renders the report document. Internal logic stays on the Report
// struct; TOON is only an output encoding, per AXI.
func (r *reporter) reportTOON() (string, error) {
	data, err := json.Marshal(r.rep)
	if err != nil {
		return "", err
	}
	v, err := decodeOrdered(data)
	if err != nil {
		return "", fmt.Errorf("ordered decode: %w", err)
	}
	obj, ok := v.(*toonObj)
	if !ok {
		return "", fmt.Errorf("report is not an object")
	}
	var b strings.Builder
	emitObj(&b, obj, 0)
	return b.String(), nil
}

// decodeOrdered parses JSON into a tree where objects keep their key order.
func decodeOrdered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return parseTOON(dec)
}

func parseTOON(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // scalar: string, json.Number, bool, nil
	}
	switch delim {
	case '{':
		obj := &toonObj{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := kt.(string)
			val, err := parseTOON(dec)
			if err != nil {
				return nil, err
			}
			obj.keys = append(obj.keys, key)
			obj.vals[key] = val
		}
		_, err := dec.Token() // consume '}'
		return obj, err
	case '[':
		var arr []any
		for dec.More() {
			val, err := parseTOON(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		_, err := dec.Token() // consume ']'
		return arr, err
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

const toonIndent = "  "

func emitObj(b *strings.Builder, obj *toonObj, depth int) {
	for _, key := range obj.keys {
		emitKV(b, depth, key, obj.vals[key])
	}
}

func emitKV(b *strings.Builder, depth int, key string, val any) {
	ind := strings.Repeat(toonIndent, depth)
	switch v := val.(type) {
	case *toonObj:
		fmt.Fprintf(b, "%s%s:\n", ind, toonKey(key))
		emitObj(b, v, depth+1)
	case []any:
		emitArr(b, depth, ind+toonKey(key), v)
	default:
		fmt.Fprintf(b, "%s%s: %s\n", ind, toonKey(key), toonScalar(v))
	}
}

// emitArr writes key[N] in the three shapes our schema uses: a primitive row
// inline, a uniform-object table with a {fields} header, or - items for the
// irregular rest.
func emitArr(b *strings.Builder, depth int, key string, arr []any) {
	if len(arr) == 0 {
		fmt.Fprintf(b, "%s[0]:\n", key)
		return
	}
	if allScalars(arr) {
		vals := make([]string, len(arr))
		for i, v := range arr {
			vals[i] = toonScalar(v)
		}
		fmt.Fprintf(b, "%s[%d]: %s\n", key, len(arr), strings.Join(vals, ","))
		return
	}
	if fields, ok := uniformKeys(arr); ok {
		fmt.Fprintf(b, "%s[%d]{%s}:\n", key, len(arr), strings.Join(fields, ","))
		ind := strings.Repeat(toonIndent, depth+1)
		for _, item := range arr {
			obj := item.(*toonObj)
			vals := make([]string, len(fields))
			for i, f := range fields {
				vals[i] = toonScalar(obj.vals[f])
			}
			fmt.Fprintf(b, "%s%s\n", ind, strings.Join(vals, ","))
		}
		return
	}
	fmt.Fprintf(b, "%s[%d]:\n", key, len(arr))
	ind := strings.Repeat(toonIndent, depth+1)
	for _, item := range arr {
		switch v := item.(type) {
		case *toonObj:
			// First field on the dash line, the rest under it.
			fmt.Fprintf(b, "%s- %s: %s\n", ind, toonKey(v.keys[0]), toonScalar(v.vals[v.keys[0]]))
			for _, k := range v.keys[1:] {
				emitKV(b, depth+2, k, v.vals[k])
			}
		default:
			fmt.Fprintf(b, "%s- %s\n", ind, toonScalar(v))
		}
	}
}

func allScalars(arr []any) bool {
	for _, v := range arr {
		switch v.(type) {
		case *toonObj, []any:
			return false
		}
	}
	return true
}

// uniformKeys returns the shared field list when every item is an object with
// the same keys in the same order — the shape that makes a TOON table.
func uniformKeys(arr []any) ([]string, bool) {
	first, ok := arr[0].(*toonObj)
	if !ok {
		return nil, false
	}
	for _, item := range arr[1:] {
		obj, ok := item.(*toonObj)
		if !ok || len(obj.keys) != len(first.keys) {
			return nil, false
		}
		for i, k := range obj.keys {
			if k != first.keys[i] {
				return nil, false
			}
		}
	}
	return first.keys, true
}

// toonScalar prints one primitive. Strings quote only when they have to —
// TOON is meant to be skimmed, and a command line full of escapes defeats it.
func toonScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return toonStr(t)
	default:
		return toonStr(fmt.Sprint(t))
	}
}

func toonKey(k string) string { return toonStr(k) }

func toonStr(s string) string {
	if !toonNeedsQuote(s) {
		return s
	}
	return `"` + toonEscaper.Replace(s) + `"`
}

var toonEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
)

// toonNeedsQuote is conservative: over-quoting is always valid TOON, an
// unquoted ambiguous string is a parse bug on the consumer's side.
func toonNeedsQuote(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return true
	}
	if strings.ContainsAny(s, `":,\[\]{}|`+"\n\r\t`#&*!?%@") {
		return true
	}
	if s[0] == '-' || s[0] == '>' || s[0] == '<' {
		return true // dash is the list marker; arrows read as block scalars
	}
	switch s {
	case "true", "false", "null", "-":
		return true
	}
	// A value out of float64 range (1e999) is still numeric syntax: ParseFloat
	// reports ErrRange for it, and a decoder would read it as a number all
	// the same.
	if _, err := strconv.ParseFloat(s, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		return true
	}
	return false
}

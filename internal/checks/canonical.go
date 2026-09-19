package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
)

// CanonicalJSON re-serializes a JSON document into the fleet record's one
// canonical form: sorted keys, two-space indent, ensure_ascii escapes,
// trailing LF — byte-equivalent to python's
// json.dumps(doc, sort_keys=True, indent=2) + "\n", the convention every
// committed fleet/stars/<star>/slag.json holds. Numbers round-trip as their
// source literals (json.Number), so an int never grows a ".0".
//
// THE SAME EMITTER HEPHAESTUS GRADES WITH (internal/slag/canonical.go),
// carried here so the record's own repo refuses a non-canonical record at the
// push instead of a consumer one repo away finding it in a golden. Measured
// 2026-09-18: foundry-dies #270 landed two records with a hand-placed block
// out of sorted order, foundry-dies' gate went green (schema-only), and
// hephaestus #120's mutation lane went could-not-run on
// TestCanonicalJSONRoundTripsEveryCommittedSlag. The two emitters must agree
// byte for byte; a divergence between them is a bug in whichever moved.
func CanonicalJSON(doc []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonical: unreadable JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("canonical: trailing data after document")
	}
	var b strings.Builder
	writeCanonical(&b, v, 0)
	b.WriteString("\n")
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, v any, indent int) {
	switch val := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		b.WriteString(val.String())
	case string:
		writeEscaped(b, val)
	case []any:
		if len(val) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, item := range val {
			padTo(b, indent+1)
			writeCanonical(b, item, indent+1)
			if i < len(val)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		padTo(b, indent)
		b.WriteString("]")
	case map[string]any:
		if len(val) == 0 {
			b.WriteString("{}")
			return
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("{\n")
		for i, k := range keys {
			padTo(b, indent+1)
			writeEscaped(b, k)
			b.WriteString(": ")
			writeCanonical(b, val[k], indent+1)
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		padTo(b, indent)
		b.WriteString("}")
	default:
		panic(fmt.Sprintf("canonical: unsupported type %T", v))
	}
}

func padTo(b *strings.Builder, indent int) {
	for range indent {
		b.WriteString("  ")
	}
}

// writeEscaped escapes exactly as python's ensure_ascii json.dumps does.
func writeEscaped(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || r > 0x7E && r <= 0xFFFF:
				fmt.Fprintf(b, `\u%04x`, r)
			case r > 0xFFFF:
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(b, `\u%04x\u%04x`, hi, lo)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

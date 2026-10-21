package dataverbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"
)

func invalid(message string) error { return svcerr.New(svcerr.CodeInvalid, message) }

// Decode closed request DTOs without rounding RawMessage numbers. Repeated keys
// are refused before Go's last-key-wins decoding. Top-level DTO names must
// match the catalog exactly; arbitrary property keys retain their case.
func decode(raw json.RawMessage, out any) error {
	if len(raw) > MaxInputBytes {
		return invalid("Data command exceeds the size limit.")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if bytes.TrimSpace(raw)[0] != '{' {
		return invalid("A JSON object is required.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := uniqueValue(decoder, 0); err != nil {
		return invalid("Malformed JSON or duplicate field.")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid("One JSON object is required.")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return invalid("Invalid command fields or field types.")
	}
	return nil
}
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid key")
			}
			if seen[name] {
				return fmt.Errorf("duplicate key")
			}
			seen[name] = true
			if err = uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected delimiter")
	}
	_, err = d.Token()
	return err
}

// Schemas describe the wire DTO shapes; cross-field and domain validation stays
// in objects.Service. Required keys are checked with the same catalog entries
// used for discovery, preventing absent members from accidentally clearing a set.
func inputSchema(t reflect.Type, required []string) map[string]any {
	schema := shape(t)
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
func shape(t reflect.Type) map[string]any {
	if t == reflect.TypeFor[json.RawMessage]() {
		return map[string]any{"type": "object"}
	}
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{shape(t.Elem()), map[string]any{"type": "null"}}}
	}
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Anonymous {
				nested := shape(field.Type)["properties"].(map[string]any)
				for k, v := range nested {
					properties[k] = v
				}
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			properties[name] = shape(field.Type)
		}
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	case reflect.Slice:
		return map[string]any{"anyOf": []any{map[string]any{"type": "array", "items": shape(t.Elem())}, map[string]any{"type": "null"}}}
	case reflect.Map:
		// Free-form objects such as fields, patch, arguments and filters. They
		// were advertised as strings, which made generated clients send JSON text
		// the server rejects.
		return map[string]any{"type": "object"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	default:
		return map[string]any{"type": "string"}
	}
}

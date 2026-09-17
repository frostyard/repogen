package intake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const maxSafeJSONInteger = 9007199254740991

type canonicalObject map[string]any
type canonicalArray []any

// CanonicalJSON returns the RFC 8785 subset used by retained intake records.
func CanonicalJSON(value any) ([]byte, error) {
	return canonicalJSON(value)
}

// DecodeCanonical verifies canonical bytes and rejects unknown fields before
// decoding the retained value.
func DecodeCanonical(data []byte, destination any) error {
	return decodeCanonical(data, destination)
}

func canonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical record: %v", ErrIntegrity, err)
	}
	canonical, err := canonicalizeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical record: %v", ErrIntegrity, err)
	}
	return canonical, nil
}

func canonicalizeJSON(data []byte) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("JSON must be valid UTF-8")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeCanonicalValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON contains trailing data")
		}
		return nil, fmt.Errorf("read trailing JSON data: %w", err)
	}
	return appendCanonicalValue(nil, value)
}

func decodeCanonicalValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode JSON value: %w", err)
	}

	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := canonicalObject{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("decode JSON object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("JSON object key is not a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("JSON object contains duplicate key %q", key)
				}
				item, err := decodeCanonicalValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			if err := consumeDelimiter(decoder, '}'); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			array := canonicalArray{}
			for decoder.More() {
				item, err := decodeCanonicalValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			if err := consumeDelimiter(decoder, ']'); err != nil {
				return nil, err
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	case json.Number:
		return canonicalInteger(value)
	case string:
		if !utf8.ValidString(value) {
			return nil, errors.New("JSON string must be valid UTF-8")
		}
		return value, nil
	case bool, nil:
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value type %T", value)
	}
}

func consumeDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON delimiter: %w", err)
	}
	actual, ok := token.(json.Delim)
	if !ok || actual != expected {
		return fmt.Errorf("expected JSON delimiter %q", expected)
	}
	return nil
}

func canonicalInteger(number json.Number) (json.Number, error) {
	text := number.String()
	if strings.ContainsAny(text, ".eE") {
		return "", fmt.Errorf("JSON number %q is not an integer", text)
	}

	integer := new(big.Int)
	if _, ok := integer.SetString(text, 10); !ok {
		return "", fmt.Errorf("JSON number %q is invalid", text)
	}
	limit := big.NewInt(maxSafeJSONInteger)
	if new(big.Int).Abs(new(big.Int).Set(integer)).Cmp(limit) > 0 {
		return "", fmt.Errorf("JSON integer %q exceeds the RFC 8785 safe range", text)
	}
	return json.Number(integer.String()), nil
}

func appendCanonicalValue(output []byte, value any) ([]byte, error) {
	switch value := value.(type) {
	case canonicalObject:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return utf16Less(keys[i], keys[j])
		})

		output = append(output, '{')
		for index, key := range keys {
			if index > 0 {
				output = append(output, ',')
			}
			output = appendJSONString(output, key)
			output = append(output, ':')
			var err error
			output, err = appendCanonicalValue(output, value[key])
			if err != nil {
				return nil, err
			}
		}
		return append(output, '}'), nil
	case canonicalArray:
		output = append(output, '[')
		for index, item := range value {
			if index > 0 {
				output = append(output, ',')
			}
			var err error
			output, err = appendCanonicalValue(output, item)
			if err != nil {
				return nil, err
			}
		}
		return append(output, ']'), nil
	case string:
		return appendJSONString(output, value), nil
	case json.Number:
		return append(output, value.String()...), nil
	case bool:
		if value {
			return append(output, "true"...), nil
		}
		return append(output, "false"...), nil
	case nil:
		return append(output, "null"...), nil
	default:
		return nil, fmt.Errorf("unsupported canonical JSON value type %T", value)
	}
}

func appendJSONString(output []byte, value string) []byte {
	const hexCharacters = "0123456789abcdef"

	output = append(output, '"')
	for _, character := range value {
		switch character {
		case '"', '\\':
			output = append(output, '\\', byte(character))
		case '\b':
			output = append(output, `\b`...)
		case '\t':
			output = append(output, `\t`...)
		case '\n':
			output = append(output, `\n`...)
		case '\f':
			output = append(output, `\f`...)
		case '\r':
			output = append(output, `\r`...)
		default:
			if character < 0x20 {
				output = append(
					output,
					'\\',
					'u',
					'0',
					'0',
					hexCharacters[character>>4],
					hexCharacters[character&0xf],
				)
				continue
			}
			output = utf8.AppendRune(output, character)
		}
	}
	return append(output, '"')
}

func utf16Less(left, right string) bool {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	for index := 0; index < len(leftUnits) && index < len(rightUnits); index++ {
		if leftUnits[index] != rightUnits[index] {
			return leftUnits[index] < rightUnits[index]
		}
	}
	return len(leftUnits) < len(rightUnits)
}

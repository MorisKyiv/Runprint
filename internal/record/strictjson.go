package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const maxJSONDepth = 128

func validateStrictJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("JSON is not valid UTF-8")
	}
	if err := rejectUnpairedSurrogates(data); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("record contains multiple JSON values")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("JSON nesting exceeds depth limit %d", maxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return errors.New("unexpected closing JSON delimiter")
	}
	return nil
}

func rejectUnpairedSurrogates(data []byte) error {
	for index := 0; index < len(data); index++ {
		if data[index] != '"' {
			continue
		}
		index++
		for index < len(data) && data[index] != '"' {
			if data[index] != '\\' {
				index++
				continue
			}
			if index+1 >= len(data) {
				return errors.New("unterminated JSON escape")
			}
			if data[index+1] != 'u' {
				index += 2
				continue
			}

			value, ok := decodeHex4(data, index+2)
			if !ok {
				return errors.New("invalid JSON Unicode escape")
			}
			switch {
			case value >= 0xd800 && value <= 0xdbff:
				if index+12 > len(data) || data[index+6] != '\\' || data[index+7] != 'u' {
					return errors.New("unpaired high surrogate in JSON string")
				}
				low, ok := decodeHex4(data, index+8)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errors.New("unpaired high surrogate in JSON string")
				}
				index += 12
			case value >= 0xdc00 && value <= 0xdfff:
				return errors.New("unpaired low surrogate in JSON string")
			default:
				index += 6
			}
		}
		if index >= len(data) {
			return errors.New("unterminated JSON string")
		}
	}
	return nil
}

func decodeHex4(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, digit := range data[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

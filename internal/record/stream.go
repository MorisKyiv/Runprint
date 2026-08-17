package record

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxSafeInteger int64 = 1<<53 - 1

// Count is a non-negative byte count with an interoperable JSON range. Its
// decoder accepts canonical integer tokens only: no sign, decimal, exponent,
// or leading zero.
type Count int64

func (c *Count) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || (len(data) > 1 && data[0] == '0') {
		return errors.New("byte count must be a canonical non-negative integer")
	}
	for _, value := range data {
		if value < '0' || value > '9' {
			return errors.New("byte count must be a canonical non-negative integer")
		}
	}
	value, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil || value > MaxSafeInteger {
		return fmt.Errorf("byte count exceeds maximum safe integer %d", MaxSafeInteger)
	}
	*c = Count(value)
	return nil
}

// Stream is a bounded, byte-accounted representation of stdout or stderr.
// Text is always readable; optional Base64 fields preserve exact bytes only
// when the corresponding chunk is not valid UTF-8.
type Stream struct {
	ReceivedBytes   Count  `json:"received_bytes"`
	CaptureComplete bool   `json:"capture_complete"`
	HeadBytes       Count  `json:"head_bytes"`
	HeadText        string `json:"head_text"`
	HeadRawBase64   string `json:"head_raw_base64,omitempty"`
	OmittedBytes    Count  `json:"omitted_bytes"`
	TailBytes       Count  `json:"tail_bytes"`
	TailText        string `json:"tail_text"`
	TailRawBase64   string `json:"tail_raw_base64,omitempty"`
}

type streamV2Wire struct {
	ReceivedBytes   *Count  `json:"received_bytes"`
	CaptureComplete *bool   `json:"capture_complete"`
	HeadBytes       *Count  `json:"head_bytes"`
	HeadText        *string `json:"head_text"`
	HeadRawBase64   *string `json:"head_raw_base64,omitempty"`
	OmittedBytes    *Count  `json:"omitted_bytes"`
	TailBytes       *Count  `json:"tail_bytes"`
	TailText        *string `json:"tail_text"`
	TailRawBase64   *string `json:"tail_raw_base64,omitempty"`
}

func (wire streamV2Wire) stream(name string) (Stream, error) {
	if wire.ReceivedBytes == nil || wire.CaptureComplete == nil ||
		wire.HeadBytes == nil || wire.HeadText == nil || wire.OmittedBytes == nil ||
		wire.TailBytes == nil || wire.TailText == nil {
		return Stream{}, fmt.Errorf("%s is missing a required field", name)
	}
	stream := Stream{
		ReceivedBytes:   *wire.ReceivedBytes,
		CaptureComplete: *wire.CaptureComplete,
		HeadBytes:       *wire.HeadBytes,
		HeadText:        *wire.HeadText,
		OmittedBytes:    *wire.OmittedBytes,
		TailBytes:       *wire.TailBytes,
		TailText:        *wire.TailText,
	}
	if wire.HeadRawBase64 != nil {
		if *wire.HeadRawBase64 == "" {
			return Stream{}, fmt.Errorf("%s.head_raw_base64 is present but empty", name)
		}
		stream.HeadRawBase64 = *wire.HeadRawBase64
	}
	if wire.TailRawBase64 != nil {
		if *wire.TailRawBase64 == "" {
			return Stream{}, fmt.Errorf("%s.tail_raw_base64 is present but empty", name)
		}
		stream.TailRawBase64 = *wire.TailRawBase64
	}
	if err := stream.Validate(name); err != nil {
		return Stream{}, err
	}
	return stream, nil
}

// NewStream constructs the canonical readable-plus-raw representation from
// exact captured bytes.
func NewStream(received int64, captureComplete bool, head []byte, omitted int64, tail []byte) Stream {
	headText, headRaw := encodeChunk(head)
	tailText, tailRaw := encodeChunk(tail)
	return Stream{
		ReceivedBytes:   Count(received),
		CaptureComplete: captureComplete,
		HeadBytes:       Count(len(head)),
		HeadText:        headText,
		HeadRawBase64:   headRaw,
		OmittedBytes:    Count(omitted),
		TailBytes:       Count(len(tail)),
		TailText:        tailText,
		TailRawBase64:   tailRaw,
	}
}

func encodeChunk(raw []byte) (string, string) {
	if utf8.Valid(raw) {
		return string(raw), ""
	}
	return strings.ToValidUTF8(string(raw), "\uFFFD"), base64.StdEncoding.EncodeToString(raw)
}

// Validate checks byte accounting, reader limits, and the exact relationship
// between readable text and optional raw bytes.
func (s Stream) Validate(name string) error {
	received := int64(s.ReceivedBytes)
	head := int64(s.HeadBytes)
	omitted := int64(s.OmittedBytes)
	tail := int64(s.TailBytes)
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"received_bytes", received},
		{"head_bytes", head},
		{"omitted_bytes", omitted},
		{"tail_bytes", tail},
	} {
		if field.value < 0 || field.value > MaxSafeInteger {
			return fmt.Errorf("%s.%s is outside 0..%d", name, field.name, MaxSafeInteger)
		}
	}
	if head > MaxCapturedBytes || tail > MaxCapturedBytes || head+tail > MaxCapturedBytes {
		return fmt.Errorf("%s retained chunks exceed %d-byte limit", name, MaxCapturedBytes)
	}
	if received != head+omitted+tail {
		return fmt.Errorf("%s byte counts do not add up", name)
	}
	if omitted == 0 && tail != 0 {
		return fmt.Errorf("%s tail must be empty when no bytes were omitted", name)
	}

	if err := validateChunk(name+".head", head, s.HeadText, s.HeadRawBase64); err != nil {
		return err
	}
	if err := validateChunk(name+".tail", tail, s.TailText, s.TailRawBase64); err != nil {
		return err
	}
	return nil
}

func validateChunk(name string, count int64, text, rawBase64 string) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("%s_text is not valid UTF-8", name)
	}
	if rawBase64 == "" {
		if int64(len(text)) != count {
			return fmt.Errorf("%s_text length does not match byte count", name)
		}
		return nil
	}
	if strings.ContainsAny(rawBase64, "\r\n") {
		return fmt.Errorf("%s_raw_base64: CR and LF are not allowed", name)
	}
	if len(rawBase64) != base64.StdEncoding.EncodedLen(int(count)) {
		return fmt.Errorf("%s_raw_base64 encoded length does not match byte count", name)
	}

	raw, err := decodeRawBase64(rawBase64)
	if err != nil {
		return fmt.Errorf("%s_raw_base64: %w", name, err)
	}
	if int64(len(raw)) != count {
		return fmt.Errorf("%s_raw_base64 length does not match byte count", name)
	}
	if utf8.Valid(raw) {
		return fmt.Errorf("%s_raw_base64 is redundant for valid UTF-8", name)
	}
	if want := strings.ToValidUTF8(string(raw), "\uFFFD"); text != want {
		return fmt.Errorf("%s_text does not match raw bytes", name)
	}
	return nil
}

func decodeRawBase64(encoded string) ([]byte, error) {
	if strings.ContainsAny(encoded, "\r\n") {
		return nil, errors.New("CR and LF are not allowed")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid canonical RFC 4648 Base64")
	}
	if base64.StdEncoding.EncodeToString(raw) != encoded {
		return nil, errors.New("invalid canonical RFC 4648 Base64")
	}
	return raw, nil
}

// HeadData returns the exact retained head bytes.
func (s Stream) HeadData() ([]byte, error) {
	return chunkData(s.HeadText, s.HeadRawBase64)
}

// TailData returns the exact retained tail bytes.
func (s Stream) TailData() ([]byte, error) {
	return chunkData(s.TailText, s.TailRawBase64)
}

func chunkData(text, rawBase64 string) ([]byte, error) {
	if rawBase64 == "" {
		return []byte(text), nil
	}
	return decodeRawBase64(rawBase64)
}

// HasInvalidUTF8 reports whether the safe text view contains replacements for
// retained raw bytes.
func (s Stream) HasInvalidUTF8() bool {
	return s.HeadRawBase64 != "" || s.TailRawBase64 != ""
}

// Truncated reports whether captured bytes are intentionally omitted.
func (s Stream) Truncated() bool {
	return s.OmittedBytes > 0
}

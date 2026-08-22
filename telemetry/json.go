package telemetry

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"unsafe"

	"google.golang.org/grpc/metadata"
)

// hexMap maps a 4-bit value to its lowercase hex digit.
const hexMap = "0123456789abcdef"

// keysPool recycles the []string slices used to sort map keys in appendJSONMapString,
// so that encoding a map does not allocate a new key slice on every call.
var keysPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, 16)
		return &s
	},
}

// stringToBytes returns a zero-copy []byte view of s, avoiding the copy that a plain []byte(s) conversion would make.
// The result must be treated as read-only: since Go strings are immutable, writing through it is undefined behavior.
func stringToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// appendEncodedString appends a double-quoted encoded string to b.
// It scans the input one byte at a time to bulk-copy safe chunks without allocating, escaping only the characters that need it.
func appendEncodedString(b []byte, s []byte) []byte {
	b = append(b, '"')
	start := 0

	for i := 0; i < len(s); {
		c := s[i]

		if c < 0x80 {
			// ASCII: only characters below 0x20 and the two special characters '"' and '\' need escaping.
			// Everything else is copied in bulk.
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}

			if start < i {
				b = append(b, s[start:i]...)
			}

			i++
			start = i

			switch c {
			case '"':
				b = append(b, `\"`...)
			case '\\':
				b = append(b, `\\`...)
			case '\n':
				b = append(b, `\n`...)
			case '\r':
				b = append(b, `\r`...)
			case '\t':
				b = append(b, `\t`...)
			default:
				// Remaining control characters → \u00XX
				b = append(b, `\u00`...)
				b = append(b, hexMap[c>>4], hexMap[c&0x0f])
			}
		} else {
			// For multi-byte UTF-8 sequence, skip over the full UTF-8 rune and copy it as-is.
			// Go strings are valid UTF-8, so no extra validation or re-encoding is needed.
			i++
			for i < len(s) && s[i]&0xC0 == 0x80 {
				i++
			}
		}
	}

	if start < len(s) {
		b = append(b, s[start:]...)
	}

	return append(b, '"')
}

// appendJSONPairs encodes pairs from kv as JSON fields and appends them to b.
// The keys can be of type string or []byte, and the values can be of any type.
//
// Each field is prefixed with a comma so the fragment can be spliced directly after any preceding field.
func appendJSONPairs(b []byte, kv []any) []byte {
	for i := 0; i+1 < len(kv); i += 2 {
		b = append(b, ',')
		b = appendEncodedJSONKey(b, kv[i])
		b = append(b, ':')
		b = appendEncodedJSONValue(b, kv[i+1])
	}

	return b
}

// appendEncodedJSONKey appends key to b as a quoted, escaped JSON field key.
func appendEncodedJSONKey(b []byte, key any) []byte {
	switch k := key.(type) {
	case string:
		return appendEncodedString(b, stringToBytes(k))
	case []byte:
		return appendEncodedString(b, k)
	default:
		return appendEncodedString(b, stringToBytes(fmt.Sprint(k)))
	}
}

// appendEncodedJSONValue appends val to b as a JSON field value.
// It uses type switches instead of reflection to handle scalar types,
// slices of scalar types, and maps keyed by string with scalar values.
// Everything else falls back to fmt.Sprintf("%+v", val).
//
// nil, bool, and numeric values are appended as unquoted JSON literals.
// Strings, slices, and maps are appended as quoted, escaped JSON strings.
func appendEncodedJSONValue(b []byte, val any) []byte {
	switch v := val.(type) {
	case nil:
		return append(b, "null"...)

	case bool:
		if v {
			return append(b, "true"...)
		} else {
			return append(b, "false"...)
		}

	case string:
		return appendEncodedString(b, stringToBytes(v))
	case []byte:
		return appendEncodedString(b, v)
	case error:
		return appendEncodedString(b, stringToBytes(v.Error()))

	// Signed integers
	case int:
		return strconv.AppendInt(b, int64(v), 10)
	case int8:
		return strconv.AppendInt(b, int64(v), 10)
	case int16:
		return strconv.AppendInt(b, int64(v), 10)
	case int32:
		return strconv.AppendInt(b, int64(v), 10)
	case int64:
		return strconv.AppendInt(b, v, 10)

	// Unsigned integers
	case uint:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint8:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint16:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint32:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint64:
		return strconv.AppendUint(b, v, 10)

	// Floating point
	case float32:
		return strconv.AppendFloat(b, float64(v), 'g', -1, 32)
	case float64:
		return strconv.AppendFloat(b, v, 'g', -1, 64)

	// Slices, maps, and anything else fall to a separate function.
	default:
		return appendEncodedComplexJSONValue(b, val)
	}
}

// appendEncodedComplexJSONValue handles the slice, map, and stringify-fallback cases of appendEncodedJSONValue.
// It is kept in its own function, with its own scratch buffer,
// because those cases call generic helpers (appendJSONSlice, appendJSONMapString);
// Go's escape analysis conservatively treats any local threaded into a generic call as heap-escaping,
// and applies that to every call of the enclosing function regardless of which branch runs.
// Splitting this out keeps that cost off appendEncodedJSONValue's scalar fast path,
// which is what most encoded values are, so that path stays genuinely allocation-free.
//
// Values that encode to more than 1024 bytes still allocate on the heap.
func appendEncodedComplexJSONValue(b []byte, val any) []byte {
	var buf [1024]byte

	switch v := val.(type) {
	// Slices of basic types
	case []any:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []bool:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []string:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []int:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []int8:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []int16:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []int32:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []int64:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []uint:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	// []uint8 is an alias for []byte
	case []uint16:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []uint32:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []uint64:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []float32:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))
	case []float64:
		return appendEncodedString(b, appendJSONSlice(buf[:0], v))

	// Maps of basic types
	case map[string]any:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]bool:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]string:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]int:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]int8:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]int16:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]int32:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]int64:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]uint:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]uint8:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]uint16:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]uint32:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]uint64:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]float32:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string]float64:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))

	// Maps of slices of basic typess
	case map[string][]any:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]bool:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]string:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]int:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]int8:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]int16:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]int32:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]int64:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]uint:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]uint8:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]uint16:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]uint32:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]uint64:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]float32:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case map[string][]float64:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))

	// Misc types
	case http.Header:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))
	case metadata.MD:
		return appendEncodedString(b, appendJSONMapString(buf[:0], v))

	// Anything else: stringify and quote
	default:
		return appendEncodedString(b, fmt.Appendf(buf[:0], "%+v", v))
	}
}

// -------------------------------------------------- Unescaped JSON Encoding --------------------------------------------------

// appendJSONKey appends key as a JSON field key without quotes or escaping.
func appendJSONKey(b []byte, key any) []byte {
	switch k := key.(type) {
	case string:
		return append(b, k...)
	case []byte:
		return append(b, k...)
	default:
		return fmt.Appendf(b, "%v", k)
	}
}

// appendJSONValue appends val as a JSON field value.
// It uses a type switch instead of reflection to handle scalar types,
// slices of scalar types, and string-keyed maps of scalar or slice values.
// Anything else is stringified with fmt.Appendf("%+v") and quoted.
//
// This is the driver function that kicks off calls to the recursive function below.
func appendJSONValue(b []byte, val any) []byte {
	return _appendJSONValue(b, val, true)
}

// This function is same as appendJSONValue, but this is the recursive version.
// The most outer call should not quote strings, while the inner recursive calls should do.
func _appendJSONValue(b []byte, val any, outer bool) []byte {
	switch v := val.(type) {
	case nil:
		if outer {
			return b
		} else {
			return append(b, "null"...)
		}

	case bool:
		if v {
			return append(b, "true"...)
		} else {
			return append(b, "false"...)
		}

	case string:
		if outer {
			return append(b, v...)
		} else {
			b = append(b, '"')
			b = append(b, v...)
			return append(b, '"')
		}
	case []byte:
		if outer {
			return append(b, v...)
		} else {
			b = append(b, '"')
			b = append(b, v...)
			return append(b, '"')
		}
	case error:
		if outer {
			return append(b, v.Error()...)
		} else {
			b = append(b, '"')
			b = append(b, v.Error()...)
			return append(b, '"')
		}

	// Signed integers
	case int:
		return strconv.AppendInt(b, int64(v), 10)
	case int8:
		return strconv.AppendInt(b, int64(v), 10)
	case int16:
		return strconv.AppendInt(b, int64(v), 10)
	case int32:
		return strconv.AppendInt(b, int64(v), 10)
	case int64:
		return strconv.AppendInt(b, v, 10)

	// Unsigned integers
	case uint:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint8:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint16:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint32:
		return strconv.AppendUint(b, uint64(v), 10)
	case uint64:
		return strconv.AppendUint(b, v, 10)

	// Floating point
	case float32:
		return strconv.AppendFloat(b, float64(v), 'g', -1, 32)
	case float64:
		return strconv.AppendFloat(b, v, 'g', -1, 64)

	// Slices of basic types
	case []any:
		return appendJSONSlice(b, v)
	case []bool:
		return appendJSONSlice(b, v)
	case []string:
		return appendJSONSlice(b, v)
	case []int:
		return appendJSONSlice(b, v)
	case []int8:
		return appendJSONSlice(b, v)
	case []int16:
		return appendJSONSlice(b, v)
	case []int32:
		return appendJSONSlice(b, v)
	case []int64:
		return appendJSONSlice(b, v)
	case []uint:
		return appendJSONSlice(b, v)
	// []uint8 is an alias for []byte
	case []uint16:
		return appendJSONSlice(b, v)
	case []uint32:
		return appendJSONSlice(b, v)
	case []uint64:
		return appendJSONSlice(b, v)
	case []float32:
		return appendJSONSlice(b, v)
	case []float64:
		return appendJSONSlice(b, v)

	// Maps of basic types
	case map[string]any:
		return appendJSONMapString(b, v)
	case map[string]bool:
		return appendJSONMapString(b, v)
	case map[string]string:
		return appendJSONMapString(b, v)
	case map[string]int:
		return appendJSONMapString(b, v)
	case map[string]int8:
		return appendJSONMapString(b, v)
	case map[string]int16:
		return appendJSONMapString(b, v)
	case map[string]int32:
		return appendJSONMapString(b, v)
	case map[string]int64:
		return appendJSONMapString(b, v)
	case map[string]uint:
		return appendJSONMapString(b, v)
	case map[string]uint8:
		return appendJSONMapString(b, v)
	case map[string]uint16:
		return appendJSONMapString(b, v)
	case map[string]uint32:
		return appendJSONMapString(b, v)
	case map[string]uint64:
		return appendJSONMapString(b, v)
	case map[string]float32:
		return appendJSONMapString(b, v)
	case map[string]float64:
		return appendJSONMapString(b, v)

	// Maps of slices of basic types
	case map[string][]any:
		return appendJSONMapString(b, v)
	case map[string][]bool:
		return appendJSONMapString(b, v)
	case map[string][]string:
		return appendJSONMapString(b, v)
	case map[string][]int:
		return appendJSONMapString(b, v)
	case map[string][]int8:
		return appendJSONMapString(b, v)
	case map[string][]int16:
		return appendJSONMapString(b, v)
	case map[string][]int32:
		return appendJSONMapString(b, v)
	case map[string][]int64:
		return appendJSONMapString(b, v)
	case map[string][]uint:
		return appendJSONMapString(b, v)
	case map[string][]uint8:
		return appendJSONMapString(b, v)
	case map[string][]uint16:
		return appendJSONMapString(b, v)
	case map[string][]uint32:
		return appendJSONMapString(b, v)
	case map[string][]uint64:
		return appendJSONMapString(b, v)
	case map[string][]float32:
		return appendJSONMapString(b, v)
	case map[string][]float64:
		return appendJSONMapString(b, v)

	// Misc types
	case http.Header:
		return appendJSONMapString(b, v)
	case metadata.MD:
		return appendJSONMapString(b, v)

	// Anything else: stringify and quote
	default:
		return fmt.Appendf(b, "%+v", v)
	}
}

// appendJSONSlice encodes s as a JSON array, appending each element with appendJSONValue.
func appendJSONSlice[T any](b []byte, s []T) []byte {
	b = append(b, '[')

	for i, v := range s {
		if i > 0 {
			b = append(b, ',')
		}
		b = _appendJSONValue(b, v, false)
	}

	return append(b, ']')
}

// appendJSONMapString encodes m as a JSON object with keys sorted.
func appendJSONMapString[T any](b []byte, m map[string]T) []byte {
	// Recycle the slice from the pool.
	keysPtr := keysPool.Get().(*[]string)
	keys := (*keysPtr)[:0]

	// Sort keys for deterministic, easily readable output.
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b = append(b, '{')

	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, k...)
		b = append(b, '"', ':')
		b = _appendJSONValue(b, m[k], false)
	}

	b = append(b, '}')

	// Save the slice to be reused.
	*keysPtr = keys
	keysPool.Put(keysPtr)

	return b
}

// getJSONKey renders key as an unquoted, unescaped JSON key string.
func getJSONKey(key any) string {
	// Stack-allocated scratch space so typical keys avoid a heap allocation.
	var buf [64]byte

	return string(appendJSONKey(buf[:0], key))
}

// getJSONValue renders val as an unquoted, unescaped JSON value string.
func getJSONValue(val any) string {
	// Stack-allocated scratch space so typical values avoid a heap allocation.
	var buf [960]byte

	return string(appendJSONValue(buf[:0], val))
}

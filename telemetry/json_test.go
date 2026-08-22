package telemetry

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/grpc/metadata"
)

func TestAppendEncodedString(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		s             []byte
		expectedBytes []byte
	}{
		{
			name:          "Empty",
			b:             []byte(`"msg":`),
			s:             []byte(""),
			expectedBytes: []byte(`"msg":""`),
		},
		{
			name:          "Simple",
			b:             []byte(`"msg":`),
			s:             []byte("ok"),
			expectedBytes: []byte(`"msg":"ok"`),
		},
		{
			name:          "WithQuotes",
			b:             []byte(`"msg":`),
			s:             []byte(`method "GET"`),
			expectedBytes: []byte(`"msg":"method \"GET\""`),
		},
		{
			name:          "WithSlashes",
			b:             []byte(`"msg":`),
			s:             []byte(`\n,\r`),
			expectedBytes: []byte(`"msg":"\\n,\\r"`),
		},
		{
			name:          "WithNewlines",
			b:             []byte(`"msg":`),
			s:             []byte("one\ntwo\nthree"),
			expectedBytes: []byte(`"msg":"one\ntwo\nthree"`),
		},
		{
			name:          "WithCarriageReturns",
			b:             []byte(`"msg":`),
			s:             []byte("first\rsecond\rthird"),
			expectedBytes: []byte(`"msg":"first\rsecond\rthird"`),
		},
		{
			name:          "WithTabs",
			b:             []byte(`"msg":`),
			s:             []byte("id\tname\tsize"),
			expectedBytes: []byte(`"msg":"id\tname\tsize"`),
		},
		{
			name:          "WithControlChars",
			b:             []byte(`"msg":`),
			s:             []byte("\u0000\u0002\u0003"),
			expectedBytes: []byte(`"msg":"\u0000\u0002\u0003"`),
		},
		{
			name:          "Unicode",
			b:             []byte(`"msg":`),
			s:             []byte("حافظ"),
			expectedBytes: []byte(`"msg":"حافظ"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := appendEncodedString(tc.b, tc.s)

			assert.Equal(t, tc.expectedBytes, b)
		})
	}
}

func TestAppendJSONPairs(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		kv            []any
		expectedBytes []byte
	}{
		{
			name: "StringKey",
			b:    []byte(`{"level":"debug"`),
			kv: []any{
				"environment", "test",
				"region", "local",
			},
			expectedBytes: []byte(`{"level":"debug","environment":"test","region":"local"`),
		},
		{
			name: "BytesKey",
			b:    []byte(`{"level":"debug"`),
			kv: []any{
				[]byte("environment"), "test",
				[]byte("region"), "local",
			},
			expectedBytes: []byte(`{"level":"debug","environment":"test","region":"local"`),
		},
		{
			name: "NonStandardKey",
			b:    []byte(`{"level":"debug"`),
			kv: []any{
				2, "two",
				true, "yes",
			},
			expectedBytes: []byte(`{"level":"debug","2":"two","true":"yes"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := appendJSONPairs(tc.b, tc.kv)

			assert.Equal(t, tc.expectedBytes, b)
		})
	}
}

func TestAppendEncodedJSONKey(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		key           any
		expectedBytes []byte
	}{
		{
			name:          "StringKey",
			b:             []byte(`{"level":"debug",`),
			key:           "environment",
			expectedBytes: []byte(`{"level":"debug","environment"`),
		},
		{
			name:          "BytesKey",
			b:             []byte(`{"level":"debug",`),
			key:           []byte("environment"),
			expectedBytes: []byte(`{"level":"debug","environment"`),
		},
		{
			name:          "NonStandardKey",
			b:             []byte(`{"level":"debug",`),
			key:           true,
			expectedBytes: []byte(`{"level":"debug","true"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := appendEncodedJSONKey(tc.b, tc.key)

			assert.Equal(t, tc.expectedBytes, b)
		})
	}
}

func TestAppendEncodedJSONValue(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		val           any
		expectedBytes []byte
	}{
		{
			name:          "Null",
			b:             []byte(`"msg":`),
			val:           nil,
			expectedBytes: []byte(`"msg":null`),
		},
		{
			name:          "False",
			b:             []byte(`"msg":`),
			val:           false,
			expectedBytes: []byte(`"msg":false`),
		},
		{
			name:          "True",
			b:             []byte(`"msg":`),
			val:           true,
			expectedBytes: []byte(`"msg":true`),
		},
		{
			name:          "String",
			b:             []byte(`"msg":`),
			val:           "ok",
			expectedBytes: []byte(`"msg":"ok"`),
		},
		{
			name:          "Bytes",
			b:             []byte(`"msg":`),
			val:           []byte{0x6f, 0x6b},
			expectedBytes: []byte(`"msg":"ok"`),
		},
		{
			name:          "Error",
			b:             []byte(`"msg":`),
			val:           errors.New("io error"),
			expectedBytes: []byte(`"msg":"io error"`),
		},
		{
			name:          "Int",
			b:             []byte(`"msg":`),
			val:           4214172627767632518,
			expectedBytes: []byte(`"msg":4214172627767632518`),
		},
		{
			name:          "Int8",
			b:             []byte(`"msg":`),
			val:           int8(120),
			expectedBytes: []byte(`"msg":120`),
		},
		{
			name:          "Int16",
			b:             []byte(`"msg":`),
			val:           int16(21270),
			expectedBytes: []byte(`"msg":21270`),
		},
		{
			name:          "Int32",
			b:             []byte(`"msg":`),
			val:           int32(2098013252),
			expectedBytes: []byte(`"msg":2098013252`),
		},
		{
			name:          "Int64",
			b:             []byte(`"msg":`),
			val:           int64(7988977323551769556),
			expectedBytes: []byte(`"msg":7988977323551769556`),
		},
		{
			name:          "Uint",
			b:             []byte(`"msg":`),
			val:           uint(12329674083326431419),
			expectedBytes: []byte(`"msg":12329674083326431419`),
		},
		{
			name:          "Uint8",
			b:             []byte(`"msg":`),
			val:           uint8(175),
			expectedBytes: []byte(`"msg":175`),
		},
		{
			name:          "Uint16",
			b:             []byte(`"msg":`),
			val:           uint16(60760),
			expectedBytes: []byte(`"msg":60760`),
		},
		{
			name:          "Uint32",
			b:             []byte(`"msg":`),
			val:           uint32(837254510),
			expectedBytes: []byte(`"msg":837254510`),
		},
		{
			name:          "Uint64",
			b:             []byte(`"msg":`),
			val:           uint64(17998475886476638010),
			expectedBytes: []byte(`"msg":17998475886476638010`),
		},
		{
			name:          "Float32",
			b:             []byte(`"msg":`),
			val:           float32(3.14159265),
			expectedBytes: []byte(`"msg":3.1415927`),
		},
		{
			name:          "Float64",
			b:             []byte(`"msg":`),
			val:           float64(3.1415926535897932),
			expectedBytes: []byte(`"msg":3.141592653589793`),
		},
		{
			name:          "EmptyAnySlice",
			b:             []byte(`"msg":`),
			val:           []any{},
			expectedBytes: []byte(`"msg":"[]"`),
		},
		{
			name:          "AnySlice",
			b:             []byte(`"msg":`),
			val:           []any{1, "two", true, nil},
			expectedBytes: []byte(`"msg":"[1,\"two\",true,null]"`),
		},
		{
			name:          "BoolSlice",
			b:             []byte(`"msg":`),
			val:           []bool{true, false},
			expectedBytes: []byte(`"msg":"[true,false]"`),
		},
		{
			name:          "StringSlice",
			b:             []byte(`"msg":`),
			val:           []string{"a", "b", "c"},
			expectedBytes: []byte(`"msg":"[\"a\",\"b\",\"c\"]"`),
		},
		{
			name:          "IntSlice",
			b:             []byte(`"msg":`),
			val:           []int{-64, -32, -16, -8, -4, -2, -1, 1, 2, 4, 8, 16, 32, 64},
			expectedBytes: []byte(`"msg":"[-64,-32,-16,-8,-4,-2,-1,1,2,4,8,16,32,64]"`),
		},
		{
			name:          "Int8Slice",
			b:             []byte(`"msg":`),
			val:           []int8{-128, 0, 127},
			expectedBytes: []byte(`"msg":"[-128,0,127]"`),
		},
		{
			name:          "Int16Slice",
			b:             []byte(`"msg":`),
			val:           []int16{-32768, 0, 32767},
			expectedBytes: []byte(`"msg":"[-32768,0,32767]"`),
		},
		{
			name:          "Int32Slice",
			b:             []byte(`"msg":`),
			val:           []int32{-2147483648, 0, 2147483647},
			expectedBytes: []byte(`"msg":"[-2147483648,0,2147483647]"`),
		},
		{
			name:          "Int64Slice",
			b:             []byte(`"msg":`),
			val:           []int64{-9223372036854775808, 0, 9223372036854775807},
			expectedBytes: []byte(`"msg":"[-9223372036854775808,0,9223372036854775807]"`),
		},
		{
			name:          "UintSlice",
			b:             []byte(`"msg":`),
			val:           []uint{1, 1, 2, 3, 5, 8, 13, 21, 34, 55},
			expectedBytes: []byte(`"msg":"[1,1,2,3,5,8,13,21,34,55]"`),
		},
		{
			name:          "Uint16Slice",
			b:             []byte(`"msg":`),
			val:           []uint16{0, 1, 65535},
			expectedBytes: []byte(`"msg":"[0,1,65535]"`),
		},
		{
			name:          "Uint32Slice",
			b:             []byte(`"msg":`),
			val:           []uint32{0, 1, 4294967295},
			expectedBytes: []byte(`"msg":"[0,1,4294967295]"`),
		},
		{
			name:          "Uint64Slice",
			b:             []byte(`"msg":`),
			val:           []uint64{0, 1, 18446744073709551615},
			expectedBytes: []byte(`"msg":"[0,1,18446744073709551615]"`),
		},
		{
			name:          "Float32Slice",
			b:             []byte(`"msg":`),
			val:           []float32{3.14159265, 2.71828182},
			expectedBytes: []byte(`"msg":"[3.1415927,2.7182817]"`),
		},
		{
			name:          "Float64Slice",
			b:             []byte(`"msg":`),
			val:           []float64{3.1415926535897932, 2.7182818284590452},
			expectedBytes: []byte(`"msg":"[3.141592653589793,2.718281828459045]"`),
		},
		{
			name:          "MapStringAny",
			b:             []byte(`"msg":`),
			val:           map[string]any{"b": 2, "a": "one"},
			expectedBytes: []byte(`"msg":"{\"a\":\"one\",\"b\":2}"`),
		},
		{
			name:          "MapStringBool",
			b:             []byte(`"msg":`),
			val:           map[string]bool{"b": true, "a": false},
			expectedBytes: []byte(`"msg":"{\"a\":false,\"b\":true}"`),
		},
		{
			name:          "MapStringString",
			b:             []byte(`"msg":`),
			val:           map[string]string{"b": "two", "a": "one"},
			expectedBytes: []byte(`"msg":"{\"a\":\"one\",\"b\":\"two\"}"`),
		},
		{
			name:          "MapStringInt",
			b:             []byte(`"msg":`),
			val:           map[string]int{"b": 1, "a": -1},
			expectedBytes: []byte(`"msg":"{\"a\":-1,\"b\":1}"`),
		},
		{
			name:          "MapStringInt8",
			b:             []byte(`"msg":`),
			val:           map[string]int8{"b": 127, "a": -128},
			expectedBytes: []byte(`"msg":"{\"a\":-128,\"b\":127}"`),
		},
		{
			name:          "MapStringInt16",
			b:             []byte(`"msg":`),
			val:           map[string]int16{"b": 32767, "a": -32768},
			expectedBytes: []byte(`"msg":"{\"a\":-32768,\"b\":32767}"`),
		},
		{
			name:          "MapStringInt32",
			b:             []byte(`"msg":`),
			val:           map[string]int32{"b": 2147483647, "a": -2147483648},
			expectedBytes: []byte(`"msg":"{\"a\":-2147483648,\"b\":2147483647}"`),
		},
		{
			name:          "MapStringInt64",
			b:             []byte(`"msg":`),
			val:           map[string]int64{"b": 9223372036854775807, "a": -9223372036854775808},
			expectedBytes: []byte(`"msg":"{\"a\":-9223372036854775808,\"b\":9223372036854775807}"`),
		},
		{
			name:          "MapStringUint",
			b:             []byte(`"msg":`),
			val:           map[string]uint{"b": 28, "a": 6},
			expectedBytes: []byte(`"msg":"{\"a\":6,\"b\":28}"`),
		},
		{
			name:          "MapStringUint8",
			b:             []byte(`"msg":`),
			val:           map[string]uint8{"b": 255, "a": 0},
			expectedBytes: []byte(`"msg":"{\"a\":0,\"b\":255}"`),
		},
		{
			name:          "MapStringUint16",
			b:             []byte(`"msg":`),
			val:           map[string]uint16{"b": 65535, "a": 0},
			expectedBytes: []byte(`"msg":"{\"a\":0,\"b\":65535}"`),
		},
		{
			name:          "MapStringUint32",
			b:             []byte(`"msg":`),
			val:           map[string]uint32{"b": 4294967295, "a": 0},
			expectedBytes: []byte(`"msg":"{\"a\":0,\"b\":4294967295}"`),
		},
		{
			name:          "MapStringUint64",
			b:             []byte(`"msg":`),
			val:           map[string]uint64{"b": 18446744073709551615, "a": 0},
			expectedBytes: []byte(`"msg":"{\"a\":0,\"b\":18446744073709551615}"`),
		},
		{
			name:          "MapStringFloat32",
			b:             []byte(`"msg":`),
			val:           map[string]float32{"b": 3.14159265, "a": 2.71828182},
			expectedBytes: []byte(`"msg":"{\"a\":2.7182817,\"b\":3.1415927}"`),
		},
		{
			name:          "MapStringFloat64",
			b:             []byte(`"msg":`),
			val:           map[string]float64{"b": 3.1415926535897932, "a": 2.7182818284590452},
			expectedBytes: []byte(`"msg":"{\"a\":2.718281828459045,\"b\":3.141592653589793}"`),
		},
		{
			name:          "MapStringAnySlice",
			b:             []byte(`"msg":`),
			val:           map[string][]any{"a": {1, "two", true, nil}},
			expectedBytes: []byte(`"msg":"{\"a\":[1,\"two\",true,null]}"`),
		},
		{
			name:          "MapStringBoolSlice",
			b:             []byte(`"msg":`),
			val:           map[string][]bool{"a": {true, false}},
			expectedBytes: []byte(`"msg":"{\"a\":[true,false]}"`),
		},
		{
			name:          "MapStringStringSlice",
			b:             []byte(`"msg":`),
			val:           map[string][]string{"a": {"x", "y"}},
			expectedBytes: []byte(`"msg":"{\"a\":[\"x\",\"y\"]}"`),
		},
		{
			name:          "MapStringIntSlice",
			b:             []byte(`"msg":`),
			val:           map[string][]int{"a": {-8, -4, -2, -1, 1, 2, 4, 8}},
			expectedBytes: []byte(`"msg":"{\"a\":[-8,-4,-2,-1,1,2,4,8]}"`),
		},
		{
			name:          "MapStringInt8Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]int8{"a": {-128, 0, 127}},
			expectedBytes: []byte(`"msg":"{\"a\":[-128,0,127]}"`),
		},
		{
			name:          "MapStringInt16Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]int16{"a": {-32768, 0, 32767}},
			expectedBytes: []byte(`"msg":"{\"a\":[-32768,0,32767]}"`),
		},
		{
			name:          "MapStringInt32Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]int32{"a": {-2147483648, 0, 2147483647}},
			expectedBytes: []byte(`"msg":"{\"a\":[-2147483648,0,2147483647]}"`),
		},
		{
			name:          "MapStringInt64Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]int64{"a": {-9223372036854775808, 0, 9223372036854775807}},
			expectedBytes: []byte(`"msg":"{\"a\":[-9223372036854775808,0,9223372036854775807]}"`),
		},
		{
			name:          "MapStringUintSlice",
			b:             []byte(`"msg":`),
			val:           map[string][]uint{"a": {1, 1, 2, 3, 5, 8}},
			expectedBytes: []byte(`"msg":"{\"a\":[1,1,2,3,5,8]}"`),
		},
		{
			name:          "MapStringUint8Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]uint8{"a": {0x6f, 0x6b}},
			expectedBytes: []byte(`"msg":"{\"a\":\"ok\"}"`),
		},
		{
			name:          "MapStringUint16Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]uint16{"a": {0, 1, 65535}},
			expectedBytes: []byte(`"msg":"{\"a\":[0,1,65535]}"`),
		},
		{
			name:          "MapStringUint32Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]uint32{"a": {0, 1, 4294967295}},
			expectedBytes: []byte(`"msg":"{\"a\":[0,1,4294967295]}"`),
		},
		{
			name:          "MapStringUint64Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]uint64{"a": {0, 1, 18446744073709551615}},
			expectedBytes: []byte(`"msg":"{\"a\":[0,1,18446744073709551615]}"`),
		},
		{
			name:          "MapStringFloat32Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]float32{"a": {3.14159265, 2.71828182}},
			expectedBytes: []byte(`"msg":"{\"a\":[3.1415927,2.7182817]}"`),
		},
		{
			name:          "MapStringFloat64Slice",
			b:             []byte(`"msg":`),
			val:           map[string][]float64{"a": {3.1415926535897932, 2.7182818284590452}},
			expectedBytes: []byte(`"msg":"{\"a\":[3.141592653589793,2.718281828459045]}"`),
		},
		{
			name: "HTTPHeader",
			b:    []byte(`"msg":`),
			val: http.Header{
				"Content-Type": {"application/json"},
				"User-Agent":   {"client-name/1.0"},
			},
			expectedBytes: []byte(`"msg":"{\"Content-Type\":[\"application/json\"],\"User-Agent\":[\"client-name/1.0\"]}"`),
		},
		{
			name: "GRPCMetadata",
			b:    []byte(`"msg":`),
			val: metadata.MD{
				"authorization": {"Bearer token"},
				"user-agent":    {"client-name/1.0"},
			},
			expectedBytes: []byte(`"msg":"{\"authorization\":[\"Bearer token\"],\"user-agent\":[\"client-name/1.0\"]}"`),
		},
		{
			name:          "Struct",
			b:             []byte(`"msg":`),
			val:           struct{ ID string }{ID: "1234-5678"},
			expectedBytes: []byte(`"msg":"{ID:1234-5678}"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := appendEncodedJSONValue(tc.b, tc.val)

			assert.Equal(t, tc.expectedBytes, b)
		})
	}
}

// -------------------------------------------------- Unescaped JSON Encoding --------------------------------------------------

func TestAppendJSONKey(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		key           any
		expectedBytes []byte
	}{
		{
			name:          "StringKey",
			b:             nil,
			key:           "environment",
			expectedBytes: []byte(`environment`),
		},
		{
			name:          "BytesKey",
			b:             nil,
			key:           []byte("environment"),
			expectedBytes: []byte(`environment`),
		},
		{
			name:          "NonStandardKey",
			b:             nil,
			key:           true,
			expectedBytes: []byte(`true`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := appendJSONKey(tc.b, tc.key)

			assert.Equal(t, tc.expectedBytes, b)
		})
	}
}

func TestAppendJSONValue(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		val           any
		expectedBytes []byte
	}{
		{
			name:          "Null",
			b:             nil,
			val:           nil,
			expectedBytes: nil,
		},
		{
			name:          "False",
			b:             nil,
			val:           false,
			expectedBytes: []byte("false"),
		},
		{
			name:          "True",
			b:             nil,
			val:           true,
			expectedBytes: []byte("true"),
		},
		{
			name:          "String",
			b:             nil,
			val:           "ok",
			expectedBytes: []byte("ok"),
		},
		{
			name:          "Bytes",
			b:             nil,
			val:           []byte{0x6f, 0x6b},
			expectedBytes: []byte("ok"),
		},
		{
			name:          "Error",
			b:             nil,
			val:           errors.New("io error"),
			expectedBytes: []byte("io error"),
		},
		{
			name:          "Int",
			b:             nil,
			val:           4214172627767632518,
			expectedBytes: []byte(`4214172627767632518`),
		},
		{
			name:          "Int8",
			b:             nil,
			val:           int8(120),
			expectedBytes: []byte(`120`),
		},
		{
			name:          "Int16",
			b:             nil,
			val:           int16(21270),
			expectedBytes: []byte(`21270`),
		},
		{
			name:          "Int32",
			b:             nil,
			val:           int32(2098013252),
			expectedBytes: []byte(`2098013252`),
		},
		{
			name:          "Int64",
			b:             nil,
			val:           int64(7988977323551769556),
			expectedBytes: []byte(`7988977323551769556`),
		},
		{
			name:          "Uint",
			b:             nil,
			val:           uint(12329674083326431419),
			expectedBytes: []byte(`12329674083326431419`),
		},
		{
			name:          "Uint8",
			b:             nil,
			val:           uint8(175),
			expectedBytes: []byte(`175`),
		},
		{
			name:          "Uint16",
			b:             nil,
			val:           uint16(60760),
			expectedBytes: []byte(`60760`),
		},
		{
			name:          "Uint32",
			b:             nil,
			val:           uint32(837254510),
			expectedBytes: []byte(`837254510`),
		},
		{
			name:          "Uint64",
			b:             nil,
			val:           uint64(17998475886476638010),
			expectedBytes: []byte(`17998475886476638010`),
		},
		{
			name:          "Float32",
			b:             nil,
			val:           float32(3.14159265),
			expectedBytes: []byte(`3.1415927`),
		},
		{
			name:          "Float64",
			b:             nil,
			val:           float64(3.1415926535897932),
			expectedBytes: []byte(`3.141592653589793`),
		},
		{
			name:          "EmptyAnySlice",
			b:             nil,
			val:           []any{},
			expectedBytes: []byte(`[]`),
		},
		{
			name:          "AnySlice",
			b:             nil,
			val:           []any{1, "two", true, nil},
			expectedBytes: []byte(`[1,"two",true,null]`),
		},
		{
			name:          "BoolSlice",
			b:             nil,
			val:           []bool{true, false},
			expectedBytes: []byte(`[true,false]`),
		},
		{
			name:          "StringSlice",
			b:             nil,
			val:           []string{"a", "b", "c"},
			expectedBytes: []byte(`["a","b","c"]`),
		},
		{
			name:          "IntSlice",
			b:             nil,
			val:           []int{-64, -32, -16, -8, -4, -2, -1, 1, 2, 4, 8, 16, 32, 64},
			expectedBytes: []byte(`[-64,-32,-16,-8,-4,-2,-1,1,2,4,8,16,32,64]`),
		},
		{
			name:          "Int8Slice",
			b:             nil,
			val:           []int8{-128, 0, 127},
			expectedBytes: []byte(`[-128,0,127]`),
		},
		{
			name:          "Int16Slice",
			b:             nil,
			val:           []int16{-32768, 0, 32767},
			expectedBytes: []byte(`[-32768,0,32767]`),
		},
		{
			name:          "Int32Slice",
			b:             nil,
			val:           []int32{-2147483648, 0, 2147483647},
			expectedBytes: []byte(`[-2147483648,0,2147483647]`),
		},
		{
			name:          "Int64Slice",
			b:             nil,
			val:           []int64{-9223372036854775808, 0, 9223372036854775807},
			expectedBytes: []byte(`[-9223372036854775808,0,9223372036854775807]`),
		},
		{
			name:          "UintSlice",
			b:             nil,
			val:           []uint{1, 1, 2, 3, 5, 8, 13, 21, 34, 55},
			expectedBytes: []byte(`[1,1,2,3,5,8,13,21,34,55]`),
		},
		{
			name:          "Uint16Slice",
			b:             nil,
			val:           []uint16{0, 1, 65535},
			expectedBytes: []byte(`[0,1,65535]`),
		},
		{
			name:          "Uint32Slice",
			b:             nil,
			val:           []uint32{0, 1, 4294967295},
			expectedBytes: []byte(`[0,1,4294967295]`),
		},
		{
			name:          "Uint64Slice",
			b:             nil,
			val:           []uint64{0, 1, 18446744073709551615},
			expectedBytes: []byte(`[0,1,18446744073709551615]`),
		},
		{
			name:          "Float32Slice",
			b:             nil,
			val:           []float32{3.14159265, 2.71828182},
			expectedBytes: []byte(`[3.1415927,2.7182817]`),
		},
		{
			name:          "Float64Slice",
			b:             nil,
			val:           []float64{3.1415926535897932, 2.7182818284590452},
			expectedBytes: []byte(`[3.141592653589793,2.718281828459045]`),
		},
		{
			name:          "MapStringAny",
			b:             nil,
			val:           map[string]any{"b": 2, "a": "one"},
			expectedBytes: []byte(`{"a":"one","b":2}`),
		},
		{
			name:          "MapStringBool",
			b:             nil,
			val:           map[string]bool{"b": true, "a": false},
			expectedBytes: []byte(`{"a":false,"b":true}`),
		},
		{
			name:          "MapStringString",
			b:             nil,
			val:           map[string]string{"b": "two", "a": "one"},
			expectedBytes: []byte(`{"a":"one","b":"two"}`),
		},
		{
			name:          "MapStringInt",
			b:             nil,
			val:           map[string]int{"b": 1, "a": -1},
			expectedBytes: []byte(`{"a":-1,"b":1}`),
		},
		{
			name:          "MapStringInt8",
			b:             nil,
			val:           map[string]int8{"b": 127, "a": -128},
			expectedBytes: []byte(`{"a":-128,"b":127}`),
		},
		{
			name:          "MapStringInt16",
			b:             nil,
			val:           map[string]int16{"b": 32767, "a": -32768},
			expectedBytes: []byte(`{"a":-32768,"b":32767}`),
		},
		{
			name:          "MapStringInt32",
			b:             nil,
			val:           map[string]int32{"b": 2147483647, "a": -2147483648},
			expectedBytes: []byte(`{"a":-2147483648,"b":2147483647}`),
		},
		{
			name:          "MapStringInt64",
			b:             nil,
			val:           map[string]int64{"b": 9223372036854775807, "a": -9223372036854775808},
			expectedBytes: []byte(`{"a":-9223372036854775808,"b":9223372036854775807}`),
		},
		{
			name:          "MapStringUint",
			b:             nil,
			val:           map[string]uint{"b": 28, "a": 6},
			expectedBytes: []byte(`{"a":6,"b":28}`),
		},
		{
			name:          "MapStringUint8",
			b:             nil,
			val:           map[string]uint8{"b": 255, "a": 0},
			expectedBytes: []byte(`{"a":0,"b":255}`),
		},
		{
			name:          "MapStringUint16",
			b:             nil,
			val:           map[string]uint16{"b": 65535, "a": 0},
			expectedBytes: []byte(`{"a":0,"b":65535}`),
		},
		{
			name:          "MapStringUint32",
			b:             nil,
			val:           map[string]uint32{"b": 4294967295, "a": 0},
			expectedBytes: []byte(`{"a":0,"b":4294967295}`),
		},
		{
			name:          "MapStringUint64",
			b:             nil,
			val:           map[string]uint64{"b": 18446744073709551615, "a": 0},
			expectedBytes: []byte(`{"a":0,"b":18446744073709551615}`),
		},
		{
			name:          "MapStringFloat32",
			b:             nil,
			val:           map[string]float32{"b": 3.14159265, "a": 2.71828182},
			expectedBytes: []byte(`{"a":2.7182817,"b":3.1415927}`),
		},
		{
			name:          "MapStringFloat64",
			b:             nil,
			val:           map[string]float64{"b": 3.1415926535897932, "a": 2.7182818284590452},
			expectedBytes: []byte(`{"a":2.718281828459045,"b":3.141592653589793}`),
		},
		{
			name:          "MapStringAnySlice",
			b:             nil,
			val:           map[string][]any{"a": {1, "two", true, nil}},
			expectedBytes: []byte(`{"a":[1,"two",true,null]}`),
		},
		{
			name:          "MapStringBoolSlice",
			b:             nil,
			val:           map[string][]bool{"a": {true, false}},
			expectedBytes: []byte(`{"a":[true,false]}`),
		},
		{
			name:          "MapStringStringSlice",
			b:             nil,
			val:           map[string][]string{"a": {"x", "y"}},
			expectedBytes: []byte(`{"a":["x","y"]}`),
		},
		{
			name:          "MapStringIntSlice",
			b:             nil,
			val:           map[string][]int{"a": {-8, -4, -2, -1, 1, 2, 4, 8}},
			expectedBytes: []byte(`{"a":[-8,-4,-2,-1,1,2,4,8]}`),
		},
		{
			name:          "MapStringInt8Slice",
			b:             nil,
			val:           map[string][]int8{"a": {-128, 0, 127}},
			expectedBytes: []byte(`{"a":[-128,0,127]}`),
		},
		{
			name:          "MapStringInt16Slice",
			b:             nil,
			val:           map[string][]int16{"a": {-32768, 0, 32767}},
			expectedBytes: []byte(`{"a":[-32768,0,32767]}`),
		},
		{
			name:          "MapStringInt32Slice",
			b:             nil,
			val:           map[string][]int32{"a": {-2147483648, 0, 2147483647}},
			expectedBytes: []byte(`{"a":[-2147483648,0,2147483647]}`),
		},
		{
			name:          "MapStringInt64Slice",
			b:             nil,
			val:           map[string][]int64{"a": {-9223372036854775808, 0, 9223372036854775807}},
			expectedBytes: []byte(`{"a":[-9223372036854775808,0,9223372036854775807]}`),
		},
		{
			name:          "MapStringUintSlice",
			b:             nil,
			val:           map[string][]uint{"a": {1, 1, 2, 3, 5, 8}},
			expectedBytes: []byte(`{"a":[1,1,2,3,5,8]}`),
		},
		{
			name:          "MapStringUint8Slice",
			b:             nil,
			val:           map[string][]uint8{"a": {0x6f, 0x6b}},
			expectedBytes: []byte(`{"a":"ok"}`),
		},
		{
			name:          "MapStringUint16Slice",
			b:             nil,
			val:           map[string][]uint16{"a": {0, 1, 65535}},
			expectedBytes: []byte(`{"a":[0,1,65535]}`),
		},
		{
			name:          "MapStringUint32Slice",
			b:             nil,
			val:           map[string][]uint32{"a": {0, 1, 4294967295}},
			expectedBytes: []byte(`{"a":[0,1,4294967295]}`),
		},
		{
			name:          "MapStringUint64Slice",
			b:             nil,
			val:           map[string][]uint64{"a": {0, 1, 18446744073709551615}},
			expectedBytes: []byte(`{"a":[0,1,18446744073709551615]}`),
		},
		{
			name:          "MapStringFloat32Slice",
			b:             nil,
			val:           map[string][]float32{"a": {3.14159265, 2.71828182}},
			expectedBytes: []byte(`{"a":[3.1415927,2.7182817]}`),
		},
		{
			name:          "MapStringFloat64Slice",
			b:             nil,
			val:           map[string][]float64{"a": {3.1415926535897932, 2.7182818284590452}},
			expectedBytes: []byte(`{"a":[3.141592653589793,2.718281828459045]}`),
		},
		{
			name: "HTTPHeader",
			b:    nil,
			val: http.Header{
				"Content-Type": {"application/json"},
				"User-Agent":   {"client-name/1.0"},
			},
			expectedBytes: []byte(`{"Content-Type":["application/json"],"User-Agent":["client-name/1.0"]}`),
		},
		{
			name: "GRPCMetadata",
			b:    nil,
			val: metadata.MD{
				"authorization": {"Bearer token"},
				"user-agent":    {"client-name/1.0"},
			},
			expectedBytes: []byte(`{"authorization":["Bearer token"],"user-agent":["client-name/1.0"]}`),
		},
		{
			name:          "Struct",
			b:             nil,
			val:           struct{ ID string }{ID: "1234-5678"},
			expectedBytes: []byte(`{ID:1234-5678}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := appendJSONValue(tc.b, tc.val)

			assert.Equal(t, tc.expectedBytes, b)
		})
	}
}

func TestGetJSONKey(t *testing.T) {
	tests := []struct {
		name           string
		key            any
		expectedString string
	}{
		{
			name:           "StringKey",
			key:            "environment",
			expectedString: `environment`,
		},
		{
			name:           "BytesKey",
			key:            []byte("environment"),
			expectedString: `environment`,
		},
		{
			name:           "NonStandardKey",
			key:            true,
			expectedString: `true`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedString, getJSONKey(tc.key))
		})
	}
}

func TestGetJSONValue(t *testing.T) {
	tests := []struct {
		name           string
		val            any
		expectedString string
	}{
		{
			name:           "Null",
			val:            nil,
			expectedString: "",
		},
		{
			name:           "False",
			val:            false,
			expectedString: "false",
		},
		{
			name:           "True",
			val:            true,
			expectedString: "true",
		},
		{
			name:           "String",
			val:            "ok",
			expectedString: "ok",
		},
		{
			name:           "Bytes",
			val:            []byte{0x6f, 0x6b},
			expectedString: "ok",
		},
		{
			name:           "Error",
			val:            errors.New("io error"),
			expectedString: "io error",
		},
		{
			name:           "Int",
			val:            4214172627767632518,
			expectedString: `4214172627767632518`,
		},
		{
			name:           "Int8",
			val:            int8(120),
			expectedString: `120`,
		},
		{
			name:           "Int16",
			val:            int16(21270),
			expectedString: `21270`,
		},
		{
			name:           "Int32",
			val:            int32(2098013252),
			expectedString: `2098013252`,
		},
		{
			name:           "Int64",
			val:            int64(7988977323551769556),
			expectedString: `7988977323551769556`,
		},
		{
			name:           "Uint",
			val:            uint(12329674083326431419),
			expectedString: `12329674083326431419`,
		},
		{
			name:           "Uint8",
			val:            uint8(175),
			expectedString: `175`,
		},
		{
			name:           "Uint16",
			val:            uint16(60760),
			expectedString: `60760`,
		},
		{
			name:           "Uint32",
			val:            uint32(837254510),
			expectedString: `837254510`,
		},
		{
			name:           "Uint64",
			val:            uint64(17998475886476638010),
			expectedString: `17998475886476638010`,
		},
		{
			name:           "Float32",
			val:            float32(3.14159265),
			expectedString: `3.1415927`,
		},
		{
			name:           "Float64",
			val:            float64(3.1415926535897932),
			expectedString: `3.141592653589793`,
		},
		{
			name:           "EmptyAnySlice",
			val:            []any{},
			expectedString: `[]`,
		},
		{
			name:           "AnySlice",
			val:            []any{1, "two", true, nil},
			expectedString: `[1,"two",true,null]`,
		},
		{
			name:           "BoolSlice",
			val:            []bool{true, false},
			expectedString: `[true,false]`,
		},
		{
			name:           "StringSlice",
			val:            []string{"a", "b", "c"},
			expectedString: `["a","b","c"]`,
		},
		{
			name:           "IntSlice",
			val:            []int{-64, -32, -16, -8, -4, -2, -1, 1, 2, 4, 8, 16, 32, 64},
			expectedString: `[-64,-32,-16,-8,-4,-2,-1,1,2,4,8,16,32,64]`,
		},
		{
			name:           "Int8Slice",
			val:            []int8{-128, 0, 127},
			expectedString: `[-128,0,127]`,
		},
		{
			name:           "Int16Slice",
			val:            []int16{-32768, 0, 32767},
			expectedString: `[-32768,0,32767]`,
		},
		{
			name:           "Int32Slice",
			val:            []int32{-2147483648, 0, 2147483647},
			expectedString: `[-2147483648,0,2147483647]`,
		},
		{
			name:           "Int64Slice",
			val:            []int64{-9223372036854775808, 0, 9223372036854775807},
			expectedString: `[-9223372036854775808,0,9223372036854775807]`,
		},
		{
			name:           "UintSlice",
			val:            []uint{1, 1, 2, 3, 5, 8, 13, 21, 34, 55},
			expectedString: `[1,1,2,3,5,8,13,21,34,55]`,
		},
		{
			name:           "Uint16Slice",
			val:            []uint16{0, 1, 65535},
			expectedString: `[0,1,65535]`,
		},
		{
			name:           "Uint32Slice",
			val:            []uint32{0, 1, 4294967295},
			expectedString: `[0,1,4294967295]`,
		},
		{
			name:           "Uint64Slice",
			val:            []uint64{0, 1, 18446744073709551615},
			expectedString: `[0,1,18446744073709551615]`,
		},
		{
			name:           "Float32Slice",
			val:            []float32{3.14159265, 2.71828182},
			expectedString: `[3.1415927,2.7182817]`,
		},
		{
			name:           "Float64Slice",
			val:            []float64{3.1415926535897932, 2.7182818284590452},
			expectedString: `[3.141592653589793,2.718281828459045]`,
		},
		{
			name:           "MapStringAny",
			val:            map[string]any{"b": 2, "a": "one"},
			expectedString: `{"a":"one","b":2}`,
		},
		{
			name:           "MapStringBool",
			val:            map[string]bool{"b": true, "a": false},
			expectedString: `{"a":false,"b":true}`,
		},
		{
			name:           "MapStringString",
			val:            map[string]string{"b": "two", "a": "one"},
			expectedString: `{"a":"one","b":"two"}`,
		},
		{
			name:           "MapStringInt",
			val:            map[string]int{"b": 1, "a": -1},
			expectedString: `{"a":-1,"b":1}`,
		},
		{
			name:           "MapStringInt8",
			val:            map[string]int8{"b": 127, "a": -128},
			expectedString: `{"a":-128,"b":127}`,
		},
		{
			name:           "MapStringInt16",
			val:            map[string]int16{"b": 32767, "a": -32768},
			expectedString: `{"a":-32768,"b":32767}`,
		},
		{
			name:           "MapStringInt32",
			val:            map[string]int32{"b": 2147483647, "a": -2147483648},
			expectedString: `{"a":-2147483648,"b":2147483647}`,
		},
		{
			name:           "MapStringInt64",
			val:            map[string]int64{"b": 9223372036854775807, "a": -9223372036854775808},
			expectedString: `{"a":-9223372036854775808,"b":9223372036854775807}`,
		},
		{
			name:           "MapStringUint",
			val:            map[string]uint{"b": 28, "a": 6},
			expectedString: `{"a":6,"b":28}`,
		},
		{
			name:           "MapStringUint8",
			val:            map[string]uint8{"b": 255, "a": 0},
			expectedString: `{"a":0,"b":255}`,
		},
		{
			name:           "MapStringUint16",
			val:            map[string]uint16{"b": 65535, "a": 0},
			expectedString: `{"a":0,"b":65535}`,
		},
		{
			name:           "MapStringUint32",
			val:            map[string]uint32{"b": 4294967295, "a": 0},
			expectedString: `{"a":0,"b":4294967295}`,
		},
		{
			name:           "MapStringUint64",
			val:            map[string]uint64{"b": 18446744073709551615, "a": 0},
			expectedString: `{"a":0,"b":18446744073709551615}`,
		},
		{
			name:           "MapStringFloat32",
			val:            map[string]float32{"b": 3.14159265, "a": 2.71828182},
			expectedString: `{"a":2.7182817,"b":3.1415927}`,
		},
		{
			name:           "MapStringFloat64",
			val:            map[string]float64{"b": 3.1415926535897932, "a": 2.7182818284590452},
			expectedString: `{"a":2.718281828459045,"b":3.141592653589793}`,
		},
		{
			name:           "MapStringAnySlice",
			val:            map[string][]any{"a": {1, "two", true, nil}},
			expectedString: `{"a":[1,"two",true,null]}`,
		},
		{
			name:           "MapStringBoolSlice",
			val:            map[string][]bool{"a": {true, false}},
			expectedString: `{"a":[true,false]}`,
		},
		{
			name:           "MapStringStringSlice",
			val:            map[string][]string{"a": {"x", "y"}},
			expectedString: `{"a":["x","y"]}`,
		},
		{
			name:           "MapStringIntSlice",
			val:            map[string][]int{"a": {-8, -4, -2, -1, 1, 2, 4, 8}},
			expectedString: `{"a":[-8,-4,-2,-1,1,2,4,8]}`,
		},
		{
			name:           "MapStringInt8Slice",
			val:            map[string][]int8{"a": {-128, 0, 127}},
			expectedString: `{"a":[-128,0,127]}`,
		},
		{
			name:           "MapStringInt16Slice",
			val:            map[string][]int16{"a": {-32768, 0, 32767}},
			expectedString: `{"a":[-32768,0,32767]}`,
		},
		{
			name:           "MapStringInt32Slice",
			val:            map[string][]int32{"a": {-2147483648, 0, 2147483647}},
			expectedString: `{"a":[-2147483648,0,2147483647]}`,
		},
		{
			name:           "MapStringInt64Slice",
			val:            map[string][]int64{"a": {-9223372036854775808, 0, 9223372036854775807}},
			expectedString: `{"a":[-9223372036854775808,0,9223372036854775807]}`,
		},
		{
			name:           "MapStringUintSlice",
			val:            map[string][]uint{"a": {1, 1, 2, 3, 5, 8}},
			expectedString: `{"a":[1,1,2,3,5,8]}`,
		},
		{
			name:           "MapStringUint8Slice",
			val:            map[string][]uint8{"a": {0x6f, 0x6b}},
			expectedString: `{"a":"ok"}`,
		},
		{
			name:           "MapStringUint16Slice",
			val:            map[string][]uint16{"a": {0, 1, 65535}},
			expectedString: `{"a":[0,1,65535]}`,
		},
		{
			name:           "MapStringUint32Slice",
			val:            map[string][]uint32{"a": {0, 1, 4294967295}},
			expectedString: `{"a":[0,1,4294967295]}`,
		},
		{
			name:           "MapStringUint64Slice",
			val:            map[string][]uint64{"a": {0, 1, 18446744073709551615}},
			expectedString: `{"a":[0,1,18446744073709551615]}`,
		},
		{
			name:           "MapStringFloat32Slice",
			val:            map[string][]float32{"a": {3.14159265, 2.71828182}},
			expectedString: `{"a":[3.1415927,2.7182817]}`,
		},
		{
			name:           "MapStringFloat64Slice",
			val:            map[string][]float64{"a": {3.1415926535897932, 2.7182818284590452}},
			expectedString: `{"a":[3.141592653589793,2.718281828459045]}`,
		},
		{
			name:           "Struct",
			val:            struct{ ID string }{ID: "1234-5678"},
			expectedString: `{ID:1234-5678}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedString, getJSONValue(tc.val))
		})
	}
}

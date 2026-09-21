package plugin_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

// kinds has one field of every kind Decode knows how to fill.
type kinds struct {
	I8    int8           `toml:"i8"`
	I     int            `toml:"i"`
	U8    uint8          `toml:"u8"`
	U     uint           `toml:"u"`
	F32   float32        `toml:"f32"`
	F64   float64        `toml:"f64"`
	S     string         `toml:"s"`
	B     bool           `toml:"b"`
	D     time.Duration  `toml:"d"`
	Addr  netip.Addr     `toml:"addr"`
	Ptr   *int           `toml:"ptr"`
	List  []string       `toml:"list"`
	Set   map[int]string `toml:"set"`
	Table map[string]int `toml:"table"`
	Sub   nested         `toml:"sub"`
	Ch    chan int       `toml:"ch"`
}

func TestDecodeAcceptsEveryIntegerTypeAParserMayProduce(t *testing.T) {
	cfg, err := plugin.Decode[kinds](map[string]any{
		"i8":  int32(-7),
		"i":   int(42),
		"u8":  int64(200),
		"u":   int(7),
		"f32": int32(3),
		"f64": int64(2),
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.I8 != -7 || cfg.I != 42 || cfg.U8 != 200 || cfg.U != 7 {
		t.Errorf("integers = %d %d %d %d, want -7 42 200 7", cfg.I8, cfg.I, cfg.U8, cfg.U)
	}
	if cfg.F32 != 3 || cfg.F64 != 2 {
		t.Errorf("floats = %v %v, want 3 2 (from integers)", cfg.F32, cfg.F64)
	}
}

func TestDecodeAssignsPointerAndBasicValues(t *testing.T) {
	cfg, err := plugin.Decode[kinds](map[string]any{
		"ptr": int64(5),
		"f64": 1.5,
		"s":   "text",
		"b":   true,
		"d":   "1m30s",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Ptr == nil || *cfg.Ptr != 5 {
		t.Errorf("Ptr = %v, want a pointer to 5", cfg.Ptr)
	}
	if cfg.F64 != 1.5 || cfg.S != "text" || !cfg.B {
		t.Errorf("basic values = %v %q %v, want 1.5 text true", cfg.F64, cfg.S, cfg.B)
	}
	if cfg.D != 90*time.Second {
		t.Errorf("D = %v, want 1m30s", cfg.D)
	}
}

func TestDecodeMapWithNumericKeysIsRejected(t *testing.T) {
	_, err := plugin.Decode[kinds](map[string]any{"set": map[string]any{"1": "one"}})
	if err == nil || !strings.Contains(err.Error(), "map keys must be strings") {
		t.Fatalf("error = %v, want a complaint about map keys", err)
	}
}

func TestDecodeRejectsBadValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value any
		want  string
	}{
		{"nil value", "s", nil, `parameter "s" has no value`},
		{"string for int", "i", "1", `cannot assign string to int`},
		{"float for int", "i", 1.5, `cannot assign float64 to int`},
		{"int for string", "s", int64(1), `cannot assign int64 to string`},
		{"string for bool", "b", "true", `cannot assign string to bool`},
		{"string for float", "f64", "1.5", `cannot assign string to float64`},
		{"string for uint", "u", "1", `cannot assign string to uint`},
		{"int8 overflow", "i8", int64(128), "out of range for int8"},
		{"uint negative", "u8", int64(-1), "out of range for uint8"},
		{"uint8 overflow", "u8", int64(256), "out of range for uint8"},
		{"uint negative into wide type", "u", int64(-1), "out of range for uint"},
		{"float32 overflow", "f32", 1e39, "out of range for float32"},
		{"duration not text", "d", int64(10), `cannot assign int64 to time.Duration`},
		{"duration unparsable", "d", "soon", `parameter "d": time: invalid duration`},
		{"address not text", "addr", int64(1), `cannot assign int64 to netip.Addr`},
		{"address unparsable", "addr", "not-an-ip", `parameter "addr": ParseAddr`},
		{"list not an array", "list", "a", `cannot assign string to []string`},
		{"list element of wrong type", "list", []any{"a", int64(2)}, `parameter "list[1]"`},
		{"table not a table", "table", "a", `cannot assign string to map[string]int`},
		{"table value of wrong type", "table", map[string]any{"a": "x"}, `parameter "table.a"`},
		{"sub not a table", "sub", "a", `cannot assign string to plugin_test.nested`},
		{"unsupported kind", "ch", int64(1), "unsupported type chan int"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := plugin.Decode[kinds](map[string]any{tt.key: tt.value})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// defaultsOfEveryKind takes every value from a `default` tag.
type defaultsOfEveryKind struct {
	I8   int8          `toml:"i8" default:"-5"`
	U16  uint16        `toml:"u16" default:"65535"`
	F32  float32       `toml:"f32" default:"0.25"`
	B    bool          `toml:"b" default:"true"`
	S    string        `toml:"s" default:"text"`
	D    time.Duration `toml:"d" default:"2m"`
	Addr netip.Addr    `toml:"addr" default:"2001:db8::1"`
	Ptr  *int          `toml:"ptr" default:"9"`
}

func TestDecodeParsesDefaultOfEveryKind(t *testing.T) {
	cfg, err := plugin.Decode[defaultsOfEveryKind](nil)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.I8 != -5 || cfg.U16 != 65535 || cfg.F32 != 0.25 || !cfg.B || cfg.S != "text" {
		t.Errorf("defaults = %+v, want -5 65535 0.25 true text", cfg)
	}
	if cfg.D != 2*time.Minute {
		t.Errorf("D = %v, want 2m", cfg.D)
	}
	if cfg.Addr != netip.MustParseAddr("2001:db8::1") {
		t.Errorf("Addr = %v, want 2001:db8::1", cfg.Addr)
	}
	if cfg.Ptr == nil || *cfg.Ptr != 9 {
		t.Errorf("Ptr = %v, want a pointer to 9", cfg.Ptr)
	}
}

type (
	badIntDefault struct {
		N int8 `toml:"n" default:"300"`
	}
	badUintDefault struct {
		N uint `toml:"n" default:"-1"`
	}
	badFloatDefault struct {
		N float64 `toml:"n" default:"many"`
	}
	badBoolDefault struct {
		N bool `toml:"n" default:"maybe"`
	}
	badDurationDefault struct {
		N time.Duration `toml:"n" default:"soon"`
	}
	badAddrDefault struct {
		N netip.Addr `toml:"n" default:"not-an-ip"`
	}
	badPointerDefault struct {
		N *int `toml:"n" default:"x"`
	}
	sliceDefault struct {
		N []string `toml:"n" default:"a,b"`
	}
)

func TestDecodeReportsBrokenDefaultOfEveryKind(t *testing.T) {
	tests := []struct {
		name   string
		decode func() error
		want   string
	}{
		{"int", decodeError[badIntDefault], "value out of range"},
		{"uint", decodeError[badUintDefault], "invalid syntax"},
		{"float", decodeError[badFloatDefault], "invalid syntax"},
		{"bool", decodeError[badBoolDefault], "invalid syntax"},
		{"duration", decodeError[badDurationDefault], "invalid duration"},
		{"address", decodeError[badAddrDefault], "ParseAddr"},
		{"pointer", decodeError[badPointerDefault], "invalid syntax"},
		{"slice", decodeError[sliceDefault], "default values are not supported for type []string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.decode()
			if err == nil || !strings.Contains(err.Error(), `invalid default for parameter "n"`) ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want an invalid default for %q containing %q", err, "n", tt.want)
			}
		})
	}
}

func decodeError[C any]() error {
	_, err := plugin.Decode[C](nil)

	return err
}

// deep holds nested values of the types deepCopy walks through.
type deep struct {
	Values []any `toml:"values"`
}

func TestDecodeCopiesEveryShapeOfNestedValue(t *testing.T) {
	number := 7
	params := map[string]any{"values": []any{
		&number,
		(*int)(nil),
		[]any(nil),
		map[string]any(nil),
		nil,
		[]string{"a"},
	}}

	cfg, err := plugin.Decode[deep](params)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// The pointer is copied, not shared with the caller.
	number = 8
	if got := *cfg.Values[0].(*int); got != 7 {
		t.Errorf("pointer element = %d, want 7 (a copy of the caller's value)", got)
	}
	if got := cfg.Values[1].(*int); got != nil {
		t.Errorf("nil pointer element = %v, want nil", got)
	}
	if got := cfg.Values[2].([]any); got != nil {
		t.Errorf("nil slice element = %v, want nil", got)
	}
	if got := cfg.Values[3].(map[string]any); got != nil {
		t.Errorf("nil map element = %v, want nil", got)
	}
	if cfg.Values[4] != nil {
		t.Errorf("nil interface element = %v, want nil", cfg.Values[4])
	}
	if got := cfg.Values[5].([]string); len(got) != 1 || got[0] != "a" {
		t.Errorf("string slice element = %v, want [a]", got)
	}
}

type anyField struct {
	Value any `toml:"value"`
}

func TestDecodeRejectsFieldOfTypeAny(t *testing.T) {
	_, err := plugin.Decode[anyField](map[string]any{"value": "text"})
	if err == nil || !strings.Contains(err.Error(), "unsupported type interface {}") {
		t.Fatalf("error = %v, want an unsupported type error", err)
	}
}

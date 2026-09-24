package paramspec

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

type base struct {
	Timeout time.Duration `toml:"timeout" default:"10s" doc:"Request timeout" example:"30s"`
}

type sample struct {
	base
	Token    string     `toml:"token,required"`
	Region   string     // named after the field
	Addr     netip.Addr `toml:"addr"`
	Skipped  string     `toml:"-"`
	hidden   string
	Options  struct{ A string }  `toml:"options"`
	Optional *struct{ B string } `toml:"optional"`
}

func TestFieldsListsParametersInDeclarationOrder(t *testing.T) {
	_ = sample{hidden: "unexported fields are never parameters"}

	fields, err := Fields(reflect.TypeFor[sample]())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}

	var keys []string
	for _, f := range fields {
		keys = append(keys, f.Key)
	}

	want := []string{"timeout", "token", "region", "addr", "options", "optional"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v (embedded promoted, tagged - and unexported skipped)", keys, want)
	}
}

func TestFieldsReadsTags(t *testing.T) {
	fields, err := Fields(reflect.TypeFor[sample]())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}

	timeout := fields[0]
	if !timeout.HasDefault || timeout.Default != "10s" || timeout.Doc != "Request timeout" ||
		!timeout.HasExample || timeout.Example != "30s" || timeout.Required {
		t.Errorf("timeout = %+v, want default 10s, doc, example 30s, not required", timeout)
	}
	if timeout.Type != reflect.TypeFor[time.Duration]() {
		t.Errorf("timeout type = %v, want time.Duration", timeout.Type)
	}
	if !reflect.DeepEqual(timeout.Index, []int{0, 0}) {
		t.Errorf("timeout index = %v, want the path through the embedded struct [0 0]", timeout.Index)
	}

	if token := fields[1]; !token.Required || token.HasDefault || token.HasExample {
		t.Errorf("token = %+v, want required with no default and no example", token)
	}
}

func TestFieldsReadsOptionsAfterTheName(t *testing.T) {
	type withOptions struct {
		Named   string `toml:"named,required"`
		Unnamed string `toml:",required"`
		Hidden  string `toml:"hidden,secret"`
		Both    string `toml:"both,required,secret"`
		Plain   string `toml:"plain"`
	}

	fields, err := Fields(reflect.TypeFor[withOptions]())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}

	type flags struct{ required, secret bool }

	got := make(map[string]flags, len(fields))
	for _, f := range fields {
		got[f.Key] = flags{f.Required, f.Secret}
	}

	want := map[string]flags{
		"named":   {required: true},
		"unnamed": {required: true},
		"hidden":  {secret: true},
		"both":    {required: true, secret: true},
		"plain":   {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flags by key = %v, want %v (an option follows the name, which may be left to the field name)", got, want)
	}
}

func TestFieldsRejectsAMalformedOption(t *testing.T) {
	type unknown struct {
		Token string `toml:"token,requird"`
	}
	type repeated struct {
		Token string `toml:"token,required,required"`
	}
	type embedded struct {
		base `toml:",required"`
	}

	tests := map[string]struct {
		config reflect.Type
		want   []string
	}{
		"unknown":  {reflect.TypeFor[unknown](), []string{`unknown option "requird"`, "field Token"}},
		"repeated": {reflect.TypeFor[repeated](), []string{`option "required" is repeated`, "field Token"}},
		"embedded": {reflect.TypeFor[embedded](), []string{"embedded struct", "field base"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Fields(tt.config)
			if err == nil {
				t.Fatal("Fields succeeded, want an error")
			}

			for _, fragment := range tt.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error = %v, want it to contain %q", err, fragment)
				}
			}
		})
	}
}

func TestFieldsRejectsDuplicatesIgnoringCase(t *testing.T) {
	type duplicate struct {
		base
		A string `toml:"name"`
		B string `toml:"NAME"`
	}

	_, err := Fields(reflect.TypeFor[duplicate]())
	if err == nil || !strings.Contains(err.Error(), `both take parameter "NAME"`) ||
		!strings.Contains(err.Error(), "A and B") {
		t.Errorf("error = %v, want it to name both Go fields and the parameter", err)
	}
}

func TestFieldsReportsGoPathOfPromotedField(t *testing.T) {
	type clash struct {
		base
		Timeout string `toml:"timeout"`
	}

	_, err := Fields(reflect.TypeFor[clash]())
	if err == nil || !strings.Contains(err.Error(), "base.Timeout and Timeout") {
		t.Errorf("error = %v, want the path of the promoted field", err)
	}
}

func TestParsesText(t *testing.T) {
	if !ParsesText(reflect.TypeFor[netip.Addr]()) {
		t.Error("netip.Addr is written as one value, want it to parse from text")
	}
	if ParsesText(reflect.TypeFor[base]()) {
		t.Error("a plain struct is a table, want it not to")
	}
}

package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

// envReference matches, in order of preference: the escape "$${", a file
// reference "${file:PATH}", a valid environment reference "${NAME}" and a
// stray "${" that starts a malformed one.
var envReference = regexp.MustCompile(`\$(?:\$\{|\{(?:file:[^}]+\}|([A-Za-z_]\w*)\})?)`)

// resolvePlugin merges the definition named by override["ref"] with the
// override and expands environment references. kind is "retriever" or
// "provider"; where locates the reference in the instance for error messages.
// Neither the definition nor the override is modified, and the result shares
// no memory with either.
func resolvePlugin(kind, where string, pool map[string]map[string]any, override map[string]any) (Plugin, []error) {
	ref, _ := override["ref"].(string)
	if ref == "" {
		return Plugin{}, []error{fmt.Errorf(`%s: "ref" is required and must be a string`, where)}
	}

	definition, ok := pool[ref]
	if !ok {
		return Plugin{}, []error{fmt.Errorf("%s: ref %q is not defined (%s)", where, ref, definedNames(kind, pool))}
	}

	if _, ok := override["type"]; ok {
		return Plugin{}, []error{fmt.Errorf(`%s: %s %q: "type" cannot be overridden`, where, kind, ref)}
	}

	merged := make(map[string]any, len(definition)+len(override))

	for _, source := range []map[string]any{definition, override} {
		for key, value := range source {
			if key != "type" && key != "ref" {
				merged[key] = value
			}
		}
	}

	params, paramErrs := expandMap(merged, "")
	if len(paramErrs) > 0 {
		errs := make([]error, len(paramErrs))
		for i, err := range paramErrs {
			errs[i] = fmt.Errorf("%s %q: %w", kind, ref, err)
		}

		return Plugin{}, errs
	}

	typ, _ := definition["type"].(string)

	return Plugin{Ref: ref, Type: typ, Params: params}, nil
}

// expandMap returns a deep copy of params with environment references
// expanded in string values, or every expansion error found. prefix is the
// dotted path of params, empty at the top level.
func expandMap(params map[string]any, prefix string) (map[string]any, []error) {
	expanded := make(map[string]any, len(params))

	var errs []error

	for _, key := range slices.Sorted(maps.Keys(params)) {
		value, valueErrs := expandValue(params[key], joinPath(prefix, key))
		errs = append(errs, valueErrs...)
		expanded[key] = value
	}

	return expanded, errs
}

func expandValue(value any, path string) (any, []error) {
	switch v := value.(type) {
	case string:
		expanded, err := expandString(v)
		if err != nil {
			return nil, []error{fmt.Errorf("parameter %q: %w", path, err)}
		}

		return expanded, nil
	case map[string]any:
		return expandMap(v, path)
	case []any:
		var errs []error

		expanded := make([]any, len(v))
		for i, item := range v {
			elem, elemErrs := expandValue(item, fmt.Sprintf("%s[%d]", path, i))
			errs = append(errs, elemErrs...)
			expanded[i] = elem
		}

		return expanded, errs
	case []map[string]any:
		var errs []error

		expanded := make([]map[string]any, len(v))
		for i, item := range v {
			elem, elemErrs := expandMap(item, fmt.Sprintf("%s[%d]", path, i))
			errs = append(errs, elemErrs...)
			expanded[i] = elem
		}

		return expanded, errs
	default:
		return value, nil
	}
}

// expandString replaces "${NAME}" with the value of the environment
// variable, "${file:PATH}" with the trimmed contents of the file (for
// Docker/Kubernetes secrets mounted as files), and "$${" with a literal
// "${". An unset variable, an unreadable file and a malformed reference are
// errors; a variable set to the empty string is not.
func expandString(s string) (string, error) {
	var problem error

	expanded := envReference.ReplaceAllStringFunc(s, func(match string) string {
		switch {
		case match == "$${":
			return "${"
		case match == "${":
			if problem == nil {
				problem = errMalformedReference
			}

			return match
		case strings.HasPrefix(match, "${file:"):
			path := match[len("${file:") : len(match)-1]

			value, err := readSecretFile(path)
			if err != nil && problem == nil {
				problem = err
			}

			return value
		}

		name := match[2 : len(match)-1]

		value, ok := os.LookupEnv(name)
		if !ok && problem == nil {
			problem = fmt.Errorf("environment variable %q is not set", name)
		}

		return value
	})

	if problem != nil {
		return "", problem
	}

	return expanded, nil
}

// readSecretFile reads a secret from path, trimming a single trailing
// newline (with an optional preceding carriage return) so a file written
// with "echo" does not carry it into the parameter value.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading secret file %q: %w", path, err)
	}

	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
}

var errMalformedReference = errors.New(`malformed reference: expected "${NAME}" or "${file:PATH}", write "$${" for a literal "${"`)

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}

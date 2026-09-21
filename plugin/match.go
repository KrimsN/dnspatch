package plugin

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/KrimsN/dnspatch/internal/paramspec"
)

// paramMatch is one configurable field together with the value given for it,
// and the name under which the value was written.
type paramMatch struct {
	spec  paramspec.Field
	name  string
	value any
	given bool
}

// matchParams pairs every field with its value, matching parameter names
// case-insensitively. It reports all the problems of one parameter block at
// once: parameters matching no field, and required fields left unset.
func matchParams(params map[string]any, specs []paramspec.Field, prefix string) ([]paramMatch, error) {
	matches := make([]paramMatch, len(specs))
	byKey := make(map[string]int, len(specs))

	for i, spec := range specs {
		matches[i] = paramMatch{spec: spec}
		byKey[strings.ToLower(spec.Key)] = i
	}

	var errs []error

	for name, value := range params {
		i, ok := byKey[strings.ToLower(name)]
		if !ok {
			errs = append(errs, unknownParamError(name, specs, prefix))
			continue
		}

		if matches[i].given {
			errs = append(errs, conflictingParamsError(matches[i].name, name, prefix+matches[i].spec.Key))
			continue
		}

		matches[i].name, matches[i].value, matches[i].given = name, value, true
	}

	for _, match := range matches {
		if match.spec.Required && !match.given {
			errs = append(errs, fmt.Errorf("parameter %q is required", prefix+match.spec.Key))
		}
	}

	sortErrors(errs)

	return matches, errors.Join(errs...)
}

// conflictingParamsError reports two parameter names that differ only in
// case and therefore address the same field. The names are ordered, since
// the one seen first depends on map iteration order.
func conflictingParamsError(first, second, key string) error {
	if second < first {
		first, second = second, first
	}

	return fmt.Errorf("parameters %q and %q both set %q", first, second, key)
}

// unknownParamError explains an unknown parameter, pointing at the closest
// known name when there is one. The prefix qualifies names in the message
// without taking part in the comparison.
func unknownParamError(name string, specs []paramspec.Field, prefix string) error {
	if closest, ok := closestKey(name, specs); ok {
		return fmt.Errorf("unknown parameter %q (did you mean %q?)", prefix+name, prefix+closest)
	}

	name = prefix + name

	keys := make([]string, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.Key)
	}
	slices.Sort(keys)

	if len(keys) == 0 {
		return fmt.Errorf("unknown parameter %q (this plugin takes no parameters)", name)
	}

	return fmt.Errorf("unknown parameter %q (known parameters: %s)", name, strings.Join(keys, ", "))
}

// closestKey returns the known parameter closest to name, if one is close
// enough to be a plausible typo.
func closestKey(name string, specs []paramspec.Field) (string, bool) {
	lowered := strings.ToLower(name)
	best, bestDistance := "", 0

	for _, spec := range specs {
		d := editDistance(lowered, strings.ToLower(spec.Key))
		if best == "" || d < bestDistance {
			best, bestDistance = spec.Key, d
		}
	}

	limit := min((len(lowered)+2)/3, 3)
	if best == "" || bestDistance == 0 || bestDistance > limit {
		return "", false
	}

	return best, true
}

// editDistance returns the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// sortErrors orders errors by message, so that iterating a map does not make
// the output vary between runs.
func sortErrors(errs []error) {
	slices.SortFunc(errs, func(a, b error) int {
		return strings.Compare(a.Error(), b.Error())
	})
}

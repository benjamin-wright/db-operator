package k8s_generic

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type BasicType interface {
	string | int | int64 | bool | map[string]string
}

func GetEncodedProperty(u *unstructured.Unstructured, args ...string) (string, error) {
	value, err := GetProperty[string](u, args...)
	if err != nil {
		return "", fmt.Errorf("failed to find property: %+v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("failed to decode property %s: %+v", strings.Join(args, "."), err)
	}

	return string(decoded), nil
}

func GetProperty[T BasicType](u *unstructured.Unstructured, args ...string) (T, error) {
	var current interface{} = u.Object
	var empty T

	for _, arg := range args {
		switch c := current.(type) {
		case map[string]interface{}:
			value, ok := c[arg]
			if !ok {
				return empty, fmt.Errorf("object doesn't have property %s in %s", arg, strings.Join(args, "."))
			}

			current = value
		case []interface{}:
			idx, err := strconv.ParseInt(arg, 10, 32)
			if err != nil {
				return empty, fmt.Errorf("attempt to index array with non-numeric key %s in %s", arg, strings.Join(args, "."))
			}

			if idx < 0 || idx >= int64(len(c)) {
				return empty, fmt.Errorf("attempt to index array with non-numeric key %s in %s", arg, strings.Join(args, "."))
			}

			current = c[idx]
		default:
			return empty, fmt.Errorf("property %s was not an object in %s", arg, strings.Join(args, "."))
		}
	}

	value, ok := current.(T)
	if !ok {
		return empty, fmt.Errorf("property %s is not the right type: expected %T but got %T", strings.Join(args, "."), value, current)
	}

	return value, nil
}

func Merge(maps ...map[string]interface{}) map[string]interface{} {
	output := map[string]interface{}{}

	for _, m := range maps {
		for key, value := range m {
			output[key] = value
		}
	}

	return output
}

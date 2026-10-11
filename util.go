package main

import (
	"sort"
)

func sortedKeys(obj map[string]any) (keys []string) {
	keys = make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return
}

func stringProp(obj map[string]any, key string) (value string, ok bool) {
	v, ok := obj[key]
	if !ok {
		return "", false
	}
	value, ok = v.(string)
	return
}

func hasDirective(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, hasRef := m["$ref"]
	_, hasInline := m["$inline"]
	_, hasMerge := m["$merge"]
	return hasRef || hasInline || hasMerge
}

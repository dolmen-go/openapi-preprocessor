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

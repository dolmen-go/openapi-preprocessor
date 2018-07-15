package main

import "sort"

func sortedKeys(obj map[string]interface{}) (keys []string) {
	keys = make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return
}

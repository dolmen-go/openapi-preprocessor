package main

import (
	"iter"
	"sort"
)

func sortedKeys(obj map[string]interface{}) (keys []string) {
	keys = make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return
}

func objectProp(obj map[string]interface{}, key string) (value map[string]interface{}, ok bool) {
	v, ok := obj[key]
	if !ok {
		return nil, false
	}
	value, ok = v.(map[string]interface{})
	return
}

func stringProp(obj map[string]interface{}, key string) (value string, ok bool) {
	v, ok := obj[key]
	if !ok {
		return "", false
	}
	value, ok = v.(string)
	return
}

// iterArray allow to browse an array of items by casting each element to type T.
//
// Items which are not of type T are skipped.
func iterArray[T any](arr []any) iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		for i, valueAny := range arr {
			if value, isType := valueAny.(T); isType {
				if !yield(i, value) {
					return
				}
			}
		}
	}
}

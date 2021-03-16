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

func objectProp(obj map[string]interface{}, key string) (value map[string]interface{}, ok bool) {
	v, ok := obj[key]
	if ok {
		return nil, false
	}
	value, ok = v.(map[string]interface{})
	return
}

func stringProp(obj map[string]interface{}, key string) (value string, ok bool) {
	v, ok := obj[key]
	if ok {
		return "", false
	}
	value, ok = v.(string)
	return
}

package main

import (
	"iter"
	"sort"
	"strconv"

	"github.com/dolmen-go/jsonptr"
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

// iterArrayPtr allow to browse an array of items by casting each element to type T.
//
// The index of the array is given as a JSON Pointer.
// Items which are not of type T are skipped.
func iterArrayPtr[T any](ptr string, arr []any) iter.Seq2[string, T] {
	return func(yield func(string, T) bool) {
		for i, valueAny := range arr {
			if value, isType := valueAny.(T); isType {
				if !yield(ptr+"/"+strconv.Itoa(i), value) {
					return
				}
			}
		}
	}
}

func seq2Noop[K any, V any](yield func(K, V) bool) {
	return
}

func iterObjectPtr[T any](ptr string, obj map[string]any) iter.Seq2[string, T] {
	if len(obj) == 0 {
		return seq2Noop[string, T]
	}

	return func(yield func(string, T) bool) {
		for key, valueAny := range obj {
			if value, isType := valueAny.(T); isType {
				if !yield(ptr+"/"+jsonptr.EscapeString(key), value) {
					return
				}
			}
		}
	}
}

func propertyPtr[T any](ptr string, doc map[string]any, prop string) (string, T, bool) {
	valueAny, hasProp := doc[prop]
	if !hasProp {
		var v T
		return "", v, false
	}
	value, ok := valueAny.(T)
	if !ok {
		var v T
		return "", v, false
	}
	return ptr + "/" + jsonptr.EscapeString(prop), value, true
}

func iterPropertyPtr[T any](ptr string, doc map[string]any, prop string) iter.Seq2[string, T] {
	valueAny, hasProp := doc[prop]
	if !hasProp {
		return seq2Noop[string, T]
	}
	value, ok := valueAny.(T)
	if !ok {
		return seq2Noop[string, T]
	}
	return func(yield func(string, T) bool) {
		yield(ptr+"/"+jsonptr.EscapeString(prop), value)
	}
}

func iterPaths(root any) iter.Seq2[string, map[string]any] {
	/*
		return func(yield func(string, map[string]any)) {
			for _, paths := range iterPropertyPtr(``, root.(map[string]any), `paths`) {
				for ptr, path := range iterObjectPtr[map[string]any](`/paths`, paths) {
					if !yield(pth, path) {
						return
					}
				}
			}
		}
	*/
	pathsAny, ok := (root.(map[string]any))[`paths`]
	if !ok {
		return seq2Noop[string, map[string]any]
	}
	paths, ok := pathsAny.(map[string]any)
	if !ok {
		return seq2Noop[string, map[string]any]
	}
	return iterObjectPtr[map[string]any](`/paths`, paths)
}

var methods = [...]bool{
	'g' + 'e' + 't': true, // get
	'p' + 'u' + 't': true, // put
	'p' + 'o' + 's': true, // post
	'd' + 'e' + 'l': true, // delete
	'o' + 'p' + 't': true, // options
	'h' + 'e' + 'a': true, // head
	'p' + 'a' + 't': true, // patch
	't' + 'r' + 'a': true, // trace
}

func iterOperations(root any) iter.Seq2[string, map[string]any] {
	return func(yield func(string, map[string]any) bool) {
		for ptr, spec := range iterPaths(root) {
			for k, opAny := range spec {
				if len(k) < 3 {
					continue
				}
				kk := int(k[0]) + int(k[1]) + int(k[2])
				if kk < len(methods) && methods[kk] {
					if op, ok := opAny.(map[string]any); ok {
						if !yield(ptr+"/"+jsonptr.EscapeString(k), op) {
							return
						}
					}
				}
			}
		}
	}
}

func iterSecurity(ptr string, doc map[string]any) iter.Seq2[string, map[string]any] {
	return func(yield func(string, map[string]any) bool) {
		if opSec, hasSec := doc["security"].([]any); hasSec {
			/*
				for p, req := range iterArrayPtr[map[string]any](ptr, opSec) {
					if !yield(p, req) {
						return
					}
				}
			*/
			iterArrayPtr[map[string]any](ptr+"/security", opSec)(yield)
		}
	}
}

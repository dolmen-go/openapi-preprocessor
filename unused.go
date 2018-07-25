package main

import (
	"errors"
	"fmt"

	"github.com/dolmen-go/jsonptr"
)

func removeEmptyObject(rdoc *interface{}, pointer string) {
	ptr, err := jsonptr.Parse(pointer)
	if err != nil {
		panic(fmt.Errorf("%s: %v", pointer, err))
	}
	parentRaw, err := ptr[:len(ptr)-1].In(*rdoc)
	if err != nil {
		return
	}
	parent, isObj := parentRaw.(map[string]interface{})
	if !isObj || len(parent) == 0 {
		return
	}
	key := ptr[len(ptr)-1]
	obj, isObj := parent[key].(map[string]interface{})
	if !isObj || len(obj) > 0 {
		return
	}
	delete(obj, key)
}

func CleanUnused(rdoc *interface{}) error {

	root, isObj := (*rdoc).(map[string]interface{})
	if !isObj {
		return errors.New("root is not an object")
	}

	if paths, hasPaths := root["paths"]; hasPaths {
		unused := make(map[string]bool)
		for _, p := range []string{`/components/schemas`, `/components/parameters`, `/components/responses`} {
			compRaw, err := jsonptr.Get(root, p)
			if err != nil {
				continue
			}
			comp, compObj := compRaw.(map[string]interface{})
			if !compObj {
				continue
			}
			for k := range comp {
				unused[p+"/"+jsonptr.EscapeString(k)] = true
			}
		}
		var visitor func(ptr jsonptr.Pointer, ref string) (string, error)
		visitor = func(ptr jsonptr.Pointer, ref string) (string, error) {
			if ref[0] != '#' {
				return ref, fmt.Errorf("%s: unexpected $ref %q", ptr, ref)
			}
			link := ref[1:]
			// log.Println(ptr, "=>", link)
			if unused[link] {
				// log.Println("seen", link)
				delete(unused, link)
			}
			targetPtr, err := jsonptr.Parse(link)
			if err != nil {
				return ref, err
			}
			targetPtr.Grow(20)
			target, err := targetPtr.In(root)
			if err != nil { // should not happen if
				return ref, err
			}
			return ref, visitRefs(target, targetPtr, visitor)
		}

		err := visitRefs(paths, append(make(jsonptr.Pointer, 0, 50), "paths"), visitor)
		if err != nil {
			return err
		}

		for p := range unused {
			// log.Printf("%s: unused", p)
			_, err = jsonptr.Delete(rdoc, p)
			if err != nil {
				panic("This should not happen")
			}
		}
	}

	removeEmptyObject(rdoc, `/components/schemas`)
	removeEmptyObject(rdoc, `/components/parameters`)
	removeEmptyObject(rdoc, `/components/responses`)
	removeEmptyObject(rdoc, `/components`)

	return nil
}

package main

import (
	"errors"
	"fmt"
	"strings"

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
	if isObj && len(obj) == 0 {
		delete(parent, key)
	}
}

// CleanUnused checks references to global components and removes unreferenced components.
//
// This is an important step after ExpandRefs as some components referenced through $inline
// or $merge have been injected and are not needed anymore.
func CleanUnused(rdoc *interface{}) error {

	root, isObj := (*rdoc).(map[string]interface{})
	if !isObj {
		return errors.New("root is not an object")
	}

	if paths, hasPaths := root["paths"]; hasPaths {

		var components []string

		if _, hasSwaggerVersion := stringProp(root, "swagger"); hasSwaggerVersion {
			// TODO check version value (must be "2.0")
			components = []string{`/definitions`, `/parameters`, `/responses`}
		}

		if _, hasOpenAPIVersion := stringProp(root, "openapi"); hasOpenAPIVersion {
			components = []string{
				`/components/schemas`,
				`/components/parameters`,
				`/components/responses`,
				`/components/examples`,
				`/components/requestBodies`,
				`/components/headers`,
				`/components/securitySchemes`,
				`/components/links`,
				`/components/callbacks`,
			}
		}

		// Collect all defined components.
		unused := make(map[string]bool)
		for _, p := range components {
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

		visited := make(map[string]bool)
		var visitor func(ptr jsonptr.Pointer, ref string) (string, error)
		visitor = func(ptr jsonptr.Pointer, ref string) (string, error) {
			// Assumptions (ensured by ExpandRefs):
			// - all $ref have been resolved to internal links
			// - all $ref have been checked to not be circular
			if ref[0] != '#' {
				return ref, fmt.Errorf("%s: unexpected $ref %q", ptr, ref)
			}
			link := ref[1:]
			// log.Println(ptr, "=>", link)
			if visited[link] {
				return ref, nil
			}
			if unused[link] {
				// log.Println("seen", link)
				delete(unused, link)
			}
			visited[link] = true
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

		// Visit paths to detect components which are used
		err := visitRefs(paths, append(make(jsonptr.Pointer, 0, 50), "paths"), visitor)
		if err != nil {
			return err
		}

	nextUnused:
		for p := range unused {
			// Look for deep references in unused schemas
			prefix := p + "/"
			for v := range visited {
				if strings.HasPrefix(v, prefix) {
					continue nextUnused
				}
			}
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
	removeEmptyObject(rdoc, `/definitions`)
	removeEmptyObject(rdoc, `/parameters`)
	removeEmptyObject(rdoc, `/responses`)

	return nil
}

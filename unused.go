package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dolmen-go/jsonptr"
)

// visitSecurity calls mark with the name of each security scheme used by the
// security requirements found in doc (located at ptr), at the root of the
// document or in operations (see [isSecurityPtr]).
func visitSecurity(doc any, ptr jsonptr.Pointer, mark func(name string)) {
	switch doc := doc.(type) {
	case map[string]any:
		for k, v := range doc {
			visitSecurity(v, append(ptr[:len(ptr):len(ptr)], k), mark)
		}
	case []any:
		if isSecurityPtr(ptr) {
			for _, req := range doc {
				if req, isObj := req.(map[string]any); isObj {
					for name := range req {
						mark(name)
					}
				}
			}
			return
		}
		for i, v := range doc {
			visitSecurity(v, append(ptr[:len(ptr):len(ptr)], strconv.Itoa(i)), mark)
		}
	}
}

func removeEmptyObject(rdoc *any, pointer string) {
	ptr, err := jsonptr.Parse(pointer)
	if err != nil {
		panic(fmt.Errorf("%s: %v", pointer, err))
	}
	parentRaw, err := ptr[:len(ptr)-1].In(*rdoc)
	if err != nil {
		return
	}
	parent, isObj := parentRaw.(map[string]any)
	if !isObj || len(parent) == 0 {
		return
	}
	key := ptr[len(ptr)-1]
	obj, isObj := parent[key].(map[string]any)
	if isObj && len(obj) == 0 {
		delete(parent, key)
	}
}

// CleanUnused checks references to global components and removes unreferenced components.
//
// This is an important step after ExpandRefs as some components referenced through $inline
// or $merge have been injected and are not needed anymore.
func CleanUnused(rdoc *any) error {

	root, isObj := (*rdoc).(map[string]any)
	if !isObj {
		return errors.New("root is not an object")
	}

	var components []string
	// Keys of the root which are containers of components
	containers := map[string]bool{"components": true}
	// Container of security schemes, which are named in security requirements
	var securitySchemes string

	if _, hasSwaggerVersion := stringProp(root, "swagger"); hasSwaggerVersion {
		// TODO check version value (must be "2.0")
		components = []string{`/definitions`, `/parameters`, `/responses`, `/securityDefinitions`}
		containers = swaggerComponents
		securitySchemes = `/securityDefinitions`
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
			`/components/pathItems`, // OpenAPI 3.1
		}
		securitySchemes = `/components/securitySchemes`
	}

	_, hasPaths := root["paths"]
	_, hasWebhooks := root["webhooks"] // OpenAPI 3.1
	if hasPaths || hasWebhooks {

		// Collect all defined components.
		unused := make(map[string]bool)
		for _, p := range components {
			compRaw, err := jsonptr.Get(root, p)
			if err != nil {
				continue
			}
			comp, compObj := compRaw.(map[string]any)
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
			// A link to a part of a component keeps the whole component (see
			// below): visit the whole component to find its own links
			for _, p := range components {
				if rest, isInside := strings.CutPrefix(link, p+"/"); isInside {
					if i := strings.IndexByte(rest, '/'); i >= 0 {
						link = link[:len(p)+1+i]
					}
					break
				}
			}
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
				return ref, fmt.Errorf("%v -> %v: %v", ptr, link, err)
			}
			return ref, visitRefs(target, targetPtr, visitor)
		}

		// The parts of the document which are not containers of components
		// (/paths, /webhooks, /security...) are used
		var used []string
		for _, k := range sortedKeys(root) {
			if !containers[k] {
				used = append(used, "/"+jsonptr.EscapeString(k))
			}
		}

		// Visit the used parts to detect components which are used
		for _, p := range used {
			err := visitRefs(root[p[1:]], jsonptr.MustParse(p), visitor)
			if err != nil {
				return err
			}
		}

		// Security schemes are not linked with $ref, but named in security
		// requirements, in the used parts and in the used components.
		// https://spec.openapis.org/oas/v3.1.1.html#security-requirement-object
		if securitySchemes != "" {
			markUsed := func(name string) {
				delete(unused, securitySchemes+"/"+jsonptr.EscapeString(name))
			}
			for _, p := range slices.Concat(used, slices.Collect(maps.Keys(visited))) {
				ptr := jsonptr.MustParse(p)
				node, err := ptr.In(root)
				if err != nil {
					continue
				}
				visitSecurity(node, ptr, markUsed)
			}
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
			if _, err := jsonptr.Delete(rdoc, p); err != nil {
				panic("This should not happen")
			}
		}
	}

	// Remove the containers of components left empty
	for _, p := range components {
		removeEmptyObject(rdoc, p)
	}
	removeEmptyObject(rdoc, `/components`)

	return nil
}

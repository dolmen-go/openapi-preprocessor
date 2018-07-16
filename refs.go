package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/mohae/deepcopy"

	"github.com/dolmen-go/jsonptr"
)

type setter func(interface{})

// visitRefs visits $ref and allows to change them.
func visitRefs(root interface{}, ptr jsonptr.Pointer, visitor func(jsonptr.Pointer, string) (string, error)) (err error) {
	switch root := root.(type) {
	case map[string]interface{}:
		ptr = ptr.Copy()
		for k, v := range root {
			ptr.Property(k)
			if k == "$ref" {
				if str, isString := v.(string); isString {
					root[k], err = visitor(ptr, str)
					if err != nil {
						return
					}
				}
			}
			ptr.Up()
		}
	case []string:
		ptr = ptr.Copy()
		for i, v := range root {
			ptr.Index(i)
			visitRefs(v, ptr, visitor)
			ptr.Up()
		}
	}
	return
}

type refResolver struct {
	rootURL  string
	docs     map[string]*interface{}
	visited  map[string]bool
	inject   map[string]string
	inlining bool
}

func (resolver *refResolver) resolve(link string, relativeTo *url.URL) (interface{}, setter, *url.URL, error) {
	//log.Println(link, relativeTo)
	u, err := url.Parse(link)
	if err != nil {
		return nil, nil, nil, err
	}

	if !u.IsAbs() {
		u = relativeTo.ResolveReference(u)
	}
	//log.Println("=>", u)

	if *u == *relativeTo {
		return nil, nil, nil, errors.New("circular link")
	}

	var rootURLStr string
	if u.Fragment != "" {
		rootURL := *u
		rootURL.Fragment = ""
		rootURLStr = rootURL.String()
	} else {
		rootURLStr = u.String()
	}

	rdoc, loaded := resolver.docs[rootURLStr]
	if !loaded {
		//log.Println("Loading", &rootURL)
		doc, err := loadURL(u)
		if err != nil {
			return nil, nil, nil, err
		}
		var itf interface{}
		itf = doc
		rdoc = &itf
		resolver.docs[rootURLStr] = rdoc
	}

	if u.Fragment == "" {
		return *rdoc, func(newDoc interface{}) {
			*rdoc = newDoc
		}, u, nil
	}

	ptr, err := jsonptr.Parse(u.Fragment)
	if err != nil {
		return nil, nil, nil, err
	}

	// FIXME we could reduce the number of evals of JSON pointers...

	frag, err := ptr.In(*rdoc)
	if err != nil {
		// If the can't be immediately resolved, this may be because
		// of a $inline in the way

		p := jsonptr.Pointer{}
		for {
			doc, err := p.In(*rdoc)
			if err != nil {
				// Failed to resolve the fragment
				return nil, nil, nil, err
			}
			if obj, isMap := doc.(map[string]interface{}); isMap {
				if _, isInline := obj["$inline"]; isInline {
					//log.Printf("%#v", obj)
					u2 := *u
					u2.Fragment = p.String()

					err := resolver.expand(obj, func(newDoc interface{}) {
						p.Set(rdoc, newDoc)
					}, &u2)
					if err != nil {
						return nil, nil, nil, err
					}

				}
			}
			if len(p) == len(ptr) {
				break
			}
			p = ptr[:len(p)+1]
		}

		frag, _ = ptr.In(*rdoc)
	}

	return frag, func(newDoc interface{}) {
		ptr.Set(rdoc, newDoc)
	}, u, nil
}

func (resolver *refResolver) expand(doc interface{}, set setter, docURL *url.URL) error {
	if docURL == nil {
		panic("nil URL")
	}
	u := docURL.String()
	//log.Println(u, docURL.Fragment)
	if resolver.visited[u] {
		return nil
	}
	if !resolver.inlining {
		resolver.visited[u] = true
	}

	if doc, isSlice := doc.([]interface{}); isSlice {
		u2 := *docURL
		for i, v := range doc {
			switch v.(type) {
			case []interface{}, map[string]interface{}:
				u2.Fragment = fmt.Sprintf("%s/%d", docURL.Fragment, i)
				err := resolver.expand(v, func(newDoc interface{}) {
					doc[i] = newDoc
				}, &u2)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	obj, isObject := doc.(map[string]interface{})
	if !isObject || obj == nil {
		return nil
	}

	if ref, isRef := obj["$ref"]; isRef {
		//log.Printf("$ref: %s => %s", docURL, ref)
		link, isString := ref.(string)
		if !isString {
			return fmt.Errorf("%s: $ref must be a string", docURL)
		}
		if len(obj) > 1 {
			return fmt.Errorf("%s: $ref must be alone (use $merge instead)", docURL)
		}

		_, u, err := resolver.resolveAndExpand(link, docURL)
		if err != nil {
			return err
		}

		if resolver.inject != nil {
			fragment := docURL.Fragment
			u2 := *u
			u2.Fragment = ""
			u2str := u2.String()
			if u2str != resolver.rootURL {
				if src := resolver.inject[fragment]; src != "" && src != u2str {
					return fmt.Errorf("import fragment %s from both %s and %s", link, src, u2str)
				}
				resolver.inject[fragment] = u2str
			}
		}

		return nil
	}

	// An extension to build an object from mixed local data and
	// imported data
	if link, isMerge := obj["$merge"]; isMerge {
		var links []string
		switch link := link.(type) {
		case string:
			if len(obj) == 1 {
				return fmt.Errorf("%s: merging with nothing?", docURL)
			}
			links = []string{link}
		case []interface{}:
			links = make([]string, len(link))
			for i, v := range link {
				l, isString := v.(string)
				if !isString {
					return fmt.Errorf("%s/%d: must be a string", docURL, i)
				}
				links[i] = l
			}
		default:
			return fmt.Errorf("%s: must be a string or array of strings", docURL)
		}
		delete(obj, "$merge")

		s := docURL.String()
		delete(resolver.visited, s)
		err := resolver.expand(doc, func(newDoc interface{}) {
			doc = newDoc
			set(newDoc)
		}, docURL)
		resolver.visited[s] = true
		if err != nil {
			return err
		}

		for i, link := range links {
			target, _, err := resolver.resolveAndExpand(link, docURL)
			if err != nil {
				return err
			}

			objTarget, isObj := target.(map[string]interface{})
			if !isObj {
				if len(links) == 1 {
					return fmt.Errorf("%s/$merge: link must point to object", docURL)
				}
				return fmt.Errorf("%s/$merge/%d: link must point to object", docURL, i)
			}
			for k, v := range objTarget {
				if _, exists := obj[k]; exists {
					// TODO warn
					continue
				}
				obj[k] = v
			}
		}

		return nil
	}

	if link, isInline := obj["$inline"]; isInline {

		inlining := resolver.inlining
		resolver.inlining = true

		target, _, err := resolver.resolveAndExpand(link.(string), docURL)
		if err != nil {
			return err
		}
		resolver.inlining = inlining

		target = deepcopy.Copy(target)
		set(target)

		//log.Printf("xxx %#v", target)

		if len(obj) > 1 {
			switch target.(type) {
			case map[string]interface{}:
				for _, k := range sortedKeys(obj) {
					if len(k) > 0 && k[0] == '$' { // skip $inline
						continue
					}
					v := obj[k]
					//log.Println(k)
					u := *docURL
					u.Fragment = u.Fragment + "/" + jsonptr.EscapeString(k)
					err = resolver.expand(v, func(newDoc interface{}) {
						v = newDoc
					}, &u)
					if err != nil {
						return err
					}
					if err := jsonptr.Set(&target, "/"+k, v); err != nil {
						return fmt.Errorf("%s/%s: %v", docURL, k, err)
					}
				}
			case []interface{}:
				// TODO
				return fmt.Errorf("%s: inlining of array not yet implemented", docURL)
			default:
				return fmt.Errorf("%s: inlined scalar value can't be patched", docURL)
			}
		}

		return nil
	}

	for _, k := range sortedKeys(obj) {
		//log.Println("Key:", k)
		u := *docURL
		u.Fragment += "/" + jsonptr.EscapeString(k)
		err := resolver.expand(obj[k], func(newDoc interface{}) {
			obj[k] = newDoc
		}, &u)
		if err != nil {
			return fmt.Errorf("%s: %v", &u, err)
		}
	}

	return nil
}

func (resolver *refResolver) resolveAndExpand(link string, relativeTo *url.URL) (interface{}, *url.URL, error) {
	target, setTarget, u, err := resolver.resolve(link, relativeTo)
	if err != nil {
		return nil, nil, err
	}
	err = resolver.expand(target, func(newDoc interface{}) {
		target = newDoc
		setTarget(newDoc)
	}, u)
	if err != nil {
		return nil, nil, err
	}
	return target, u, err
}

func ExpandRefs(rdoc *interface{}, docURL *url.URL) error {
	if len(docURL.Fragment) > 0 {
		panic("URL fragment unexpected for initial document")
	}

	docURLstr := docURL.String()
	resolver := refResolver{
		rootURL: docURL.String(),
		docs: map[string]*interface{}{
			docURLstr: rdoc,
		},
		inject:  make(map[string]string),
		visited: make(map[string]bool),
	}

	err := resolver.expand(*rdoc, func(newDoc interface{}) {
		*rdoc = newDoc
	}, docURL)

	if err != nil {
		return err
	}

	// Inject content in external documents pointed by $ref.
	// The inject path is the same as the path in the source doc.
	for ptr, u := range resolver.inject {
		// log.Println(ptr, u)

		if _, err := jsonptr.Get(*rdoc, ptr); err != nil {
			return fmt.Errorf("%s: content replaced from %s", ptr, u)
		}
		target, _ := jsonptr.Get(*resolver.docs[u], ptr)
		_ = jsonptr.Set(rdoc, ptr, target)
	}

	// As some $ref pointed to external documents we have to fix them
	if len(resolver.docs) > 1 {
		_ = visitRefs(*rdoc, nil, func(ptr jsonptr.Pointer, ref string) (string, error) {
			i := strings.IndexByte(ref, '#')
			if i > 0 {
				ref = ref[i:]
			}
			return ref, nil
		})
	}

	return err
}

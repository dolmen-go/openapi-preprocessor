package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mohae/deepcopy"

	"github.com/dolmen-go/jsonptr"
)

// visitRefs visits $ref and allows to change them.
func visitRefs(root interface{}, ptr jsonptr.Pointer, visitor func(jsonptr.Pointer, string) (string, error)) (err error) {
	//log.Println(ptr)
	switch root := root.(type) {
	case map[string]interface{}:
		if len(root) == 0 {
			break
		}
		ptr.Grow(1)
		for _, k := range sortedKeys(root) {
			ptr.Property(k)
			if k == "$ref" {
				if str, isString := root[k].(string); isString {
					root[k], err = visitor(ptr, str)
					if err != nil {
						return
					}
				}
			} else {
				err = visitRefs(root[k], ptr, visitor)
				if err != nil {
					break
				}
			}
			ptr.Up()
		}
	case []interface{}:
		if len(root) == 0 {
			break
		}
		ptr.Grow(1)
		for i, v := range root {
			ptr.Index(i)
			// log.Println(ptr)
			err = visitRefs(v, ptr, visitor)
			if err != nil {
				break
			}
			ptr.Up()
		}
	}
	return
}

// loc represents the location of a JSON node.
type loc struct {
	Path string
	Ptr  string
}

func (l *loc) URL() *url.URL {
	u := url.URL{
		Path:     l.Path,
		Fragment: l.Ptr,
	}
	if l.Path[0] == '/' {
		u.Scheme = "file"
	}
	return &u
}

func (l loc) String() string {
	//return l.URL().String()
	if l.Ptr == "" {
		return l.Path
	}
	return l.Path + "#" + l.Ptr
}

/*
func (l *loc) Pointer() jsonptr.Pointer {
	ptr, _ := jsonptr.Parse(l.Ptr)
	return ptr
}
*/

func (l *loc) Property(name string) loc {
	return loc{
		Path: l.Path,
		//Ptr:  l.Ptr + "/" + jsonptr.EscapeString(name),
		Ptr: string(jsonptr.AppendEscape(append([]byte(l.Ptr), '/'), name)),
	}
}

func (l *loc) Index(i int) loc {
	return loc{
		Path: l.Path,
		//Ptr:  l.Ptr + "/" + strconv.Itoa(i),
		Ptr: string(strconv.AppendInt(append([]byte(l.Ptr), '/'), int64(i), 10)),
	}
}

func (l *loc) Rel(basePath string) loc {
	// FIXME do not use FS dependent paths
	rel, err := filepath.Rel(filepath.FromSlash(basePath), filepath.FromSlash(l.Path))
	if err != nil {
		return *l
	}

	return loc{rel, l.Ptr}
}

type setter func(interface{})

type node struct {
	data interface{}
	set  setter
	loc  loc
}

type refResolver struct {
	basePath string // absolute path to make errors relative to
	rootPath string
	docs     map[string]*interface{} // path -> rdoc
	visited  map[loc]bool
	inject   map[string]string
	inlining bool
}

type errExpand struct {
	loc loc
	err error
}

func (e *errExpand) Error() string {
	return e.loc.String() + ": " + e.err.Error()
}

func (resolver *refResolver) Error(loc *loc, err error) error {
	return &errExpand{loc.Rel(resolver.basePath), err}
}

func (resolver *refResolver) Errorf(loc *loc, msg string, args ...interface{}) error {
	var err error
	if len(args) == 0 {
		err = errors.New(msg)
	} else {
		err = fmt.Errorf(msg, args...)
	}
	return resolver.Error(loc, err)
}

func (resolver *refResolver) resolve(link string, relativeTo *loc) (*node, error) {
	// log.Println(link, relativeTo)
	var targetLoc loc
	var ptr jsonptr.Pointer
	var err error

	if i := strings.IndexByte(link, '#'); i >= 0 {
		targetLoc.Path = link[:i]
		targetLoc.Ptr = link[i+1:]
		ptr, err = jsonptr.Parse(targetLoc.Ptr)
		if err != nil {
			return nil, fmt.Errorf("%q: %v", targetLoc.Ptr, err)
		}
	} else {
		targetLoc.Path = link
	}

	if len(targetLoc.Path) > 0 {
		tmpPath, err := url.PathUnescape(targetLoc.Path)
		if err != nil {
			return nil, fmt.Errorf("%q: %v", targetLoc.Path, err)
		}
		targetLoc.Path = resolvePath(relativeTo.Path, tmpPath)
	} else {
		targetLoc.Path = relativeTo.Path
	}

	// log.Println("=>", u)

	if targetLoc.Path == relativeTo.Path && strings.HasPrefix(relativeTo.Ptr, targetLoc.Ptr+"/") {
		return nil, errors.New("circular link")
	}

	rdoc, loaded := resolver.docs[targetLoc.Path]
	if !loaded {
		//log.Println("Loading", &targetLoc)
		doc, err := loadFile(filepath.FromSlash(targetLoc.Path))
		if err != nil {
			return nil, fmt.Errorf("can't load %q: %v", targetLoc.Path, err)
		}
		var itf interface{}
		itf = doc
		rdoc = &itf
		resolver.docs[targetLoc.Path] = rdoc
	}

	if targetLoc.Ptr == "" {
		return &node{*rdoc, func(data interface{}) {
			*rdoc = data
		}, targetLoc}, nil
	}

	// FIXME we could reduce the number of evals of JSON pointers...

	frag, err := ptr.In(*rdoc)
	if err != nil {
		// If the can't be immediately resolved, this may be because
		// of a $inline in the way

		p := jsonptr.Pointer{}
		for {
			// log.Println(p)
			doc, err := p.In(*rdoc)
			if err != nil {
				// Failed to resolve the fragment
				return nil, err
			}
			if obj, isMap := doc.(map[string]interface{}); isMap {
				if _, isInline := obj["$inline"]; isInline {
					//log.Printf("%#v", obj)
					err := resolver.expand(node{obj, func(data interface{}) {
						p.Set(rdoc, data)
					}, loc{Path: targetLoc.Path, Ptr: p.String()}})
					if err != nil {
						return nil, err
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

	return &node{frag, func(data interface{}) {
		ptr.Set(rdoc, data)
	}, targetLoc}, nil
}

func (resolver *refResolver) expand(n node) error {
	//log.Println(u, node.loc.Ptr)
	if resolver.visited[n.loc] {
		return nil
	}
	if !resolver.inlining {
		resolver.visited[n.loc] = true
	}

	if doc, isSlice := n.data.([]interface{}); isSlice {
		for i, v := range doc {
			switch v.(type) {
			case []interface{}, map[string]interface{}:
				err := resolver.expand(node{v, func(data interface{}) {
					doc[i] = data
				}, n.loc.Index(i)})
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	obj, isObject := n.data.(map[string]interface{})
	if !isObject || obj == nil {
		return nil
	}

	if ref, isRef := obj["$ref"]; isRef {
		return resolver.expandTagRef(obj, n.set, &n.loc, ref)
	}

	// An extension to build an object from mixed local data and
	// imported data
	if refs, isMerge := obj["$merge"]; isMerge {
		return resolver.expandTagMerge(obj, n.set, &n.loc, refs)
	}

	if ref, isInline := obj["$inline"]; isInline {
		return resolver.expandTagInline(obj, n.set, &n.loc, ref)
	}

	for _, k := range sortedKeys(obj) {
		//log.Println("Key:", k)
		err := resolver.expand(node{obj[k], func(data interface{}) {
			obj[k] = data
		}, n.loc.Property(k)})
		if err != nil {
			return err
		}
	}

	return nil
}

// expandTagRef expands (follows) a $ref link.
func (resolver *refResolver) expandTagRef(obj map[string]interface{}, set setter, l *loc, ref interface{}) error {
	//log.Printf("$ref: %s => %s", l, ref)
	link, isString := ref.(string)
	if !isString {
		return resolver.Errorf(&loc{l.Path, l.Ptr + "/$ref"}, "must be a string")
	}

	if len(obj) > 1 {
		return resolver.Errorf(l, "$ref must be alone (tip: use $merge instead)")
	}

	target, err := resolver.resolveAndExpand(link, l)
	if err != nil {
		return err
	}
	if l.Ptr != target.loc.Ptr && strings.HasPrefix(l.Ptr+"/", target.loc.Ptr+"/") {
		if target.loc.Ptr == "" {
			return resolver.Errorf(l, "injection of %q at root will create a circular link (tip: use $inline)", target.loc.Path)
		}
		return resolver.Errorf(l, "injection of %q at path %q will create a circular link (tip: use $inline)", target.loc, target.loc.Ptr)
	}

	if resolver.inject != nil {
		if target.loc.Path != resolver.rootPath {
			if src := resolver.inject[l.Ptr]; src != "" && src != target.loc.Path {
				// TODO we should also save l in resolver.inject to be able to signal the location
				// of $ref that provoke the injection
				return resolver.Errorf(l, "import fragment %q is imported from %q and %q", link, src, target.loc.Path)
			}
			resolver.inject[l.Ptr] = target.loc.Path
		}
	}

	return nil
}

// expandTagMerge expands a $merge object.
func (resolver *refResolver) expandTagMerge(obj map[string]interface{}, set setter, l *loc, refs interface{}) error {
	var links []string
	switch refs := refs.(type) {
	case string:
		if len(obj) == 1 {
			return resolver.Errorf(l, "merging with nothing?")
		}
		links = []string{refs}
	case []interface{}:
		links = make([]string, len(refs))
		for i, v := range refs {
			lnk, isString := v.(string)
			if !isString {
				return resolver.Errorf(&loc{l.Path, fmt.Sprintf("%s/%d", l.Ptr, i)}, "must be a string")
			}
			// Reverse order
			links[len(links)-1-i] = lnk
		}
		if len(links) == 1 && len(obj) == 1 {
			return resolver.Errorf(l, "merging with nothing? (tip: use $inline)")
		}
	default:
		return resolver.Errorf(&loc{l.Path, l.Ptr + "/$merge"}, "must be a string or array of strings")
	}
	delete(obj, "$merge")

	delete(resolver.visited, *l)
	err := resolver.expand(node{obj, func(data interface{}) {
		obj = data.(map[string]interface{})
		set(data)
	}, *l})
	resolver.visited[*l] = true
	if err != nil {
		return err
	}

	// overrides := make(map[string]string)
	// fill with (key => loc.Property(key))

	for i, link := range links {
		target, err := resolver.resolveAndExpand(link, l)
		if err != nil {
			return err
		}

		objTarget, isObj := target.data.(map[string]interface{})
		if !isObj {
			if len(links) == 1 {
				return resolver.Errorf(&loc{l.Path, l.Ptr + "/$merge"}, "link must point to object")
			}
			return resolver.Errorf(&loc{l.Path, fmt.Sprintf("%s/$merge/%d", l.Ptr, i)}, "link must point to object")
		}
		for k, v := range objTarget {
			if _, exists := obj[k]; exists {
				// TODO warn about overrides if verbose
				// if o, overriden := overrides[k]; overriden {
				//   log.Println("%s overrides %s", l.Property(k), target.loc.Property(k))
				// }
				continue
			}
			obj[k] = v
			// overrides[k] = link
		}
	}

	return nil
}

// expandTagInline expands a $inline object.
func (resolver *refResolver) expandTagInline(obj map[string]interface{}, set setter, l *loc, ref interface{}) error {
	link, isString := ref.(string)
	if !isString {
		return resolver.Errorf(&loc{l.Path, l.Ptr + "/$inline"}, "must be a string")
	}

	inlining := resolver.inlining
	resolver.inlining = true

	target, err := resolver.resolveAndExpand(link, l)
	if err != nil {
		return err
	}
	resolver.inlining = inlining

	target.data = deepcopy.Copy(target.data)
	// Replace the original node (obj) with the copy of the target
	set(target.data)
	// obj is now disconnected from the original tree

	//log.Printf("xxx %#v", target.data)

	if len(obj) > 1 {
		switch targetX := target.data.(type) {
		case map[string]interface{}:
			// To forbid raw '$' (because we have '$inline'), but still enable it
			// in pointers, we use "~2" as a replacement as it is not a valid JSON Pointer
			// sequence.
			replDollar := strings.NewReplacer("~2", "$")
			var prefixes []string
			for _, k := range sortedKeys(obj) {
				if len(k) > 0 && k[0] == '$' { // skip $inline
					continue
				}
				v := obj[k]
				//log.Println(k)
				err = resolver.expand(node{v, func(data interface{}) {
					v = data
				}, l.Property(k)})
				if err != nil {
					return err
				}
				ptr := "/" + replDollar.Replace(k)
				if !strings.ContainsAny(k, "/") {
					prop, err := jsonptr.UnescapeString(ptr[1:])
					if err != nil {
						return resolver.Errorf(l, "%q: %v", k, err)
					}
					targetX[prop] = v
					prefixes = append(prefixes[:0], ptr)
				} else {
					// If patching a previous patch, we want to preserve the source
					// Find the previous longest prefix of ptr, if any, and clone the tree
					i := len(prefixes) - 1
					for i > 0 {
						p := prefixes[i]
						if strings.HasPrefix(ptr, p+"/") {
							p = p[:len(p)-1]
							t, _ := jsonptr.Get(target, p)
							t = deepcopy.Copy(t)
							jsonptr.Set(&target.data, p, t)
							break
						}
						i--
					}
					prefixes = append(prefixes[:i+1], ptr) // clear longer prefixes and append this one
					if err := jsonptr.Set(&target.data, ptr, v); err != nil {
						return resolver.Error(&loc{l.Path, l.Ptr + "/" + k}, err)
					}
				}
			}
		case []interface{}:
			// TODO
			return resolver.Errorf(l, "inlining of array not yet implemented")
		default:
			return resolver.Errorf(l, "inlined scalar value can't be patched")
		}
	}

	return nil
}

func (resolver *refResolver) resolveAndExpand(link string, relativeTo *loc) (n *node, err error) {
	n, err = resolver.resolve(link, relativeTo)
	if err != nil {
		if _, isExpandErr := err.(*errExpand); !isExpandErr {
			err = resolver.Error(relativeTo, err)
		}
	} else {
		err = resolver.expand(*n)
	}
	return
}

func ExpandRefs(rdoc *interface{}, docURL *url.URL) error {
	if len(docURL.Fragment) > 0 {
		panic("URL fragment unexpected for initial document")
	}

	cwd, _ := os.Getwd()

	path := path.Clean(docURL.Path)
	resolver := refResolver{
		basePath: filepath.ToSlash(cwd),
		rootPath: path,
		docs: map[string]*interface{}{
			path: rdoc,
		},
		inject:  make(map[string]string),
		visited: make(map[loc]bool),
	}

	// First step:
	// - load referenced documents
	// - collect $ref locations pointing to external documents
	// - replace $inline, $merge
	err := resolver.expand(node{*rdoc, func(data interface{}) {
		*rdoc = data
	}, loc{Path: path}})

	if err != nil {
		return err
	}

	// Second step:
	// Inject content from external documents pointed by $ref.
	// The inject path is the same as the path in the source doc.
	for ptr, sourcePath := range resolver.inject {
		// log.Println(ptr, sourcePath)

		if _, err := jsonptr.Get(*rdoc, ptr); err != nil {
			return fmt.Errorf("%s: content replaced from %s", ptr, sourcePath)
		}
		target, err := jsonptr.Get(*resolver.docs[sourcePath], ptr)
		if err != nil {
			return fmt.Errorf("%s#%s has disappeared after replacement of $inline and $merge: %v", sourcePath, ptr, err)
		}
		if err = jsonptr.Set(rdoc, ptr, target); err != nil {
			return fmt.Errorf("%s#%s: %v", sourcePath, ptr, err)
		}
	}

	// Third step:
	// As some $ref pointed to external documents we have to fix them to make the references
	// local.
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

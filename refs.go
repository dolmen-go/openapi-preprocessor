package main

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mohae/deepcopy"

	"github.com/dolmen-go/jsonptr"
)

func skipRef(ptr jsonptr.Pointer) bool {
	if len(ptr) < 1 {
		return false
	}
	last := ptr[len(ptr)-1]
	return last == "properties" ||
		last == "additionalProperties"
}

// visitRefs visits $ref and allows to change them.
func visitRefs(root any, ptr jsonptr.Pointer, visitor func(jsonptr.Pointer, string) (string, error)) (err error) {
	//log.Println(ptr)
	switch root := root.(type) {
	case map[string]any:
		if len(root) == 0 {
			break
		}
		ptr.Grow(1)
		for _, k := range sortedKeys(root) {
			ptr.Property(k)
			if k == "$ref" && !skipRef(ptr[:len(ptr)-1]) {
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
	case []any:
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
	rel, err := filepath.Rel(urlPathToOSPath(basePath), urlPathToOSPath(l.Path))
	if err != nil {
		return *l
	}

	return loc{rel, l.Ptr}
}

type setter func(any)

type node struct {
	data any
	set  setter
	loc  loc
}

// IsRef returns true if the node is a $ref.
func (n *node) IsRef() bool {
	obj, isObj := n.data.(map[string]any)
	if !isObj || obj == nil {
		return false
	}
	link, isString := obj["$ref"].(string)
	if !isString {
		return false
	}
	return len(link) > 0
}

// Ref returns the link of a $ref node.
func (n *node) Ref() string {
	obj, isObj := n.data.(map[string]any)
	if !isObj || obj == nil {
		return ""
	}
	link, isString := obj["$ref"].(string)
	if !isString {
		return ""
	}
	return link
}

type refResolver struct {
	basePath string // absolute path to make errors relative to
	rootPath string
	docs     map[string]*any // path -> rdoc
	visited  map[loc]bool
	inject   map[string]injection // pointer in the final document -> source
	deferred []deferredLink
	inlining bool
	trace    func(string)

	swagger bool // Swagger 2.0 (else OpenAPI 3.x)

	// securitySchemes is the location of security schemes:
	// "/components/securitySchemes/" (OpenAPI 3.x) or "/securityDefinitions/" (Swagger 2.0)
	securitySchemes string
}

// swaggerComponents are the containers of reusable components in Swagger 2.0.
var swaggerComponents = map[string]bool{
	"definitions":         true,
	"parameters":          true,
	"responses":           true,
	"securityDefinitions": true,
}

// injection is content to copy, at the same pointer, from a document to the final document.
type injection struct {
	src  string // path of the source document
	from loc    // location of the link which requires the injection
}

// deferredLink is a relative link which target doesn't exist in the document where
// it is used: its resolution is deferred to the final document.
type deferredLink struct {
	link string // "#<pointer>"
	from loc    // location of the link
}

// operationMethods are the keys of operations in a Path Item Object.
var operationMethods = map[string]bool{
	"get":     true,
	"put":     true,
	"post":    true,
	"delete":  true,
	"options": true,
	"head":    true,
	"patch":   true,
	"trace":   true,
	"query":   true, // OpenAPI 3.2
}

// isSecurityPtr reports whether ptr is the location of a list of Security Requirement Objects:
// at the root of a document or in an Operation Object.
func isSecurityPtr(ptr jsonptr.Pointer) bool {
	n := len(ptr)
	if n == 0 || ptr[n-1] != "security" {
		return false
	}
	if n == 1 {
		return true
	}
	op := ptr[:n-1]
	if !operationMethods[op[len(op)-1]] {
		return false
	}
	// /paths/{path}/{method}, /webhooks/{name}/{method}, /components/pathItems/{name}/{method}
	if len(op) >= 3 {
		switch op[len(op)-3] {
		case "paths", "webhooks", "pathItems":
			return true
		}
	}
	// .../callbacks/{name}/{expression}/{method}
	return len(op) >= 4 && op[len(op)-4] == "callbacks"
}

// isNotFound reports whether err (from resolve) is because the target of the link doesn't exist.
func isNotFound(err error) bool {
	var errExp *errExpand
	if errors.As(err, &errExp) {
		// Failure while expanding something else on the way
		return false
	}
	return errors.Is(err, jsonptr.ErrProperty) || errors.Is(err, jsonptr.ErrIndex)
}

type errExpand struct {
	loc loc
	err error
}

func (e *errExpand) Error() string {
	return e.loc.String() + ": " + e.err.Error()
}

func (e *errExpand) Unwrap() error {
	return e.err
}

func (resolver *refResolver) Error(loc *loc, err error) error {
	return &errExpand{loc.Rel(resolver.basePath), err}
}

func (resolver *refResolver) Errorf(loc *loc, msg string, args ...any) error {
	var err error
	if len(args) == 0 {
		err = errors.New(msg)
	} else {
		err = fmt.Errorf(msg, args...)
	}
	return resolver.Error(loc, err)
}

func (resolver *refResolver) Tracef(msg string, args ...any) {
	if resolver.trace == nil {
		return
	}
	resolver.trace(fmt.Sprintf(msg, args...))
}

func (resolver *refResolver) resolve(link string, relativeTo *loc) (*node, error) {
	// log.Println(link, relativeTo)
	var targetLoc loc
	var ptr jsonptr.Pointer
	var err error

	if before, after, ok := strings.Cut(link, "#"); ok {
		targetLoc.Path = before
		targetLoc.Ptr = after
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
		doc, err := loadFile(urlPathToOSPath(targetLoc.Path))
		if err != nil {
			return nil, fmt.Errorf("can't load %q: %v", targetLoc.Path, err)
		}
		var itf any = doc
		rdoc = &itf
		resolver.docs[targetLoc.Path] = rdoc
	}

	if targetLoc.Ptr == "" {
		return &node{*rdoc, func(data any) {
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
			if obj, isMap := doc.(map[string]any); isMap {
				if _, isInline := obj["$inline"]; isInline {
					//log.Printf("%#v", obj)
					err := resolver.expand(node{obj, func(data any) {
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

	return &node{frag, func(data any) {
		ptr.Set(rdoc, data)
	}, targetLoc}, nil
}

func (resolver *refResolver) expand(n node) error {
	resolver.Tracef("%14s %s", n.loc.Path[strings.LastIndexByte(n.loc.Path, '/')+1:], n.loc.Ptr)
	if resolver.visited[n.loc] {
		return nil
	}
	if !resolver.inlining {
		resolver.visited[n.loc] = true
	}

	if doc, isSlice := n.data.([]any); isSlice {
		if isSecurityPtr(jsonptr.MustParse(n.loc.Ptr)) {
			if err := resolver.expandSecurity(n.loc, doc); err != nil {
				return err
			}
		}
		for i, v := range doc {
			switch v.(type) {
			case []any, map[string]any:
				err := resolver.expand(node{v, func(data any) {
					doc[i] = data
				}, n.loc.Index(i)})
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	obj, isObject := n.data.(map[string]any)
	if !isObject || obj == nil {
		return nil
	}

	if ref, isRef := obj["$ref"]; isRef && !skipRef(jsonptr.MustParse(n.loc.Ptr)) {
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
		// /components are expanded only on demand (when targeted by a link)
		// because expanding unused components would inject their external
		// content into the final document.
		if k == "components" && n.loc.Ptr == "" {
			continue
		}
		if err := resolver.expandProperty(n.loc, obj, k); err != nil {
			return err
		}
	}

	return nil
}

func (resolver *refResolver) expandProperty(parentLoc loc, obj map[string]any, key string) error {
	//log.Println("Key:", key)
	return resolver.expand(node{obj[key], func(data any) {
		obj[key] = data
	}, parentLoc.Property(key)})
}

// expandTagRef expands (follows) a $ref link.
func (resolver *refResolver) expandTagRef(obj map[string]any, set setter, l *loc, ref any) error {
	resolver.Tracef("$ref: %s => %s", l, ref)
	link, isString := ref.(string)
	if !isString {
		return resolver.Errorf(&loc{l.Path, l.Ptr + "/$ref"}, "must be a string")
	}

	if len(obj) > 1 {
		unexpected := len(obj) - 1 // Don't count $ref
		// http://spec.openapis.org/oas/v3.1.0#reference-object
		if _, ok := stringProp(obj, "summary"); ok {
			unexpected--
		}
		if _, ok := stringProp(obj, "description"); ok {
			unexpected--
		}
		// Non-standard, but that's not our job to validate spec
		if _, ok := stringProp(obj, "$comment"); ok {
			unexpected--
		}
		if unexpected > 0 {
			return resolver.Errorf(l, "$ref must be alone (tip: use $merge instead)")
		}
	}

	target, err := resolver.importLink(link, l)
	if err != nil || target == nil { // target == nil: deferred
		return err
	}
	if l.Ptr != target.loc.Ptr && strings.HasPrefix(l.Ptr+"/", target.loc.Ptr+"/") {
		if target.loc.Ptr == "" {
			return resolver.Errorf(l, "injection of %q at root will create a circular link (tip: use $inline)", target.loc.Path)
		}
		return resolver.Errorf(l, "injection of %q at path %q will create a circular link (tip: use $inline)", target.loc, target.loc.Ptr)
	}

	return nil
}

// importLink resolves link from l, expands its target and records its injection into
// the final document.
//
// If link is relative and its target doesn't exist, the link is deferred and the returned node is nil.
func (resolver *refResolver) importLink(link string, l *loc) (*node, error) {
	target, err := resolver.resolve(link, l)
	if err != nil {
		if isRelativeLink(link) && isNotFound(err) {
			resolver.deferLink(link, l)
			return nil, nil
		}
		if _, isExpandErr := err.(*errExpand); !isExpandErr {
			err = resolver.Error(l, err)
		}
		return nil, err
	}
	if err = resolver.expandNode(target); err != nil {
		return nil, err
	}
	if err = resolver.recordInjection(target, l, link); err != nil {
		return nil, err
	}
	return target, nil
}

// isRelativeLink reports whether link is relative to the document where it is used ("#<pointer>").
//
// The target of a relative link is imported from that document if it exists there. Else its
// resolution is deferred to the final document (see [refResolver.deferLink]).
func isRelativeLink(link string) bool {
	return len(link) > 0 && link[0] == '#'
}

// deferLink records a relative link which target doesn't exist in the document
// where it is used. It will be resolved in the final document.
func (resolver *refResolver) deferLink(link string, l *loc) {
	resolver.Tracef("deferred: %s => %s", l, link)
	resolver.deferred = append(resolver.deferred, deferredLink{link: link, from: *l})
}

// recordInjection records that target, linked from l, must be copied to the final document
// at the same pointer, if it isn't already in the root document.
func (resolver *refResolver) recordInjection(target *node, l *loc, link string) error {
	if target.loc.Path == resolver.rootPath {
		return nil
	}
	ptr, src := target.loc.Ptr, target.loc.Path
	if prev, exists := resolver.inject[ptr]; exists && prev.src != src {
		// Chain of $ref at the same pointer: keep the final target
		if resolver.linksTo(target.data, src, prev.src, ptr) {
			return nil
		}
		prevContent, err := jsonptr.Get(*resolver.docs[prev.src], ptr)
		if err != nil || !resolver.linksTo(prevContent, prev.src, src, ptr) {
			return resolver.Errorf(l, "import fragment %q is imported from %s and %s (from %s)",
				link, resolver.relPath(prev.src), resolver.relPath(src), prev.from.Rel(resolver.basePath))
		}
	} else if exists {
		return nil
	}
	resolver.inject[ptr] = injection{src: src, from: *l}
	return nil
}

// expandSecurity expands the security schemes named in a list of Security Requirement Objects.
//
// A name is a link relative to the document where the requirement is used.
func (resolver *refResolver) expandSecurity(l loc, reqs []any) error {
	for i, req := range reqs {
		req, isObj := req.(map[string]any)
		if !isObj {
			continue
		}
		lReq := l.Index(i)
		for _, name := range sortedKeys(req) {
			link := "#" + resolver.securitySchemes + jsonptr.EscapeString(name)
			lName := lReq.Property(name)
			if _, err := resolver.importLink(link, &lName); err != nil {
				return err
			}
		}
	}
	return nil
}

// expandTagMerge expands a $merge object.
func (resolver *refResolver) expandTagMerge(obj map[string]any, set setter, l *loc, refs any) error {
	resolver.Tracef("$merge at %s", l)
	var links []string
	switch refs := refs.(type) {
	case string:
		if len(obj) == 1 {
			return resolver.Errorf(l, "merging with nothing?")
		}
		links = []string{refs}
	case []any:
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
	err := resolver.expand(node{obj, func(data any) {
		obj = data.(map[string]any)
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

		objTarget, isObj := target.data.(map[string]any)
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

var replDollar = strings.NewReplacer("~2", "$")

type patch struct {
	key   string
	ptr   jsonptr.Pointer
	value any
}

func sortedPatches(obj map[string]any) ([]*patch, error) {
	patches := make([]*patch, 0, len(obj))
	for k, v := range obj {
		if len(k) > 0 && k[0] == '$' {
			continue
		}
		// To forbid raw '$' (because we have '$inline'), but still enable it
		// in pointers, we use "~2" as a replacement as it is not a valid JSON Pointer
		// sequence.
		ptr, err := jsonptr.Parse("/" + replDollar.Replace(k))
		if err != nil {
			return nil, fmt.Errorf("patch %q: %w", k, err)
		}
		patches = append(patches, &patch{
			key:   k,
			ptr:   ptr,
			value: v,
		})
	}
	slices.SortStableFunc(patches, func(a, b *patch) int {
		if x := cmp.Compare(len(a.ptr), len(b.ptr)); x != 0 {
			return x
		}
		for i := range len(a.ptr) {
			if x := cmp.Compare(a.ptr[i], b.ptr[i]); x != 0 {
				return x
			}
		}
		return cmp.Compare(a.key, b.key) // "~2" vs "$"
	})
	return patches, nil
}

// expandTagInline expands a $inline object.
func (resolver *refResolver) expandTagInline(obj map[string]any, set setter, l *loc, ref any) error {
	resolver.Tracef("$inline: %s => %s", l, ref)
	link, isString := ref.(string)
	if !isString {
		return resolver.Errorf(&loc{l.Path, l.Ptr + "/$inline"}, "must be a string")
	}

	inlining := resolver.inlining
	resolver.inlining = true

	var target *node
	var err error
	l2 := loc{l.Path, l.Ptr} // Clone
	for {
		target, err = resolver.resolveAndExpand(link, &l2)
		if err != nil {
			return err
		}
		// If target is not $ref, stop
		link = target.Ref()
		if link == "" || len(obj) == 1 {
			break
		}
		/*
			if target.loc == l2 {
				// FIXME Fix message
				return resolver.Errorf(&loc{l.Path, l.Ptr + "/$inline"}, "circular link %s %s", l2, link)
			}
		*/
		// Else loop to dereference it
		l2 = loc{target.loc.Path, target.loc.Ptr}
	}

	resolver.inlining = inlining

	target.data = deepcopy.Copy(target.data)
	// Replace the original node (obj) with the copy of the target
	set(target.data)
	// obj is now disconnected from the original tree

	//log.Printf("xxx %#v", target.data)

	if len(obj) > 1 {
		switch target.data.(type) {
		case map[string]any, []any:
			patches, err := sortedPatches(obj)
			if err != nil {
				return resolver.Error(l, err)
			}
			for _, p := range patches {
				v := p.value
				err = resolver.expand(node{v, func(data any) {
					v = data
				}, l.Property(p.key)})
				if err != nil {
					return err
				}
				// Preserve the source, as it may be patched later
				v = deepcopy.Copy(v)
				if err := p.ptr.Set(&target.data, v); err != nil {
					l2 := l.Property(p.key)
					return resolver.Error(&l2, err)
				}
				// If slice, it may have been appended
				set(target.data)
			}
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
		err = resolver.expandNode(n)
	}
	return
}

// expandNode expands n and updates n.data if the node is replaced (ex: $inline).
func (resolver *refResolver) expandNode(n *node) error {
	set := n.set
	n.set = func(data any) {
		n.data = data
		set(data)
	}
	return resolver.expand(*n)
}

func ExpandRefs(rdoc *any, docURL *url.URL, trace func(string)) error {
	if len(docURL.Fragment) > 0 {
		panic("URL fragment unexpected for initial document")
	}

	cwd, _ := os.Getwd()

	path := path.Clean(docURL.Path)
	resolver := refResolver{
		basePath: osPathToURLPath(cwd),
		rootPath: path,
		docs: map[string]*any{
			path: rdoc,
		},
		inject:  make(map[string]injection),
		visited: make(map[loc]bool),
		trace:   trace,

		securitySchemes: "/components/securitySchemes/",
	}
	if root, isObj := (*rdoc).(map[string]any); isObj {
		if _, resolver.swagger = root["swagger"]; resolver.swagger {
			resolver.securitySchemes = "/securityDefinitions/"
		}
	}

	// First step:
	// - load referenced documents
	// - collect links to content to import from other documents
	// - collect relative links which target doesn't exist where they are used
	// - replace $inline, $merge
	err := resolver.expand(node{*rdoc, func(data any) {
		*rdoc = data
	}, loc{Path: path}})

	if err != nil {
		return err
	}

	// Second step:
	// Expand the targets of deferred links that exist in the root document
	// (as /components are expanded only on demand). This may defer more links.
	rootLoc := loc{Path: path}
	for i := 0; i < len(resolver.deferred); i++ {
		d := resolver.deferred[i]
		if d.from.Path == path {
			continue // Already not found in the root document
		}
		target, err := resolver.resolve(d.link, &rootLoc)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			if _, isExpandErr := err.(*errExpand); !isExpandErr {
				err = resolver.Error(&d.from, err)
			}
			return err
		}
		if err = resolver.expandNode(target); err != nil {
			return err
		}
	}

	// Third step:
	// Inject content imported from other documents, at the same pointer.
	if err = resolver.injectAll(rdoc); err != nil {
		return err
	}

	// Fourth step:
	// Check that the targets of deferred links exist in the final document.
	for _, d := range resolver.deferred {
		if _, err := jsonptr.Get(*rdoc, d.link[1:]); err != nil {
			return resolver.Errorf(&d.from, "%q: not found in the final document", d.link)
		}
	}

	// Fifth step:
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

	return nil
}

// injectAll copies content imported from other documents into the final document *rdoc,
// at the same pointer.
//
// The location must either not exist (missing parents are created) or be a $ref to
// the imported content. Else this is a conflict.
func (resolver *refResolver) injectAll(rdoc *any) error {
	type imported struct {
		ptr jsonptr.Pointer
		injection
	}
	imports := make([]imported, 0, len(resolver.inject))
	for p, inj := range resolver.inject {
		imports = append(imports, imported{jsonptr.MustParse(p), inj})
	}
	// Shallow pointers first, so we know where the content of deeper pointers comes from
	slices.SortFunc(imports, func(a, b imported) int {
		if x := cmp.Compare(len(a.ptr), len(b.ptr)); x != 0 {
			return x
		}
		return slices.Compare(a.ptr, b.ptr)
	})

	injected := make(map[string]string, len(imports)) // pointer -> source document
	for _, imp := range imports {
		ptr := imp.ptr.String()

		// The document where the current content at ptr comes from
		base := resolver.rootPath
		for i := len(imp.ptr) - 1; i >= 0; i-- {
			if src, isInjected := injected[imp.ptr[:i].String()]; isInjected {
				base = src
				break
			}
		}
		if base == imp.src {
			continue // Already imported with a parent
		}

		content, err := imp.ptr.In(*resolver.docs[imp.src])
		if err != nil {
			return resolver.Errorf(&imp.from, "%s#%s has disappeared after replacement of $inline and $merge: %v", resolver.relPath(imp.src), ptr, err)
		}
		if current, err := imp.ptr.In(*rdoc); err == nil && !resolver.linksTo(current, base, imp.src, ptr) {
			return resolver.Errorf(&imp.from, "%s: conflict between content imported from %s and content from %s",
				ptr, resolver.relPath(imp.src), resolver.relPath(base))
		}
		if err = resolver.importAt(rdoc, imp.ptr, content); err != nil {
			return resolver.Errorf(&imp.from, "%s#%s: %v", resolver.relPath(imp.src), ptr, err)
		}
		injected[ptr] = imp.src
	}
	return nil
}

// relPath returns the path of a document relative to the base path, for error messages.
func (resolver *refResolver) relPath(pth string) string {
	l := loc{Path: pth}
	return l.Rel(resolver.basePath).Path
}

// linksTo reports whether v, from document base, is a $ref to src#ptr, directly or
// through a chain of $ref at the same pointer in other documents.
func (resolver *refResolver) linksTo(v any, base, src, ptr string) bool {
	for range len(resolver.docs) { // Bound the chain to avoid loops
		obj, isObj := v.(map[string]any)
		if !isObj {
			return false
		}
		link, isString := obj["$ref"].(string)
		if !isString {
			return false
		}
		file, frag, _ := strings.Cut(link, "#")
		if frag != ptr || file == "" {
			return false
		}
		file, err := url.PathUnescape(file)
		if err != nil {
			return false
		}
		base = resolvePath(base, file)
		if base == src {
			return true
		}
		rdoc, isLoaded := resolver.docs[base]
		if !isLoaded {
			return false
		}
		if v, err = jsonptr.Get(*rdoc, ptr); err != nil {
			return false
		}
	}
	return false
}

// importAt stores value at ptr in the final document *rdoc.
//
// Missing parents are created only for a component: /components/<type>/<name>
// (OpenAPI 3.x), or /<container>/<name> in Swagger 2.0 (container is one of
// definitions, parameters, responses, securityDefinitions). Any other missing
// parent is an error.
func (resolver *refResolver) importAt(rdoc *any, ptr jsonptr.Pointer, value any) error {
	var creatable int // Number of leading parents of ptr that may be created
	switch {
	case resolver.swagger:
		if len(ptr) == 2 && swaggerComponents[ptr[0]] {
			creatable = 1
		}
	case len(ptr) == 3 && ptr[0] == "components":
		creatable = 2
	}

	for i := 1; i < len(ptr); i++ {
		parent := ptr[:i]
		if _, err := parent.In(*rdoc); err != nil {
			if !errors.Is(err, jsonptr.ErrProperty) {
				return err
			}
			if i > creatable {
				return fmt.Errorf("%s doesn't exist in the final document", parent)
			}
			if err = parent.Set(rdoc, map[string]any{}); err != nil {
				return err
			}
		}
	}
	return ptr.Set(rdoc, value)
}

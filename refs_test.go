package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dolmen-go/jsonptr"
)

func assertString(t *testing.T, got, expected string) bool {
	if got == expected {
		return true
	}
	t.Errorf("got: %q, expected: %q", got, expected)
	return false
}

func TestLoc(t *testing.T) {
	t.Logf(".URL().String()")
	assertString(t, (&loc{Path: "x.yml"}).URL().String(), "x.yml")
	assertString(t, (&loc{Path: "/tmp/x.yml", Ptr: "/info"}).URL().String(), "file:///tmp/x.yml#/info")

	t.Logf(".String()")
	assertString(t, (&loc{Path: "x.yml"}).String(), "x.yml")
	assertString(t, (&loc{Path: "/tmp/x.yml", Ptr: "/info"}).String(), "/tmp/x.yml#/info")
}

func TestExpandRefs(t *testing.T) {
	runAllExpandRefs(t)
}

func BenchmarkExpandRefs(b *testing.B) {
	runAllExpandRefs(b)
}

func runAllExpandRefs(t interface {
	testing.TB
}) {
	dir, err := os.Open("testdata")
	if err != nil {
		log.Fatal(err)
	}
	all, err := dir.Readdir(-1)
	dir.Close()
	if err != nil {
		log.Fatal(err)
	}

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Name() < all[j].Name()
	})

	for _, f := range all {
		if !f.IsDir() {
			continue
		}
		name := f.Name()
		if name[0] < '0' || name[0] > '9' {
			continue
		}
		switch t := t.(type) {
		case *testing.T:
			t.Run(name, func(t *testing.T) {
				runExpandRefs(t, "testdata/"+name)
			})
		case *testing.B:
			runExpandRefs(t, "testdata/"+name)
		}
	}
}

// decodeJSONExact decodes JSON keeping numbers as written (json.Number), to
// compare results precisely. It is independent of the code under test (loadJSON).
func decodeJSONExact(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func runExpandRefs(t testing.TB, path string) {
	var inputPath string
	for _, ext := range []string{".yml", ".yaml", ".json"} {
		p := path + "/input" + ext
		t.Log(p)
		_, err := os.Stat(p)
		if err == nil {
			inputPath = p
			break
		}
		if os.IsNotExist(err) {
			continue
		}
		log.Fatalf("%s: %v", p, err)
	}
	if inputPath == "" {
		t.Fatal("no input file")
	}

	resultJSON, err := os.ReadFile(filepath.Join(filepath.FromSlash(path), "result.json"))
	if err != nil {
		t.Fatalf("%s/result.json: %v", path, err)
	}
	expected, err := decodeJSONExact(resultJSON)
	if err != nil {
		t.Fatalf("%s/result.json: %v", path, err)
	}

	switch tb := t.(type) {
	case *testing.T:
		var out any
		err = processFile(inputPath, func(result any) error {
			out = result
			return nil
		}, &debugFlags{})
		if err != nil {
			t.Fatal(err)
		}

		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		// Roundtrip to avoid float64/int64 issues because of YAML unserializer.
		// Decode like result.json, for the same representation of numbers.
		out2, err := decodeJSONExact(b)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(out2, expected) {
			var bFmt bytes.Buffer
			json.Indent(&bFmt, b, "", "    ")
			t.Errorf("output doesn't match:\n%s", bFmt.String())
		}
	case *testing.B:
		for i := 0; i < tb.N; i++ {
			_ = processFile(inputPath, func(any) error {
				return nil
			}, &debugFlags{})
		}
	}
}

func TestInlineIndirect(t *testing.T) {
	runExpandRefs(t, "testdata/41-inline-indirect")
}

func Benchmark43(b *testing.B) {
	runExpandRefs(b, "testdata/43-inline-overrides-deep")
}

// TestExpandRefsErrors runs the testdata/errors/*/input.yml cases, which must
// fail with the error in error.txt.
func TestExpandRefsErrors(t *testing.T) {
	dirs, err := filepath.Glob("testdata/errors/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			expected, err := os.ReadFile(filepath.Join(dir, "error.txt"))
			if err != nil {
				t.Fatal(err)
			}
			err = processFile(filepath.Join(dir, "input.yml"), func(any) error {
				return nil
			}, &debugFlags{})
			if err == nil {
				t.Fatal("error expected")
			}
			// Error messages contain OS paths
			assertString(t, filepath.ToSlash(err.Error()), strings.TrimSpace(string(expected)))
		})
	}
}

func TestIsSecurityPtr(t *testing.T) {
	for _, tc := range []struct {
		ptr      string
		expected bool
	}{
		{"/security", true},
		{"/paths/~1pets/get/security", true},
		{"/paths/~1pets/query/security", true}, // OpenAPI 3.2
		{"/webhooks/newPet/post/security", true},
		{"/components/pathItems/Pets/get/security", true},
		{"/components/callbacks/onEvent/{$request.body#~1url}/post/security", true},
		{"/paths/~1pets/post/callbacks/onEvent/{$request.body#~1url}/post/security", true},
		{"/webhooks/newPet/post/callbacks/onEvent/{$request.body#~1url}/post/security", true},
		// Callback in a callback
		{"/paths/~1pets/post/callbacks/a/{$url}/post/callbacks/b/{$url}/post/security", true},

		{"", false},
		{"/info/security", false},
		{"/paths/~1pets/security", false},            // Path Item
		{"/paths/~1pets/parameters/security", false}, // Not a method
		{"/components/schemas/Spec/security", false},
		// Look like operations, but not at their locations in the document
		{"/components/schemas/Spec/examples/0/paths/~1pets/get/security", false},
		{"/components/schemas/Spec/properties/webhooks/newPet/post/security", false},
		{"/x-doc/components/pathItems/Pets/get/security", false},
		{"/components/schemas/Spec/example/callbacks/a/{$url}/post/security", false},
		{"/x-callbacks/callbacks/a/{$url}/post/security", false},
	} {
		if got := isSecurityPtr(jsonptr.MustParse(tc.ptr)); got != tc.expected {
			t.Errorf("isSecurityPtr(%q): got %t, expected %t", tc.ptr, got, tc.expected)
		}
	}
}

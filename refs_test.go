package main

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
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

	expected, err := loadFile(filepath.Join(filepath.FromSlash(path), "result.json"))
	if err != nil {
		t.Fatalf("%s/result.json: %v", path, err)
	}

	switch tb := t.(type) {
	case *testing.T:
		var out interface{}
		err = processFile(inputPath, func(result interface{}) error {
			out = result
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(out, expected) {
			t.Errorf("output doesn't match")
		}
	case *testing.B:
		for i := 0; i < tb.N; i++ {
			_ = processFile(inputPath, func(interface{}) error {
				return nil
			})
		}
	}
}

func TestInlineIndirect(t *testing.T) {
	runExpandRefs(t, "testdata/41-inline-indirect")
}

func Benchmark43(b *testing.B) {
	runExpandRefs(b, "testdata/43-inline-overrides-deep")
}

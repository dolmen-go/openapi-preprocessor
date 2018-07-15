package main

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestExpandRefs(t *testing.T) {
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
		t.Run(name, func(t *testing.T) {
			testExpandRefs(t, "testdata/"+name)
		})
	}
}

func testExpandRefs(t *testing.T, path string) {
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
}

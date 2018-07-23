package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
)

func main() {
	code, err := _main()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func _main() (int, error) {
	log.SetPrefix("")
	log.SetFlags(0)

	if len(os.Args) < 2 {
		return 1, fmt.Errorf("usage: %s <file>", os.Args[0])
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	return 0, processFile(os.Args[1], enc.Encode)
}

func processFile(pth string, encode func(interface{}) error) error {
	pth, err := filepath.Abs(pth)
	if err != nil {
		return err
	}

	spec, err := loadFile(pth)
	if err != nil {
		return err
	}

	var tmp interface{}
	tmp = spec

	err = ExpandRefs(&tmp, &url.URL{
		//Scheme: "file",
		Path: filepath.ToSlash(pth),
	})
	if err != nil {
		return err
	}

	return encode(tmp)
}

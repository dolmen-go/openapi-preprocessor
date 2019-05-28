package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
)

// go build -ldflags "-X main.version=@(#)$(git describe --tags --always --dirty)"
// '@(#)' is a special tag recognized by the 'what' command
var version = "master"

func init() {
	if len(version) > 0 && version[0] == '@' {
		version = version[4:]
	}
}

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

	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "show program version")

	var compactJSON bool
	flag.BoolVar(&compactJSON, "c", false, "compact JSON output")
	flag.BoolVar(&compactJSON, "compact-output", false, "compact JSON output")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [<option>...] <file>\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return 0, nil
	}

	if flag.NArg() < 1 {
		flag.Usage()
	}

	enc := json.NewEncoder(os.Stdout)
	if !compactJSON {
		enc.SetIndent("", "  ")
	}

	return 0, processFile(flag.Arg(0), enc.Encode)
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

	for _, transform := range []func(*interface{}) error{
		CleanUnused,
	} {
		err = transform(&tmp)
		if err != nil {
			return err
		}
	}

	return encode(tmp)
}

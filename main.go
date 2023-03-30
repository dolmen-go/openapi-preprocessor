package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// go build -ldflags "-X main.version=@(#)$(git describe --tags --always --dirty)"
// '@(#)' is a special tag recognized by the 'what' command
var version = "master"

func init() {
	if len(version) > 0 && version[0] == '@' {
		version = version[4:]
	}
}

type debugFlags struct {
	Trace bool
}

func (dbg debugFlags) String() string {
	if dbg.Trace {
		return "trace"
	}
	return ""
}

func (dbg *debugFlags) Set(s string) error {
	if s == "" {
		return nil
	}
	vals := strings.Split(s, ",")
	for _, v := range vals {
		switch v {
		case "trace":
			dbg.Trace = true
		default:
			return fmt.Errorf("invalid debug value %q", v)
		}
	}
	return nil
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
	var debug debugFlags
	flag.Var(&debug, "debug", "debug flags comma separated (trace=trace document navigation)")

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

	return 0, processFile(flag.Arg(0), enc.Encode, &debug)
}

func processFile(pth string, encode func(interface{}) error, debug *debugFlags) error {
	pth, err := filepath.Abs(pth)
	if err != nil {
		return err
	}

	spec, err := loadFile(pth)
	if err != nil {
		return err
	}

	var tmp interface{} = spec

	var trace func(string)
	if debug.Trace {
		buf := append(make([]byte, 0, 1024), "[TRACE] "...)
		trace = func(s string) {
			buf = append(append(buf, s...), '\n')
			os.Stderr.Write(buf)
			buf = buf[:8]
		}
	}

	err = ExpandRefs(&tmp, &url.URL{
		//Scheme: "file",
		Path: filepath.ToSlash(pth),
	}, trace)
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

// Rebuild openapi-preprocessor.1 from godoc:
//go:generate go run github.com/lufia/godoc2man@v0.0.0-20241209075419-fff9a8c99089

/*
openapi-preprocessor process keywords in an extended version of an OpenAPI specification.

# Synopsis

	openapi-preprocessor [-c] [-compact-output] [-debug=trace] <spec[.yaml|.json]>

	openapi-preprocessor -version

# Options

  - -c compact JSON output
  - -debug=trace show trace of how the document is traversed

# Preprocessor directives

See [full documentation] online.

# Authors

  - Olivier Mengué <dolmen@cpan.org>

[full documentation]: https://github.com/dolmen-go/openapi-preprocessor/blob/master/README.md#keywords
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// version returns the program version from the build info embedded by the Go
// toolchain: the module version when installed with 'go install <module>@<version>',
// or a pseudo-version derived from the VCS state when built from a checkout.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Version == "" {
		return "(unknown)"
	}
	return bi.Main.Version
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
	vals := strings.SplitSeq(s, ",")
	for v := range vals {
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
		fmt.Println(version())
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

func processFile(pth string, encode func(any) error, debug *debugFlags) error {
	pth, err := filepath.Abs(pth)
	if err != nil {
		return err
	}

	spec, err := loadFile(pth)
	if err != nil {
		return err
	}

	var tmp any = spec

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
		Path: osPathToURLPath(pth),
	}, trace)
	if err != nil {
		return err
	}

	for _, transform := range []func(*any) error{
		CleanUnused,
	} {
		err = transform(&tmp)
		if err != nil {
			return err
		}
	}

	return encode(tmp)
}

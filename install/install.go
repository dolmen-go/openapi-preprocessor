package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

const (
	repoOwner = "dolmen-go"
	repo      = "openapi-preprocessor"
	branch    = "master"
	cmd       = repo

	mod = "github.com/" + repoOwner + "/" + repo
	pkg = mod
)

func main() {
	verbose := len(os.Args) > 1 && os.Args[1] == "-v"
	if runtime.Version() >= "go1.16" {
		var info struct {
			C struct {
				C struct {
					C struct {
						Date time.Time `json:"date"`
					} `json:"committer"`
				} `json:"commit"`
				SHA string `json:"sha"`
			} `json:"commit"`
		}

		r, err := http.Get("https://api.github.com/repos/" + repoOwner + "/" + repo + "/branches/" + branch)
		check(err)
		dec := json.NewDecoder(r.Body)
		check(dec.Decode(&info))
		r.Body.Close()

		//fmt.Println(info.C.C.C.Date, info.C.SHA)
		version := "v0.0.0-" + info.C.C.C.Date.In(time.UTC).Format("20060102150405") + "-" + info.C.SHA[:12]
		//fmt.Println(version)

		if verbose {
			fmt.Printf("Installing %s %s...\n", cmd, version)
		}
		_, err = exec.Command("go", "install", "-ldflags", "-X main.version=@(#)"+version, pkg+"@"+version).Output()
		check(err)
	} else {
		goExec, err := exec.LookPath("go")
		check(err)
		env := append(os.Environ(),
			"GO111MODULE=off", // For "go get"
		)
		run := func(cmd string, args ...string) {
			c := exec.Command(cmd, args...)
			c.Env = env
			check(c.Run())
		}
		runStr := func(cmd string, args ...string) string {
			c := exec.Command(cmd, args...)
			c.Env = env
			out, err := c.Output()
			check(err)
			return string(out[:len(out)-1]) // Remove last \n
		}
		GOPATH := runStr(goExec, "env", "GOPATH")
		check(os.Chdir(GOPATH))
		run(goExec, "get", "-u", mod)
		check(os.Chdir(GOPATH + "/src/" + mod))
		var dateSeconds int64
		var sha12 string
		_, err = fmt.Sscanf(runStr(goExec, "log", "-1", "--date=raw", "--abbrev=12", "--pretty=tformat:%cd %h"), "%d %s", &sha12)
		check(err)
		version := "v0.0.0-" + time.Unix(dateSeconds, 0).UTC().Format("20060102150405") + "-" + sha12

		if verbose {
			fmt.Printf("Installing %s %s...\n", cmd, version)
		}
		run(goExec, "install", "-ldflags", "-X main.version=@(#)"+version, pkg)
	}
	if verbose {
		fmt.Println("OK.")
	}
}

func check(err error) {
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok && e != nil && len(e.Stderr) > 0 {
			fmt.Fprintln(os.Stderr, "Error:", string(e.Stderr))
		} else {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}

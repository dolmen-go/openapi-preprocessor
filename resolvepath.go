// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file of the Go project.

package main

import (
	"runtime"
	"strings"
)

// resolvePath applies special path segments from refs and applies
// them to base, per RFC 3986.
//
// Copied from package net/url.
func resolvePath(base, ref string) string {
	var full string
	if ref == "" {
		full = base
	} else if ref[0] != '/' {
		i := strings.LastIndex(base, "/")
		full = base[:i+1] + ref
	} else {
		full = ref
	}
	if full == "" {
		return ""
	}
	var dst []string
	src := strings.Split(full, "/")
	for _, elem := range src {
		switch elem {
		case ".":
			// drop
		case "..":
			if len(dst) > 0 {
				dst = dst[:len(dst)-1]
			}

		default:
			dst = append(dst, elem)
		}
	}

	if last := src[len(src)-1]; last == "." || last == ".." {
		// Add final slash to the joined path.
		dst = append(dst, "")
	}

	if runtime.GOOS == "windows" {
		return strings.TrimPrefix(strings.Join(dst, "/"), "/")
	}
	return "/" + strings.TrimPrefix(strings.Join(dst, "/"), "/")
}

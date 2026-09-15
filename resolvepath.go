// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file of the Go project.

package main

import (
	"path/filepath"
	"runtime"
	"strings"
)

// osPathToURLPath converts a native absolute filesystem path (as returned by
// filepath.Abs) into the "/"-rooted path convention used internally to
// identify documents, which resolvePath always produces and expects as input.
//
// On POSIX this is a no-op beyond slash conversion, since native absolute
// paths already start with "/". On Windows, native absolute paths start with
// a drive letter (e.g. "C:\foo\bar") with no leading separator, so one is
// added, matching the "file:///C:/foo/bar" URI convention.
func osPathToURLPath(pth string) string {
	pth = filepath.ToSlash(pth)
	if pth == "" || pth[0] != '/' {
		pth = "/" + pth
	}
	return pth
}

// isWindowsDriveAbs reports whether pth is an internal "/"-rooted path
// wrapping a Windows drive-absolute path, e.g. "/C:/foo/bar".
func isWindowsDriveAbs(pth string) bool {
	return len(pth) >= 3 &&
		pth[0] == '/' &&
		(('a' <= pth[1] && pth[1] <= 'z') || ('A' <= pth[1] && pth[1] <= 'Z')) &&
		pth[2] == ':' &&
		(len(pth) == 3 || pth[3] == '/')
}

// urlPathToOSPath is the reverse of osPathToURLPath: it turns an internal
// "/"-rooted document path back into a native OS path suitable for os.Open.
//
// On Windows, a path like "/C:/foo/bar" must have its leading "/" stripped
// before the drive letter, or filepath.FromSlash would produce the invalid
// "\C:\foo\bar".
func urlPathToOSPath(pth string) string {
	if runtime.GOOS == "windows" && isWindowsDriveAbs(pth) {
		pth = pth[1:]
	}
	return filepath.FromSlash(pth)
}

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
	return "/" + strings.TrimPrefix(strings.Join(dst, "/"), "/")
}

# openapi-preprocessor

`openapi-preprocessor` is an processing tool that gives flexibility to API documentation authors for writing OpenAPI 2.0/3.x specifications.

[![Travis-CI](https://api.travis-ci.org/dolmen-go/openapi-preprocessor.svg?branch=master)](https://travis-ci.org/dolmen-go/openapi-preprocessor)
[![Codecov](https://img.shields.io/codecov/c/github/dolmen-go/openapi-preprocessor/master.svg)](https://codecov.io/gh/dolmen-go/openapi-preprocessor/branch/master)
[![Go Report Card](https://goreportcard.com/badge/github.com/dolmen-go/openapi-preprocessor)](https://goreportcard.com/report/github.com/dolmen-go/openapi-preprocessor)

## Uses Cases

- Author your OpenAPI spec in YAML but publish as JSON.
- Split your OpenAPI spec source in multiple files for authoring, but publish a single file.
- Build multiple specs from shared parts.
- Merge spec generated from source code with your additional content created by hand.
- Use advanced inlining (`$inline`, `$merge`) to remove duplication (source of inconsistencies).
- Use advanced inlining (`$inline`, `$merge`) to produce complex schemas that share subset of properties.
- Derivate a spec to build a new spec with altered servers settings for localhost/staging/preprod environments.
- *Submit yours...*

## Features

- Every valid OpenAPI 2.0/3.x specification is a valid input (so you can easily start refactoring gradually from an existing spec)
- Allows to build a spec from multiple files; produces a single output file
- YAML or JSON input
- Produces an OpenAPI with maximum compatibility with consumming tools:
  - simplifies complex parts of the spec not supported by all tools
  - JSON output
- Adds a few keywords (`$inline`, `$merge`) that allow to avoid duplication of content and ease the writing of consistent documentation
- Removes unused global schemas (under `/components/schemas`), parameters (under `/components/parameters`) and responses (under `/components/responses`). This reduces risk of leaking work in progress or internal details.

## Install

### Install from source

A [Go 1.23+ development environment](https://go.dev/doc/install#install) is required.

Build `openapi-preprocessor` binary and install in `$GOPATH/bin`:

    $ make install

## Usage

    openapi-preprocessor [<option>...] <file>

## Keywords

### `$ref`

    { "$ref": "<file>" }
    { "$ref": "<file>#<pointer>" }
    { "$ref": "#<pointer>" }

`$ref` is like in OpenAPI, but it can reference content in external files using relative URLs as well as intra-document. The referenced part of the pointed document is injected into the output document.

Restrictions:
- JSON pointer location in the output document will be the same location as in the ref link. Example: `{"$ref": "external.yml#/components/parameters/Id"}` will import the content to `/components/parameters/Id`. This implies that partial files should have the same layout as a full spec (this is a feature as it enforces readability of partials).
- other properties along `$ref` are not allowed as the semantics in JSON Schema and Swagger/OpenAPI has evolved and the support in consuming tools may vary. Use `$merge` instead that has a strict behaviour in this tool.

### `$inline`

    { "$inline": "<file>#<pointer>"}

    {
        "$inline": "<file>#<pointer>",
        "pointer1": <value>, // Overrides value at <file>#<pointer>/pointer1
        "pointer2/slash": <value> // Overrides value deeply at <file>#<pointer>/pointer/slash
    }

`$inline` is an OpenAPI extension allowing to inject a copy of another part of a document in place. Keys along the `$inline` keyword are JSON pointers (with the leading `/` removed) allowing to override some parts of the inlined content.

If the target of `$inline` is a `$ref` and `$inline` has overrides, the link is dereferenced recursively before inlining.

Note: deep inlining (inlining a node which itself use `$inline` in its tree) might work, but will probably not (see [issue #6](https://github.com/dolmen-go/openapi-preprocessor/issues/6) as an example). Use instead `$merge` which supports it.

### `$merge`

    {
        "$merge": "<file>#<pointer>",
        "key": <value>,
        "key/slash": <value> // Overrides value at <file>#<pointer>/key~1slash
    }

    {
        "$merge": [
            "<file1>#<pointer1>",
            "<file2>#<pointer2>" // Overrides keys from <file1>#<pointer1>
        ]
        "key": <value>,
        "key/slash": <value> // Overrides value at <file2>#<pointer2>/key~1slash
    }


`$merge` is an OpenAPI extension allowing to copy a node, overriding some keys. This is a kind of inlined *`$ref` with keys overrides*.

## Examples

See the [testsuite](https://github.com/dolmen-go/openapi-preprocessor/tree/master/testdata).

Running a basic example:

    $ make
    $ ./openapi-preprocessor testdata/10-ref-ext/input.yml

## License

Copyright 2018-2022 Olivier Mengué

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

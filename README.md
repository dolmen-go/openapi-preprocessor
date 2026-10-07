# openapi-preprocessor

`openapi-preprocessor` is a processing tool that gives flexibility to API documentation authors for writing OpenAPI 2.0/3.x specifications.

[![Test](https://github.com/dolmen-go/openapi-preprocessor/actions/workflows/test.yml/badge.svg)](https://github.com/dolmen-go/openapi-preprocessor/actions/workflows/test.yml)
[![Codecov](https://img.shields.io/codecov/c/github/dolmen-go/openapi-preprocessor/master.svg)](https://codecov.io/gh/dolmen-go/openapi-preprocessor/branch/master)

## Uses Cases

- Author your OpenAPI spec in YAML but publish as JSON.
- Split your OpenAPI spec source in multiple files for authoring, but publish a single file.
- Build multiple specs from shared parts.
- Merge spec bits generated from source code with your additional content created by hand: a master document managed by the author, with JSON bits injected.
- Use advanced inlining (`$inline`, `$merge`) to remove duplication (source of inconsistencies).
- Use advanced inlining (`$inline`, `$merge`) to produce complex schemas that share subset of properties.
- Derivate a spec to build a new spec with altered servers settings for localhost/staging/preprod environments.
- Derivate a spec for interoperability with processing tools (ex: apply a patch for downgrading the OpenAPI
  version for a code generation tool that doesn't support the latest standard).
- Derivate a spec from a partner to fix interoperability issues: enable the maintenance of a set of patches
  to apply to the public spec releases before using them internally.
- *Submit yours...*

## Features

- Every valid OpenAPI 2.0/3.x specification is a valid input (so you can easily start refactoring gradually from an existing spec)
- Allows to build a spec from multiple files; produces a single output file
- YAML or JSON input
- Produces an OpenAPI with maximum compatibility with consumming tools:
  - simplifies complex parts of the spec not supported by all tools
  - JSON output
- Adds a few keywords (`$inline`, `$merge`) that allow to avoid duplication of content and ease the writing of consistent documentation
- Removes unused global components: everything under `/components` (in Swagger 2.0: `/definitions`, `/parameters`, `/responses`, `/securityDefinitions`) which is not linked with `$ref` (or, for security schemes, named in a security requirement). This reduces risk of leaking work in progress or internal details.

## Install

### Install from source

A [Go 1.26+ development environment](https://go.dev/doc/install#install) is required.

Build `openapi-preprocessor` binary and install in `$GOPATH/bin`:

    $ go install github.com/dolmen-go/openapi-preprocessor@latest

### Install with mise-en-place

[mise-en-place](https://mise.jdx.dev/) can build and install the tool through
its [`go` backend](https://mise.jdx.dev/dev-tools/backends/go.html) (a Go
toolchain is installed automatically if needed).

Install globally:

    $ mise use -g go:github.com/dolmen-go/openapi-preprocessor@latest

Or pin it as a project dependency (recorded in the project's `mise.toml`):

    $ mise use go:github.com/dolmen-go/openapi-preprocessor@latest

## Usage

    openapi-preprocessor [<option>...] <file>

## Keywords

### `$ref`

    { "$ref": "<file>" }
    { "$ref": "<file>#<pointer>" }
    { "$ref": "#<pointer>" }

`$ref` is [like in OpenAPI](https://spec.openapis.org/oas/latest.html#reference-object), but it can reference content in external files using relative URLs as well as intra-document. The referenced part of the pointed document is injected into the output document.

Restrictions:
- JSON pointer location in the output document will be the same location as in the ref link. Example: `{"$ref": "external.yml#/components/parameters/Id"}` will import the content to `/components/parameters/Id`. This implies that partial files should have the same layout as a full spec (this is a feature as it enforces readability of partials).
- other properties along `$ref` are not allowed, except `summary` and `description` (as in the [OpenAPI 3.1 Reference Object](https://spec.openapis.org/oas/v3.1.0#reference-object)) and `$comment`, which are kept as is in the output. The semantics of other properties along `$ref` in JSON Schema and Swagger/OpenAPI has evolved and the support in consuming tools may vary (see [issue #19](https://github.com/dolmen-go/openapi-preprocessor/issues/19)). Use `$merge` instead that has a strict behaviour in this tool.

A link without a file (`{"$ref": "#<pointer>"}`) is relative to the file where it is used. In a partial file:
- if the target exists in that file, it is imported into the output document. A partial file can so bring its own components.
- else, the target is looked up in the output document, once fully assembled. A partial file can so use components defined in the main document or in other partial files.

The same applies to the names of security schemes in security requirements (`security`): they refer to `/components/securitySchemes/<name>` (`/securityDefinitions/<name>` in Swagger 2.0) of the file where the requirement is used.

Content imported into the output document must not conflict with content already there: a location can only be filled from a single file. The parent of the location must exist in the output document, unless the content is a component (`/components/<type>/<name>`, or in Swagger 2.0 `/definitions/<name>`, `/parameters/<name>`, `/responses/<name>`, `/securityDefinitions/<name>`). A chain of `$ref` at the same location across files (`a.yml#/info` links to `b.yml#/info`) imports the final target. A link to a part of a component (ex: `#/components/schemas/Pet/properties/name`) imports the whole component.

Components (under `/components`) are processed only when they are used: links in unused components are not followed (unused components are removed from the output anyway).

### `$inline`

    { "$inline": "<file>#<pointer>"}

    {
        "$inline": "<file>#<pointer>",
        "pointer1": <value>, // Overrides value at <file>#<pointer>/pointer1
        "pointer2/slash": <value> // Overrides value deeply at <file>#<pointer>/pointer/slash
    }

`$inline` is an OpenAPI extension allowing to inject a copy of another part of a document in place. Keys along the `$inline` keyword are JSON pointers (with the leading `/` removed) allowing to override some parts of the inlined content.

If the target of `$inline` is a `$ref` and `$inline` has overrides, the link is dereferenced recursively before inlining.

Patches are applied from the shallowest to the deepest pointer, so `a` is applied before `a/b`.

The target of `$inline` may also be an array: patch keys are then array indexes (ex: `"0"`,
`"2/name"`), and `-` appends an item.

Notes:
- deep inlining (inlining a node which itself uses `$inline` in its tree) is supported. If it
  doesn't work in some case, use `$merge` as a workaround.
- array patching limitations:
  - `-` is applied before indexes: a patch at the index just past the end of the source array
    (ex: `"3"` on a 3-item array) overwrites the item appended by `-`.
  - an index beyond the end of the array pads it with `null` items.

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

`$merge` and `$inline` can't be used together in the same object.

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

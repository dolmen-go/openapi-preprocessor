# openapi-preprocessor

`openapi-preprocessor` is an authoring tool to ease writing API documentation following OpenAPI 2.0/3.x specifications.

## Features
- Every valid OpenAPI 2.0 specification is a valid input (so you can easily start refactoring gradually from an existing spec)
- Allows to build a spec from multiple files; produces a single output file
- YAML or JSON input: YAML allows 
- Produces an OpenAPI with maximum compatibility with consumming tools:
  - simplifies complex parts of the spec
  - JSON output
  - remove 
- Add a few keywords (`$inline`, `$merge`) that allow to avoid duplication of content and ease the writing of consistent documentation
- (*TODO*) Unused global schemas (under `/definitions`), parameters (under `/parameters`) and responses (under `/responses`) are cleaned.

## Keywords

### `$ref`

    { "$ref": "<file>" }
    { "$ref": "<file>#<pointer>" }
    { "$ref": "#<pointer>" }

`$ref` is like in OpenAPI, but it can reference content in external files using relative URLs as well as intra-document. The referenced part of the pointed document is injected into the output document.

Restriction: JSON pointer location in the output document will be the same location as in the ref link. Example: `{"$ref": "external.yml#/parameters/Id"}` will import the content at `/parameters/Id`). This implies that partial files should have the same layout as a full spec (this is a feature as it enforces readability of partials).

### `$inline`

    { "$inline": "<file>#<pointer>"}

    {
        "$inline": "<file>#<pointer>",
        "pointer": <value>, // Overrides value at <file>#<pointer>/pointer1
        "pointer/slash": <value> // Overrides value at <file>#<pointer>/pointer/slash
    }

`$inline` is an OpenAPI extension allowing to inject a copy of another part of a document in place. Keys along the `$inline` keyword are JSON pointers (with the leading `/` removed) allowing to override some parts of the inlined content.

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

## License

Copyright 2018 Olivier Mengué

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
package openapi

import _ "embed"

// Document is the offline OpenAPI 3.1 contract served at /openapi.json.
//
//go:embed openapi.json
var Document []byte

//go:embed docs.html
var DocsHTML []byte

//go:embed docs.js
var DocsJS []byte

//go:embed docs.css
var DocsCSS []byte

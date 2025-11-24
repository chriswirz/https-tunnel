package server

import _ "embed"

// openapiJSON is the API contract. It is the single source of truth for the
// spec: the server publishes it at /openapi.json, the Swagger page in the
// frontend renders that URL, and `npm run codegen` in web/ generates the
// TypeScript client from this same file.
//
//go:embed openapi.json
var openapiJSON []byte

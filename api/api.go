// Package api embeds the OpenAPI description of the box's REST API.
package api

import _ "embed"

// OpenAPI is openapi.yaml, served at /api/v1/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte

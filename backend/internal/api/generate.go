package api

// Match CI: the Go version affects the embedded Swagger gzip bytes.
//go:generate sh -c "cd ../../.. && GOTOOLCHAIN=go1.26.7 go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config api/oapi-codegen.yaml api/openapi.yaml"

//go:generate go run github.com/FuturFusion/incus-redfish-proxy/cmd/redfish-openapi-filter openapi.filtered.yaml
//go:generate npx @redocly/openapi-cli@latest bundle --force --output openapi.bundled.yaml openapi.filtered.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config config.yaml openapi.bundled.yaml

package api

//go:generate go run github.com/FuturFusion/incus-redfish-proxy/cmd/redfish-openapi-filter openapi.filtered.yaml
//go:generate npx @redocly/openapi-cli@latest bundle --force --output openapi.bundled.yaml openapi.filtered.yaml
//go:generate go run github.com/FuturFusion/incus-redfish-proxy/cmd/redfish-openapi-apply-annotations components openapi.bundled.yaml openapi.patched-components.yaml
//go:generate npx @redocly/openapi-cli@latest bundle --force --output openapi.bundled2.yaml openapi.patched-components.yaml
//go:generate go run github.com/FuturFusion/incus-redfish-proxy/cmd/redfish-openapi-apply-annotations paths openapi.bundled2.yaml openapi.patched-paths.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config config.yaml openapi.patched-paths.yaml

package api

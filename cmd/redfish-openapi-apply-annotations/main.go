package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"go.yaml.in/yaml/v4"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: redfish-openapi-apply-annotations [components|paths] <source-filename> <output-filename>")
		os.Exit(1)
	}

	operation := os.Args[1]
	source := os.Args[2]
	output := os.Args[3]

	if !slices.Contains([]string{"components", "paths"}, operation) {
		fmt.Println("usage: redfish-openapi-apply-annotations [components|paths] <source-filename> <output-filename>")
		os.Exit(1)
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(source)
	die(err)

	switch operation {
	case "components":
		for name, schema := range doc.Components.Schemas {
			if !strings.Contains(schema.Value.Description, "@Redfish.Settings") {
				continue
			}

			if schema.Value.Properties == nil {
				schema.Value.Properties = openapi3.Schemas{}
			}

			_, has := schema.Value.Properties["@Redfish.Settings"]
			if has {
				continue
			}

			settingsRef := "http://redfish.dmtf.org/schemas/v1/Settings.v1_5_0.yaml#/components/schemas/Settings_v1_5_0_Settings"
			schema.Value.Properties["@Redfish.Settings"] = &openapi3.SchemaRef{Ref: settingsRef}

			doc.Components.Schemas[name] = schema
		}

	case "paths":
		for path, item := range doc.Paths.Map() {
			get := item.Get
			if get == nil || get.Responses == nil {
				continue
			}

			ok := get.Responses.Status(200)
			if ok == nil || ok.Value == nil {
				continue
			}

			mimetype := ok.Value.Content.Get("application/json")
			if mimetype == nil || mimetype.Schema == nil || mimetype.Schema.Value == nil {
				continue
			}

			if !strings.Contains(mimetype.Schema.Value.Description, "@Redfish.Settings") {
				continue
			}

			settingsPath := path + "/Settings"
			if doc.Paths.Value(settingsPath) != nil {
				continue
			}

			doc.Paths.Set(settingsPath, settingsItem(item, mimetype.Schema.Ref))
		}
	}

	yamlOut, err := doc.MarshalYAML()
	die(err)

	f, err := os.OpenFile(output, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	die(err)
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	defer func() {
		_ = enc.Close()
	}()

	err = enc.Encode(yamlOut)
	die(err)
}

func settingsItem(item *openapi3.PathItem, ref string) *openapi3.PathItem {
	body := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Ref: ref})

	resp := openapi3.NewResponses()
	resp.Set("200", &openapi3.ResponseRef{
		Value: openapi3.NewResponse().WithDescription("Settings resource").WithContent(body),
	})

	rb := &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().WithContent(body)}

	var parameters openapi3.Parameters

	for _, p := range item.Parameters {
		v := p.Value
		if v == nil || v.In != openapi3.ParameterInPath {
			continue
		}

		parameters = append(parameters, p)
	}

	return &openapi3.PathItem{
		Get:        &openapi3.Operation{Responses: resp},
		Patch:      &openapi3.Operation{RequestBody: rb, Responses: resp},
		Put:        &openapi3.Operation{RequestBody: rb, Responses: resp},
		Parameters: parameters,
	}
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/imagegen"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
	"github.com/stretchr/testify/require"
)

type catalogueVision struct{}

func (catalogueVision) Describe(context.Context, []vision.Image, string) (string, error) {
	panic("catalogue inspection must not call provider")
}

func TestDefaultRegistryMultimodalSchemasReachModelCatalogue(t *testing.T) {
	registry := tools.NewDefaultRegistry(tools.FactoryDeps{CWD: t.TempDir(), VisionDescriber: catalogueVision{}, ExtraTools: []tools.Tool{imagegen.New(config.ImageGenConfig{})}})
	registry.SetExecutionGuard(func(context.Context, tools.Tool, json.RawMessage) (json.RawMessage, error) {
		panic("catalogue inspection must not execute tools")
	})
	a := &Agent{toolRegistry: registry}
	found := map[string]bool{}
	for _, definition := range a.buildToolDefs() {
		if definition.Name != "vision_describe" && definition.Name != "image_gen" {
			continue
		}
		found[definition.Name] = true
		require.NotEqual(t, "Execute tool: "+definition.Name, definition.Description)
		var schema struct {
			Type                 string
			Properties           map[string]json.RawMessage
			AdditionalProperties bool
		}
		require.NoError(t, json.Unmarshal(definition.Parameters, &schema))
		require.Equal(t, "object", schema.Type)
		require.False(t, schema.AdditionalProperties)
		require.Contains(t, schema.Properties, "prompt")
		if definition.Name == "vision_describe" {
			require.Contains(t, schema.Properties, "file")
		} else {
			require.Contains(t, schema.Properties, "provider")
		}
	}
	require.True(t, found["vision_describe"])
	require.True(t, found["image_gen"])
}

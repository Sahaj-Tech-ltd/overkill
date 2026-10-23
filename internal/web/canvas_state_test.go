package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func canvasLine(action string, payload any) string {
	raw, _ := json.Marshal(map[string]any{action: payload})
	return string(raw)
}
func modelApply(t *testing.T, state CanvasState, line string) CanvasState {
	t.Helper()
	m, err := parseCanvasMutation(state.SessionID, line)
	require.NoError(t, err)
	state, err = applyCanvasMutation(state, m)
	require.NoError(t, err)
	return state
}
func TestCanvasModelCatalogLifecycle(t *testing.T) {
	state := CanvasState{SessionID: "s", Surfaces: []CanvasSurface{}}
	state = modelApply(t, state, `{"createSurface":{"surfaceId":"view","catalog":"overkill.canvas.v1"}}`)
	state = modelApply(t, state, `{"surfaceUpdate":{"surfaceId":"view","components":[{"id":"root","type":"Column","children":["title","divider"]},{"id":"title","type":"Heading","textPath":"/title"},{"id":"divider","type":"Divider"}]}}`)
	state = modelApply(t, state, `{"dataModelUpdate":{"surfaceId":"view","contents":{"title":"Hello","exact":9007199254740991}}}`)
	state = modelApply(t, state, `{"beginRendering":{"surfaceId":"view","root":"root"}}`)
	require.Equal(t, int64(4), state.Revision)
	require.True(t, state.Surfaces[0].Rendering)
	require.Equal(t, json.Number("9007199254740991"), state.Surfaces[0].Data["exact"])
	m, err := parseCanvasMutation("s", `{"surfaceUpdate":{"surfaceId":"view","components":[{"id":"other","type":"Divider"}]}}`)
	require.NoError(t, err)
	_, err = applyCanvasMutation(state, m)
	require.ErrorContains(t, err, "root")
	state = modelApply(t, state, `{"deleteSurface":{"surfaceId":"view"}}`)
	require.Empty(t, state.Surfaces)
	require.Equal(t, int64(5), state.Revision)
}
func TestCanvasModelRejectsUnsupportedAndAmbiguousInput(t *testing.T) {
	invalid := []string{
		`{"createSurface":{"surfaceId":"v"}}`,
		`{"createSurface":{"surfaceId":"v","catalog":"upstream"}}`,
		`{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1","Catalog":"overkill.canvas.v1"}}`,
		`{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1","sessionId":"elsewhere"}}`,
		`{"deleteSurface":{"surfaceId":"v","extra":false}}`,
		`{"dataModelUpdate":{"surfaceId":"v","contents":null}}`,
		`{"dataModelUpdate":{"surfaceId":"v","contents":{"a":1,"a":2}}}`,
		`{"surfaceUpdate":{"surfaceId":"v","components":null}}`,
	}
	badComponents := []string{
		`{"id":"a","type":"Button"}`, `{"id":"a","type":"Text"}`, `{"id":"a","type":"Text","text":null}`,
		`{"id":"a","type":"Text","text":"x","textPath":"/x"}`, `{"id":"a","type":"Text","text":"x","children":[]}`,
		`{"id":"a","type":"Text","textPath":"x"}`, `{"id":"a","type":"Text","textPath":"/~2"}`,
		`{"id":"a","type":"Divider","text":"x"}`, `{"id":"a","type":"Column"}`, `{"id":"a","type":"Column","children":null}`,
		`{"id":"a","type":"Divider","Type":"Text"}`, `{"id":"a","type":"Divider","id":"b"}`,
	}
	for _, c := range badComponents {
		invalid = append(invalid, `{"surfaceUpdate":{"surfaceId":"v","components":[`+c+`]}}`)
	}
	for _, line := range invalid {
		_, err := parseCanvasMutation("s", line)
		require.Error(t, err, line)
	}
}
func TestCanvasModelGraphAndBounds(t *testing.T) {
	for _, components := range [][]CanvasComponent{
		{{ID: "a", Type: "Divider"}, {ID: "a", Type: "Divider"}},
		{{ID: "a", Type: "Column", Children: []string{"absent"}}},
		{{ID: "ok", Type: "Divider"}, {ID: "a", Type: "Row", Children: []string{"b"}}, {ID: "b", Type: "Row", Children: []string{"a"}}},
	} {
		require.Error(t, validateCanvasGraph(components, ""))
	}
	nodes := []CanvasComponent{}
	for i := 0; i < 16; i++ {
		c := CanvasComponent{ID: fmt.Sprint(i), Type: "Column", Children: []string{}}
		if i < 15 {
			c.Children = []string{fmt.Sprint(i + 1)}
		}
		nodes = append(nodes, c)
	}
	require.NoError(t, validateCanvasGraph(nodes, "0"))
	nodes[15].Children = []string{"16"}
	nodes = append(nodes, CanvasComponent{ID: "16", Type: "Divider"})
	require.ErrorContains(t, validateCanvasGraph(nodes, "0"), "depth")
	for _, n := range []int{CanvasMaxTextBytes, CanvasMaxTextBytes + 1} {
		_, err := parseCanvasMutation("s", canvasLine("surfaceUpdate", map[string]any{"surfaceId": "v", "components": []any{map[string]any{"id": "a", "type": "Text", "text": strings.Repeat("x", n)}}}))
		if n == CanvasMaxTextBytes {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	// A small DAG can expand exponentially despite the distinct-node/depth cap.
	nodes = nil
	for i := 0; i < 13; i++ {
		c := CanvasComponent{ID: fmt.Sprint(i), Type: "Row", Children: []string{}}
		if i < 12 {
			c.Children = []string{fmt.Sprint(i + 1), fmt.Sprint(i + 1)}
		}
		nodes = append(nodes, c)
	}
	require.NoError(t, validateCanvasGraph(nodes, ""))
	require.ErrorContains(t, validateCanvasGraph(nodes, "0"), "rendered nodes")
	state := CanvasState{SessionID: "s", Surfaces: []CanvasSurface{}}
	for i := 0; i < CanvasMaxSurfaces; i++ {
		state = modelApply(t, state, canvasLine("createSurface", map[string]any{"surfaceId": fmt.Sprint(i), "catalog": CatalogCanvasV1}))
	}
	m, err := parseCanvasMutation("s", canvasLine("createSurface", map[string]any{"surfaceId": "overflow", "catalog": CatalogCanvasV1}))
	require.NoError(t, err)
	_, err = applyCanvasMutation(state, m)
	require.Error(t, err)
	_, err = parseCanvasMutation("s", canvasLine("dataModelUpdate", map[string]any{"surfaceId": "v", "contents": map[string]any{"big": strings.Repeat("x", CanvasMaxDataBytes)}}))
	require.Error(t, err)
	nested := strings.Repeat(`{"x":`, 70) + `1` + strings.Repeat(`}`, 70)
	_, err = parseCanvasMutation("s", `{"dataModelUpdate":{"surfaceId":"v","contents":`+nested+`}}`)
	require.ErrorContains(t, err, "nesting")
}
func TestCanvasModelBoundTextLimit(t *testing.T) {
	state := CanvasState{SessionID: "s", Surfaces: []CanvasSurface{}}
	state = modelApply(t, state, `{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1"}}`)
	state = modelApply(t, state, `{"surfaceUpdate":{"surfaceId":"v","components":[{"id":"a","type":"Text","textPath":"/a~1b/~0"}]}}`)
	m, err := parseCanvasMutation("s", canvasLine("dataModelUpdate", map[string]any{"surfaceId": "v", "contents": map[string]any{"a/b": map[string]any{"~": strings.Repeat("x", CanvasMaxTextBytes+1)}}}))
	require.NoError(t, err)
	_, err = applyCanvasMutation(state, m)
	require.ErrorContains(t, err, "bound text")
}

func TestCanvasModelExactNodeAndDataBounds(t *testing.T) {
	for _, n := range []int{CanvasMaxComponents, CanvasMaxComponents + 1} {
		components := make([]map[string]any, n)
		for i := range components {
			components[i] = map[string]any{"id": fmt.Sprint(i), "type": "Divider"}
		}
		_, err := parseCanvasMutation("s", canvasLine("surfaceUpdate", map[string]any{"surfaceId": "v", "components": components}))
		if n == CanvasMaxComponents {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	// {"x":""} is eight bytes before its string contents.
	for _, n := range []int{CanvasMaxDataBytes, CanvasMaxDataBytes + 1} {
		_, err := parseCanvasMutation("s", canvasLine("dataModelUpdate", map[string]any{"surfaceId": "v", "contents": map[string]any{"x": strings.Repeat("x", n-8)}}))
		if n == CanvasMaxDataBytes {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
}

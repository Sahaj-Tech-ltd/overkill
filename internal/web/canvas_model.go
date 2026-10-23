package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	CanvasMaxRevision      int64 = 9007199254740991
	CatalogCanvasV1              = "overkill.canvas.v1"
	CanvasMaxComponents          = 128
	CanvasMaxDepth               = 16
	CanvasMaxTextBytes           = 16 * 1024
	CanvasMaxDataBytes           = 64 * 1024
	CanvasMaxStateBytes          = 256 * 1024
	CanvasMaxSurfaces            = 16
	CanvasMaxRenderedNodes       = 2048
)

var ErrCanvasInvalid = errors.New("invalid canvas update")
var ErrCanvasSessionNotFound = errors.New("canvas session not found")

// CanvasState is the durable overkill.canvas.v1 document, not upstream A2UI.
type CanvasState struct {
	SessionID string          `json:"sessionId"`
	Revision  int64           `json:"revision"`
	Surfaces  []CanvasSurface `json:"surfaces"`
}
type CanvasSurface struct {
	SurfaceID  string            `json:"surfaceId"`
	Catalog    string            `json:"catalog"`
	Root       string            `json:"root"`
	Components []CanvasComponent `json:"components"`
	Data       map[string]any    `json:"data"`
	Rendering  bool              `json:"rendering"`
}
type CanvasComponent struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Text     *string  `json:"text,omitempty"`
	TextPath string   `json:"textPath,omitempty"`
	Children []string `json:"children,omitempty"`
}

type canvasMutation struct {
	action, surfaceID, catalog, root string
	components                       []CanvasComponent
	data                             map[string]any
}

func canvasFields(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := decodeCanvasJSON(raw, &fields); err != nil {
		return nil, err
	}
	for key := range fields {
		known := false
		for _, name := range allowed {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown field %q", key)
		}
	}
	return fields, nil
}
func canvasString(fields map[string]json.RawMessage, key string) (string, error) {
	var value string
	raw := fields[key]
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}
func parseCanvasMutation(sid, line string) (canvasMutation, error) {
	var m canvasMutation
	if !canvasIdentifier(sid) {
		return m, fmt.Errorf("valid sessionId required")
	}
	if len(line) > CanvasMaxStateBytes {
		return m, fmt.Errorf("event exceeds %d bytes", CanvasMaxStateBytes)
	}
	action, target, err := validateCanvasEvent(line, sid, "")
	if err != nil {
		return m, err
	}
	var envelope map[string]json.RawMessage
	if err := decodeCanvasJSON([]byte(line), &envelope); err != nil {
		return m, err
	}
	allowed := []string{"surfaceId", "sessionId"}
	switch action {
	case "createSurface":
		allowed = append(allowed, "catalog")
	case "surfaceUpdate":
		allowed = append(allowed, "components")
	case "beginRendering":
		allowed = append(allowed, "root")
	case "dataModelUpdate":
		allowed = append(allowed, "contents")
	}
	fields, err := canvasFields(envelope[action], allowed...)
	if err != nil {
		return m, err
	}
	m.action, m.surfaceID = action, target
	switch action {
	case "createSurface":
		m.catalog, err = canvasString(fields, "catalog")
		if err == nil && m.catalog != CatalogCanvasV1 {
			err = fmt.Errorf("unsupported catalog; expected %s", CatalogCanvasV1)
		}
	case "beginRendering":
		m.root, err = canvasString(fields, "root")
		if err == nil && !canvasIdentifier(m.root) {
			err = fmt.Errorf("valid root required")
		}
	case "surfaceUpdate":
		raw := fields["components"]
		if len(raw) == 0 || raw[0] != '[' {
			return m, fmt.Errorf("components must be an array")
		}
		var components []json.RawMessage
		if err = json.Unmarshal(raw, &components); err != nil {
			return m, err
		}
		if len(components) > CanvasMaxComponents {
			return m, fmt.Errorf("too many components")
		}
		m.components = make([]CanvasComponent, 0, len(components))
		for _, component := range components {
			c, e := parseCanvasComponent(component)
			if e != nil {
				return m, e
			}
			m.components = append(m.components, c)
		}
		err = validateCanvasGraph(m.components, "")
	case "dataModelUpdate":
		raw := fields["contents"]
		err = decodeCanvasJSON(raw, &m.data)
		if err == nil {
			if e := normalizeCanvasNumbers(m.data); e != nil {
				return m, e
			}
			encoded, e := json.Marshal(m.data)
			if e != nil {
				return m, e
			}
			if len(encoded) > CanvasMaxDataBytes {
				return m, fmt.Errorf("contents exceeds %d bytes", CanvasMaxDataBytes)
			}
		}
	}
	return m, err
}

func parseCanvasComponent(raw json.RawMessage) (CanvasComponent, error) {
	var c CanvasComponent
	fields, err := canvasFields(raw, "id", "type", "text", "textPath", "children")
	if err != nil {
		return c, err
	}
	c.ID, err = canvasString(fields, "id")
	if err != nil || !canvasIdentifier(c.ID) {
		return c, fmt.Errorf("valid component id required")
	}
	c.Type, err = canvasString(fields, "type")
	if err != nil {
		return c, err
	}
	_, text := fields["text"]
	_, path := fields["textPath"]
	_, children := fields["children"]
	switch c.Type {
	case "Text", "Heading":
		if text == path || children {
			return c, fmt.Errorf("%s requires exactly text or textPath", c.Type)
		}
		if text {
			v, e := canvasString(fields, "text")
			if e != nil {
				return c, e
			}
			if len(v) > CanvasMaxTextBytes {
				return c, fmt.Errorf("text exceeds %d bytes", CanvasMaxTextBytes)
			}
			c.Text = &v
		}
		if path {
			c.TextPath, err = canvasString(fields, "textPath")
			if err != nil {
				return c, err
			}
			if !validCanvasPointer(c.TextPath) {
				return c, fmt.Errorf("textPath must be a JSON pointer")
			}
		}
	case "Column", "Row":
		if text || path || !children {
			return c, fmt.Errorf("%s requires children only", c.Type)
		}
		if len(fields["children"]) == 0 || fields["children"][0] != '[' {
			return c, fmt.Errorf("children must be an array")
		}
		if err = json.Unmarshal(fields["children"], &c.Children); err != nil {
			return c, err
		}
		if len(c.Children) > CanvasMaxComponents {
			return c, fmt.Errorf("too many children")
		}
		for _, id := range c.Children {
			if !canvasIdentifier(id) {
				return c, fmt.Errorf("valid child id required")
			}
		}
	case "Divider":
		if text || path || children {
			return c, fmt.Errorf("Divider has no properties")
		}
	default:
		return c, fmt.Errorf("unsupported component type %q", c.Type)
	}
	return c, nil
}
func validCanvasPointer(path string) bool {
	if !strings.HasPrefix(path, "/") || len(path) > 256 {
		return false
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '~' {
			i++
			if i >= len(path) || (path[i] != '0' && path[i] != '1') {
				return false
			}
		}
	}
	return true
}
func validateCanvasGraph(components []CanvasComponent, root string) error {
	if len(components) > CanvasMaxComponents {
		return fmt.Errorf("too many components")
	}
	nodes := make(map[string]CanvasComponent, len(components))
	for _, c := range components {
		if _, ok := nodes[c.ID]; ok {
			return fmt.Errorf("duplicate component id %q", c.ID)
		}
		nodes[c.ID] = c
	}
	if root != "" {
		if _, ok := nodes[root]; !ok {
			return fmt.Errorf("rendering root does not exist")
		}
	}
	colors := map[string]int{}
	heights := map[string]int{}
	var visit func(string) (int, error)
	visit = func(id string) (int, error) {
		c, ok := nodes[id]
		if !ok {
			return 0, fmt.Errorf("missing component %q", id)
		}
		if colors[id] == 1 {
			return 0, fmt.Errorf("component cycle")
		}
		if colors[id] == 2 {
			return heights[id], nil
		}
		colors[id] = 1
		height := 1
		for _, child := range c.Children {
			h, e := visit(child)
			if e != nil {
				return 0, e
			}
			if h+1 > height {
				height = h + 1
			}
		}
		if height > CanvasMaxDepth {
			return 0, fmt.Errorf("component depth exceeds %d", CanvasMaxDepth)
		}
		colors[id] = 2
		heights[id] = height
		return height, nil
	}
	for id := range nodes {
		if _, err := visit(id); err != nil {
			return err
		}
	}
	if root != "" {
		counts := map[string]int{}
		var count func(string) int
		count = func(id string) int {
			if n, ok := counts[id]; ok {
				return n
			}
			n := 1
			for _, child := range nodes[id].Children {
				n += count(child)
				if n > CanvasMaxRenderedNodes {
					n = CanvasMaxRenderedNodes + 1
					break
				}
			}
			counts[id] = n
			return n
		}
		if count(root) > CanvasMaxRenderedNodes {
			return fmt.Errorf("rendered nodes exceed %d", CanvasMaxRenderedNodes)
		}
	}
	return nil
}

func applyCanvasMutation(state CanvasState, m canvasMutation) (CanvasState, error) {
	if state.Revision < 0 || state.Revision >= CanvasMaxRevision {
		return state, fmt.Errorf("canvas revision exhausted or invalid")
	}
	index := -1
	for i, surface := range state.Surfaces {
		if surface.SurfaceID == m.surfaceID {
			index = i
			break
		}
	}
	if m.action == "createSurface" {
		if index >= 0 {
			return state, fmt.Errorf("surface already exists")
		}
		if len(state.Surfaces) >= CanvasMaxSurfaces {
			return state, fmt.Errorf("too many surfaces")
		}
		state.Surfaces = append(state.Surfaces, CanvasSurface{SurfaceID: m.surfaceID, Catalog: m.catalog, Components: []CanvasComponent{}, Data: map[string]any{}})
	} else {
		if index < 0 {
			return state, fmt.Errorf("surface does not exist")
		}
		surface := &state.Surfaces[index]
		switch m.action {
		case "surfaceUpdate":
			root := ""
			if surface.Rendering {
				root = surface.Root
			}
			if err := validateCanvasGraph(m.components, root); err != nil {
				return state, err
			}
			surface.Components = m.components
		case "dataModelUpdate":
			surface.Data = m.data
		case "beginRendering":
			if err := validateCanvasGraph(surface.Components, m.root); err != nil {
				return state, err
			}
			surface.Root = m.root
			surface.Rendering = true
		case "deleteSurface":
			state.Surfaces = append(state.Surfaces[:index], state.Surfaces[index+1:]...)
		}
	}
	for _, surface := range state.Surfaces {
		if err := validateCanvasBoundText(surface); err != nil {
			return state, err
		}
	}
	state.Revision++
	raw, err := json.Marshal(state)
	if err != nil {
		return state, err
	}
	if len(raw) > CanvasMaxStateBytes {
		return state, fmt.Errorf("state exceeds %d bytes", CanvasMaxStateBytes)
	}
	return state, nil
}

func validateCanvasBoundText(surface CanvasSurface) error {
	for _, c := range surface.Components {
		if c.TextPath == "" {
			continue
		}
		var value any = surface.Data
		for _, segment := range strings.Split(c.TextPath[1:], "/") {
			key := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
			switch data := value.(type) {
			case map[string]any:
				value = data[key]
			case []any:
				index, err := strconv.Atoi(key)
				if err != nil || index < 0 || index >= len(data) || strconv.Itoa(index) != key {
					value = nil
				} else {
					value = data[index]
				}
			default:
				value = nil
			}
		}
		var text string
		switch v := value.(type) {
		case string:
			text = v
		case json.Number:
			text = string(v)
		}
		if len(text) > CanvasMaxTextBytes {
			return fmt.Errorf("bound text exceeds %d bytes", CanvasMaxTextBytes)
		}
	}
	return nil
}

// Numbers intentionally use the same finite IEEE754 range as the browser.
// Fractional decimals are normalized to their nearest float64 representation;
// values outside the safe integer magnitude are rejected before persistence.
func normalizeCanvasNumbers(value any) error {
	var normalize func(any) (any, error)
	normalize = func(value any) (any, error) {
		switch v := value.(type) {
		case json.Number:
			number, err := strconv.ParseFloat(string(v), 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > 9007199254740991 {
				return nil, fmt.Errorf("data number outside finite browser-safe range")
			}
			return json.Number(strconv.FormatFloat(number, 'f', -1, 64)), nil
		case map[string]any:
			for key, item := range v {
				normal, err := normalize(item)
				if err != nil {
					return nil, err
				}
				v[key] = normal
			}
		case []any:
			for i, item := range v {
				normal, err := normalize(item)
				if err != nil {
					return nil, err
				}
				v[i] = normal
			}
		}
		return value, nil
	}
	_, err := normalize(value)
	return err
}

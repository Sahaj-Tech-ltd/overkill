package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArgumentSizePreflightMatchesJSONEncoding(t *testing.T) {
	input := json.RawMessage(`{"big":9007199254740993,"strings":["<>&\u2028\u2029","quote\"slash\\\n\t\u0001","雪"],"empty":[],"null":null,"true":true,"false":false}`)
	value, err := decodeCallArguments(input)
	require.NoError(t, err)
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	size, err := encodedJSONSize(value, len(encoded))
	require.NoError(t, err)
	require.Equal(t, len(encoded), size)
	_, err = encodedJSONSize(value, len(encoded)-1)
	require.ErrorContains(t, err, "byte limit")
	require.Contains(t, string(encoded), `9007199254740993`)
}
func TestOutboundRPCTransportCapRejectsBeforeWrite(t *testing.T) {
	var output strings.Builder
	connection := newJSONRPCConn(&output, strings.NewReader(""))
	err := connection.writeMessage(&jsonrpcMessage{JSONRPC: "2.0", Method: "large", Params: json.RawMessage(`"` + strings.Repeat("x", maxMCPLineBytes) + `"`)})
	require.ErrorContains(t, err, "transport byte limit")
	require.Zero(t, output.Len())
}

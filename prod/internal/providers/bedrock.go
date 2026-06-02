package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// BedrockProvider implements Provider for AWS Bedrock (Converse API).
// Uses SigV4 signing via AWS SDK v2.
type BedrockProvider struct {
	*BaseProvider
	cfg    aws.Config
	region string
	signer *v4.Signer
	client *http.Client
	mu     sync.Mutex
}

func NewBedrockProvider(region, accessKeyID, secretAccessKey string, models []Model) (*BedrockProvider, error) {
	if region == "" {
		region = "us-east-1"
	}

	var cfg aws.Config
	var err error

	if accessKeyID != "" && secretAccessKey != "" {
		cfg, err = awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion(region),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				accessKeyID, secretAccessKey, "",
			)),
		)
	} else {
		cfg, err = awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion(region),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("bedrock: loading AWS config: %w", err)
	}

	return &BedrockProvider{
		BaseProvider: &BaseProvider{
			name:    "bedrock",
			baseURL: fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", region),
			models:  models,
			headers: make(map[string]string),
		},
		cfg:    cfg,
		region: region,
		signer: v4.NewSigner(),
		client: &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// --- Bedrock Converse API shapes ---

type bedrockConverseReq struct {
	Messages        []bedrockConvMsg  `json:"messages"`
	System          []bedrockSysBlock `json:"system,omitempty"`
	InferenceConfig *bedrockInfCfg    `json:"inferenceConfig,omitempty"`
	ToolConfig      *bedrockToolCfg   `json:"toolConfig,omitempty"`
}

type bedrockToolCfg struct {
	Tools []bedrockTool `json:"tools"`
}

type bedrockTool struct {
	ToolSpec bedrockToolSpec `json:"toolSpec"`
}

type bedrockToolSpec struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema bedrockToolSchema `json:"inputSchema"`
}

type bedrockToolSchema struct {
	JSON json.RawMessage `json:"json"`
}

type bedrockConvMsg struct {
	Role    string         `json:"role"`
	Content []bedrockBlock `json:"content"`
}

type bedrockBlock struct {
	Text string `json:"text,omitempty"`
}

type bedrockSysBlock struct {
	Text string `json:"text"`
}

type bedrockInfCfg struct {
	MaxTokens *int `json:"maxTokens,omitempty"`
}

type bedrockConverseResp struct {
	Output struct {
		Message bedrockConvMsg `json:"message"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

// Complete sends a request to the Bedrock Converse endpoint.
func (p *BedrockProvider) Complete(ctx context.Context, req Request) (Response, error) {
	bReq := bedrockConverseReq{
		Messages: convertToBedrockMsgs(req.Messages),
	}
	if req.SystemPrompt != "" {
		bReq.System = []bedrockSysBlock{{Text: req.SystemPrompt}}
	}
	if req.MaxTokens > 0 {
		bReq.InferenceConfig = &bedrockInfCfg{MaxTokens: &req.MaxTokens}
	}
	if len(req.Tools) > 0 {
		bReq.ToolConfig = &bedrockToolCfg{
			Tools: convertToBedrockTools(req.Tools),
		}
	}

	body, err := json.Marshal(bReq)
	if err != nil {
		return Response{}, fmt.Errorf("bedrock: marshal: %w", err)
	}

	// Model ID: the user passes the full Bedrock model ID (e.g.
	// "us.anthropic.claude-sonnet-4-20250514-v1:0").
	modelID := req.Model
	url := fmt.Sprintf("%s/model/%s/converse", p.baseURL, modelID)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("bedrock: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	creds, err := p.cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("bedrock: credentials: %w", err)
	}

	// Compute payload hash for SigV4
	h := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(h[:])

	// SignHTTP needs serialised access
	p.mu.Lock()
	signErr := p.signer.SignHTTP(ctx, creds, httpReq, payloadHash, "bedrock", p.region, time.Now())
	p.mu.Unlock()
	if signErr != nil {
		return Response{}, fmt.Errorf("bedrock: sign: %w", signErr)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("bedrock: do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024*1024))
	if err != nil {
		return Response{}, fmt.Errorf("bedrock: read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("bedrock: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var bResp bedrockConverseResp
	if err := json.Unmarshal(respBody, &bResp); err != nil {
		return Response{}, fmt.Errorf("bedrock: unmarshal: %w", err)
	}

	return Response{
		Content: extractBedrockText(bResp.Output.Message),
		Usage: Usage{
			InputTokens:  bResp.Usage.InputTokens,
			OutputTokens: bResp.Usage.OutputTokens,
		},
	}, nil
}

// Stream implements basic streaming via the Bedrock ConverseStream API.
// Returns a channel of Chunk events. If the ConverseStream endpoint
// returns an error, the channel is closed with the error on the first
// chunk.
func (p *BedrockProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	bReq := bedrockConverseReq{
		Messages: convertToBedrockMsgs(req.Messages),
	}
	if req.SystemPrompt != "" {
		bReq.System = []bedrockSysBlock{{Text: req.SystemPrompt}}
	}
	if req.MaxTokens > 0 {
		bReq.InferenceConfig = &bedrockInfCfg{MaxTokens: &req.MaxTokens}
	}
	if len(req.Tools) > 0 {
		bReq.ToolConfig = &bedrockToolCfg{
			Tools: convertToBedrockTools(req.Tools),
		}
	}

	body, err := json.Marshal(bReq)
	if err != nil {
		return nil, fmt.Errorf("bedrock: stream marshal: %w", err)
	}

	modelID := req.Model
	url := fmt.Sprintf("%s/model/%s/converse-stream", p.baseURL, modelID)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: stream new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	creds, err := p.cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("bedrock: stream credentials: %w", err)
	}

	h := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(h[:])

	p.mu.Lock()
	signErr := p.signer.SignHTTP(ctx, creds, httpReq, payloadHash, "bedrock", p.region, time.Now())
	p.mu.Unlock()
	if signErr != nil {
		return nil, fmt.Errorf("bedrock: stream sign: %w", signErr)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bedrock: stream do: %w", err)
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("bedrock: stream HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan Chunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var fullText strings.Builder
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()
			if !strings.HasPrefix(line, "{") {
				continue
			}

			var event bedrockConverseStreamEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}

			if event.ContentBlockDelta != nil && event.ContentBlockDelta.Delta != nil {
				if text := event.ContentBlockDelta.Delta.Text; text != "" {
					fullText.WriteString(text)
					if !sendChunk(ctx, ch, Chunk{Content: text}) {
						return
					}
				}
			}

			if event.MessageStop != nil {
				usage := &Usage{}
				if event.AmazonBedrockMetadata != nil && event.AmazonBedrockMetadata.Usage != nil {
					usage.InputTokens = event.AmazonBedrockMetadata.Usage.InputTokens
					usage.OutputTokens = event.AmazonBedrockMetadata.Usage.OutputTokens
				}
				logSendChunk(ctx, ch, Chunk{Done: true, Usage: usage})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			logSendChunk(ctx, ch, Chunk{Err: fmt.Errorf("bedrock stream: %w", err)})
			return
		}
		logSendChunk(ctx, ch, Chunk{Done: true})
	}()

	return ch, nil
}

// bedrockConverseStreamEvent is a single JSON-line event from the ConverseStream API.
type bedrockConverseStreamEvent struct {
	ContentBlockDelta *struct {
		Delta *struct {
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"contentBlockDelta,omitempty"`
	MessageStop             *struct{}              `json:"messageStop,omitempty"`
	AmazonBedrockMetadata   *bedrockStreamMetadata `json:"metadata,omitempty"`
	InternalServerException *struct {
		Message string `json:"message"`
	} `json:"internalServerException,omitempty"`
	ModelStreamErrorException *struct {
		Message string `json:"message"`
	} `json:"modelStreamErrorException,omitempty"`
	ValidationException *struct {
		Message string `json:"message"`
	} `json:"validationException,omitempty"`
	ThrottlingException *struct {
		Message string `json:"message"`
	} `json:"throttlingException,omitempty"`
}

type bedrockStreamMetadata struct {
	Usage *struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage,omitempty"`
}

func convertToBedrockMsgs(msgs []Message) []bedrockConvMsg {
	out := make([]bedrockConvMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, bedrockConvMsg{
			Role:    m.Role,
			Content: []bedrockBlock{{Text: m.Content}},
		})
	}
	return out
}

func convertToBedrockTools(tools []Tool) []bedrockTool {
	out := make([]bedrockTool, 0, len(tools))
	for _, t := range tools {
		schema := t.Parameters
		if schema == nil {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, bedrockTool{
			ToolSpec: bedrockToolSpec{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: bedrockToolSchema{JSON: schema},
			},
		})
	}
	return out
}

func extractBedrockText(msg bedrockConvMsg) string {
	var b strings.Builder
	for _, blk := range msg.Content {
		if blk.Text != "" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

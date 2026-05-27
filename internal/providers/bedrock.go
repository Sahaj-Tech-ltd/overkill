package providers

import (
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
	Messages        []bedrockConvMsg    `json:"messages"`
	System          []bedrockSysBlock   `json:"system,omitempty"`
	InferenceConfig *bedrockInfCfg      `json:"inferenceConfig,omitempty"`
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

	respBody, err := io.ReadAll(resp.Body)
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

// Stream is not yet implemented for Bedrock.
func (p *BedrockProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	return nil, fmt.Errorf("bedrock: streaming not yet implemented")
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

func extractBedrockText(msg bedrockConvMsg) string {
	var b strings.Builder
	for _, blk := range msg.Content {
		if blk.Text != "" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

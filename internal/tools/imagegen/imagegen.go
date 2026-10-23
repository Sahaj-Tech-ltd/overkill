// Package imagegen provides text-to-image generation tools for Overkill.
//
// Supported providers:
//   - openai: OpenAI DALL-E 3
//   - stability: Stability AI (Stable Diffusion)
//   - replicate: Replicate (Flux Schnell, free-ish)
package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Tool implements tools.Tool for text-to-image generation.
type Tool struct {
	cfg config.ImageGenConfig
}

// New creates a new image generation tool with the given config.
func New(cfg config.ImageGenConfig) *Tool {
	return &Tool{cfg: cfg}
}

func (t *Tool) Name() string { return "image_gen" }

func (t *Tool) Description() string {
	return "Generate images with a configured OpenAI, Stability, or Replicate provider and return private temporary file paths. Requires that provider's credentials; remote image downloads allow only public HTTPS destinations."
}

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","minLength":1,"maxLength":8192},"provider":{"type":"string","enum":["openai","stability","replicate"]},"size":{"type":"string","maxLength":32},"n":{"type":"integer","minimum":1,"maximum":4}},"required":["prompt"],"additionalProperties":false}`)
}

// ImageGenInput is the JSON input for image_gen.
type ImageGenInput struct {
	Prompt   string `json:"prompt"`
	Provider string `json:"provider"` // "openai" | "stability" | "replicate"
	Size     string `json:"size"`     // e.g. "1024x1024", "1792x1024", "1024x1792"
	N        int    `json:"n"`        // number of images (DALL-E 3 only supports 1)
}

// ImageGenOutput is the JSON output from image_gen.
type ImageGenOutput struct {
	Images   []string `json:"images"`
	Provider string   `json:"provider"`
	Prompt   string   `json:"prompt"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in ImageGenInput
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input) > 16384 {
		return nil, fmt.Errorf("image_gen: input exceeds bound")
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("image_gen: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("image_gen: one input object required")
	}
	if len(in.Prompt) > 8192 || len(in.Size) > 32 || in.N > 4 || in.N < 0 {
		return nil, fmt.Errorf("image_gen: input field exceeds bound")
	}

	if strings.TrimSpace(in.Prompt) == "" {
		return nil, fmt.Errorf("image_gen: prompt is required")
	}

	// Resolve provider: input > config
	provider := in.Provider
	if provider == "" {
		provider = t.cfg.Provider
	}
	if provider == "" {
		return json.Marshal(ImageGenOutput{
			Images:   []string{},
			Provider: "none",
			Prompt:   in.Prompt,
		})
	}

	if in.Size == "" {
		in.Size = "1024x1024"
	}
	if in.N <= 0 {
		in.N = 1
	}

	switch provider {
	case "openai":
		return t.generateOpenAI(ctx, in)
	case "stability":
		return t.generateStability(ctx, in)
	case "replicate":
		return t.generateReplicate(ctx, in)
	default:
		return nil, fmt.Errorf("image_gen: unknown provider %q (supported: openai, stability, replicate)", provider)
	}
}

// ---------------------------------------------------------------------------
// OpenAI DALL-E 3
// ---------------------------------------------------------------------------

type openaiImageRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	N      int    `json:"n"`
	Size   string `json:"size"`
}

type openaiImageResponse struct {
	Data []struct {
		URL string `json:"url"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (t *Tool) generateOpenAI(ctx context.Context, in ImageGenInput) (json.RawMessage, error) {
	apiKey := t.cfg.OpenAIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("image_gen (openai): API key not configured (set [image_gen] openai_key or OPENAI_API_KEY)")
	}

	// DALL-E 3 only supports n=1
	if in.N > 1 {
		in.N = 1
	}

	reqBody := openaiImageRequest{
		Model:  "dall-e-3",
		Prompt: in.Prompt,
		N:      in.N,
		Size:   in.Size,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("image_gen (openai): marshal: %w", err)
	}

	imageURL := os.Getenv("OPENAI_IMAGE_URL")
	if imageURL == "" {
		imageURL = "https://api.openai.com/v1/images/generations"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, imageURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("image_gen (openai): %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image_gen (openai): request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("image_gen (openai): read: %w", err)
	}

	var genResp openaiImageResponse
	if err := json.Unmarshal(body, &genResp); err != nil {
		return nil, fmt.Errorf("image_gen (openai): parse response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if genResp.Error != nil {
			errMsg = genResp.Error.Message
		}
		return nil, fmt.Errorf("image_gen (openai): %s", errMsg)
	}

	var images []string
	for _, d := range genResp.Data {
		outPath, err := downloadImage(ctx, d.URL, "overkill-img", ".png")
		if err != nil {
			return nil, fmt.Errorf("image_gen (openai): download: %w", err)
		}
		images = append(images, outPath)
	}

	return marshalOut(ImageGenOutput{
		Images:   images,
		Provider: "openai",
		Prompt:   in.Prompt,
	})
}

// ---------------------------------------------------------------------------
// Stability AI
// ---------------------------------------------------------------------------

type stabilityError struct {
	Message string `json:"message"`
	Name    string `json:"name"`
}

func (t *Tool) generateStability(ctx context.Context, in ImageGenInput) (json.RawMessage, error) {
	apiKey := t.cfg.StabilityKey
	if apiKey == "" {
		apiKey = os.Getenv("STABILITY_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("image_gen (stability): API key not configured (set [image_gen] stability_key or STABILITY_API_KEY)")
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("prompt", in.Prompt); err != nil {
		return nil, fmt.Errorf("imagegen: write prompt field: %w", err)
	}
	if err := writer.WriteField("output_format", "png"); err != nil {
		return nil, fmt.Errorf("imagegen: write output_format field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("image_gen (stability): multipart: %w", err)
	}

	stabilityURL := os.Getenv("STABILITY_URL")
	if stabilityURL == "" {
		stabilityURL = "https://api.stability.ai/v2beta/stable-image/generate/core"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stabilityURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("image_gen (stability): %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "image/*")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image_gen (stability): request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("image_gen (stability): read: %w", err)
	}

	if len(body) > maxDownloadedImageBytes {
		return nil, fmt.Errorf("image_gen (stability): image exceeds 50 MiB limit")
	}

	if resp.StatusCode != http.StatusOK {
		var serr stabilityError
		if json.Unmarshal(body, &serr) == nil && serr.Message != "" {
			return nil, fmt.Errorf("image_gen (stability): HTTP %d: %s", resp.StatusCode, serr.Message)
		}
		return nil, fmt.Errorf("image_gen (stability): HTTP %d: %s", resp.StatusCode, string(body))
	}

	outPath, err := writeGeneratedImage(body)
	if err != nil {
		return nil, fmt.Errorf("image_gen (stability): write file: %w", err)
	}

	return marshalOut(ImageGenOutput{
		Images:   []string{outPath},
		Provider: "stability",
		Prompt:   in.Prompt,
	})
}

// ---------------------------------------------------------------------------
// Replicate
// ---------------------------------------------------------------------------

type replicatePrediction struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Input  struct {
		Prompt string `json:"prompt"`
	} `json:"input"`
	Output interface{} `json:"output"`
	Error  interface{} `json:"error"`
}

type replicateCreateResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (t *Tool) generateReplicate(ctx context.Context, in ImageGenInput) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	apiToken := t.cfg.ReplicateToken
	if apiToken == "" {
		apiToken = os.Getenv("REPLICATE_API_TOKEN")
	}
	if apiToken == "" {
		return nil, fmt.Errorf("image_gen (replicate): API token not configured (set [image_gen] replicate_token or REPLICATE_API_TOKEN)")
	}

	// Create prediction
	createBody := map[string]interface{}{
		"input": map[string]string{
			"prompt": in.Prompt,
		},
	}
	payload, err := json.Marshal(createBody)
	if err != nil {
		return nil, fmt.Errorf("image_gen (replicate): marshal: %w", err)
	}

	replicateURL := os.Getenv("REPLICATE_URL")
	if replicateURL == "" {
		replicateURL = "https://api.replicate.com/v1/models/black-forest-labs/flux-schnell/predictions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, replicateURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("image_gen (replicate): %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image_gen (replicate): create: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("image_gen (replicate): read: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("image_gen (replicate): HTTP %d: %s", resp.StatusCode, string(body))
	}

	var createResp replicateCreateResponse
	if err := json.Unmarshal(body, &createResp); err != nil {
		return nil, fmt.Errorf("image_gen (replicate): parse create: %w", err)
	}

	// Poll for completion
	pollURL := fmt.Sprintf("https://api.replicate.com/v1/predictions/%s", createResp.ID)
	var pred replicatePrediction
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}

		// B021: Use a per-request child context so each poll gets its own
		// timeout rather than sharing the single 300s client timeout.
		reqCtx, reqCancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, pollURL, nil)
		if err != nil {
			reqCancel()
			return nil, fmt.Errorf("image_gen (replicate): poll: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiToken)

		resp, err := client.Do(req)
		if err != nil {
			reqCancel()
			return nil, fmt.Errorf("image_gen (replicate): poll request: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		reqCancel() // release only after consuming the response body
		if err != nil {
			return nil, fmt.Errorf("image_gen (replicate): poll read: %w", err)
		}

		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("image_gen (replicate): poll HTTP %d: %s", resp.StatusCode, string(body))
		}

		if err := json.Unmarshal(body, &pred); err != nil {
			return nil, fmt.Errorf("image_gen (replicate): parse poll: %w", err)
		}

		switch pred.Status {
		case "succeeded":
			goto done
		case "failed", "canceled":
			return nil, fmt.Errorf("image_gen (replicate): prediction %s: %v", pred.Status, pred.Error)
		}
	}

done:
	// Replicate output: could be a single URL string or []string
	var urls []string
	switch v := pred.Output.(type) {
	case string:
		urls = []string{v}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				urls = append(urls, s)
			}
		}
	default:
		return nil, fmt.Errorf("image_gen (replicate): unexpected output type %T", pred.Output)
	}

	var images []string
	for _, u := range urls {
		outPath, err := downloadImage(ctx, u, "overkill-img", ".png")
		if err != nil {
			return nil, fmt.Errorf("image_gen (replicate): download: %w", err)
		}
		images = append(images, outPath)
	}

	return marshalOut(ImageGenOutput{
		Images:   images,
		Provider: "replicate",
		Prompt:   in.Prompt,
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeGeneratedImage publishes only a complete private temporary file.
func writeGeneratedImage(body []byte) (string, error) {
	file, err := os.CreateTemp("", "overkill-img-*.png")
	if err != nil {
		return "", err
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err := file.Write(body); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	success = true
	return file.Name(), nil
}

// marshalOut serializes an ImageGenOutput, handling errors.
func marshalOut(out ImageGenOutput) (json.RawMessage, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("image_gen: marshal output: %w", err)
	}
	return raw, nil
}

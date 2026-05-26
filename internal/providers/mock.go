package providers

import (
	"context"
	"time"
)

type MockProvider struct {
	name    string
	models  []Model
	handler func(req Request) (Response, error)
}

func NewMockProvider(name string, models []Model, handler func(Request) (Response, error)) *MockProvider {
	return &MockProvider{
		name:    name,
		models:  models,
		handler: handler,
	}
}

func (m *MockProvider) Name() string {
	return m.name
}

func (m *MockProvider) Complete(_ context.Context, req Request) (Response, error) {
	return m.handler(req)
}

func (m *MockProvider) Stream(_ context.Context, req Request) (<-chan Chunk, error) {
	resp, err := m.handler(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan Chunk, 2)
	go func() {
		defer close(ch)

		for _, r := range resp.Content {
			ch <- Chunk{Content: string(r)}
			time.Sleep(time.Millisecond)
		}

		ch <- Chunk{
			Done:  true,
			Usage: &resp.Usage,
		}
	}()

	return ch, nil
}

func (m *MockProvider) Models() []Model {
	return m.models
}

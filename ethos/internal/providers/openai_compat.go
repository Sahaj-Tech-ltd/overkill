package providers

type OpenAICompatProvider struct {
	*OpenAIProvider
}

func NewOpenAICompatProvider(name, baseURL, apiKey string, models []Model) *OpenAICompatProvider {
	inner := NewOpenAIProvider(apiKey, models)
	inner.BaseProvider.name = name
	inner.BaseProvider.baseURL = baseURL
	return &OpenAICompatProvider{
		OpenAIProvider: inner,
	}
}

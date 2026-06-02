package main

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/rewriter"
)

// sycophancyFilterAdapter bridges rewriter.SycophancyReducer into the
// agent's ResponseFilter interface (§4.10 wire-up). The reducer
// already implements Strip — this is a one-line shim that satisfies
// the interface contract.
type sycophancyFilterAdapter struct {
	reducer *rewriter.SycophancyReducer
}

var _ agent.ResponseFilter = (*sycophancyFilterAdapter)(nil)

func (s *sycophancyFilterAdapter) Filter(content string) string {
	if s == nil || s.reducer == nil {
		return content
	}
	return s.reducer.Strip(content)
}

package main

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui"
)

// journalReaderProxy lets us register the bookmark_recall tool BEFORE
// app.Journal is constructed by setupAgent — the proxy resolves
// app.Journal lazily on each tool call. Without this we'd need to
// reorder boot sequence (touch tools after agent) or move tag tool
// registration further down (touch tags after Journal). The proxy is
// the smaller change.
type journalReaderProxy struct {
	app *tui.App
}

func (p journalReaderProxy) GetFlight(id string) (*journal.Entry, error) {
	if p.app == nil || p.app.Journal == nil {
		return nil, nil
	}
	return p.app.Journal.GetFlight(id)
}

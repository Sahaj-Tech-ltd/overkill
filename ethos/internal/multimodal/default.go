// Package multimodal — default registry assembly.
//
// Order matters. The router walks the list and takes the first
// extractor whose Supports() returns true. So specific extractors
// register before broad ones; the BinaryFallback always wins last.
package multimodal

import "github.com/Sahaj-Tech-ltd/overkill/internal/vision"

// DefaultRegistry returns a registry pre-loaded with the built-in
// extractors. Pass a non-nil Describer to enable image captioning;
// nil keeps images on the metadata-only path.
//
// Registration order (first match wins):
//   1. PDF
//   2. Pandoc (DOCX/DOC/RTF/ODT)
//   3. Audio (whisper)
//   4. Image (vision describer)
//   5. Text (plain text + code + structured config)
//   6. Binary fallback (claims everything)
func DefaultRegistry(d vision.Describer) *Registry {
	r := NewRegistry()
	r.Register(NewPDFExtractor())
	r.Register(NewPandocExtractor())
	r.Register(NewAudioExtractor())
	r.Register(NewImageExtractor(d))
	r.Register(NewTextExtractor())
	r.Register(NewBinaryFallback())
	return r
}

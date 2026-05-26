// Package devbrowser — snapshotForAI structured page extraction.
//
// The model doesn't need raw HTML or a 3MB DOM dump. It needs a
// scannable summary it can reason about: title, headings, primary
// links, form fields, plus a bounded chunk of visible text. This
// file builds that summary from the live DOM via a single tiny
// JS payload — the only Evaluate() we allow ourselves to run, and
// only because there's no DOM-API-equivalent that returns
// pre-aggregated structured data.
//
// We don't expose Evaluate to the agent. The model can't get
// arbitrary JS into the page; it can only ask for the snapshot.
package devbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// Snapshot is the structured page content the agent reads. JSON-
// marshalable so tools can serialize without ceremony.
type Snapshot struct {
	URL         string         `json:"url"`
	Title       string         `json:"title"`
	Headings    []HeadingEntry `json:"headings,omitempty"`
	Links       []LinkEntry    `json:"links,omitempty"`
	Forms       []FormEntry    `json:"forms,omitempty"`
	Text        string         `json:"text"`
	TextLength  int            `json:"text_length"`
	WasTruncated bool          `json:"truncated,omitempty"`
}

// HeadingEntry is one h1-h6 in document order.
type HeadingEntry struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// LinkEntry is one <a href>. Text is the link's visible label.
type LinkEntry struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

// FormEntry is one <form>. Inputs lists the form's controls so the
// agent can decide what to fill before submitting.
type FormEntry struct {
	Action string       `json:"action,omitempty"`
	Method string       `json:"method,omitempty"`
	Inputs []FormInput  `json:"inputs,omitempty"`
}

// FormInput is one input/textarea/select inside a form.
type FormInput struct {
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Value       string `json:"value,omitempty"`
}

// Caps on snapshot size — protect the next-turn context budget.
const (
	maxHeadings = 50
	maxLinks    = 50
	maxForms    = 10
	maxText     = 6000 // ~4KB of utf-8 text
)

// snapshotJSTemplate is a printf-style template for the in-page
// script. fmt.Sprintf splices the numeric caps at startup so the
// Go-side and JS-side caps stay aligned. Double-quoted with explicit
// "\n" because Go raw strings can't contain backticks and we'd
// otherwise need an awkward escape dance.
const snapshotJSTemplate = "(function() {\n" +
	"  function txt(el) { return (el && el.textContent || '').replace(/\\s+/g, ' ').trim(); }\n" +
	"  var headings = [];\n" +
	"  var hSel = document.querySelectorAll('h1, h2, h3, h4, h5, h6');\n" +
	"  for (var i = 0; i < hSel.length && headings.length < %d; i++) {\n" +
	"    var h = hSel[i];\n" +
	"    headings.push({ level: parseInt(h.tagName.slice(1), 10), text: txt(h).slice(0, 200) });\n" +
	"  }\n" +
	"  var links = [];\n" +
	"  var aSel = document.querySelectorAll('a[href]');\n" +
	"  for (i = 0; i < aSel.length && links.length < %d; i++) {\n" +
	"    var a = aSel[i];\n" +
	"    var t = txt(a);\n" +
	"    if (t) links.push({ text: t.slice(0, 200), href: a.href });\n" +
	"  }\n" +
	"  var forms = [];\n" +
	"  var fSel = document.querySelectorAll('form');\n" +
	"  for (i = 0; i < fSel.length && forms.length < %d; i++) {\n" +
	"    var f = fSel[i];\n" +
	"    var inputs = [];\n" +
	"    var inSel = f.querySelectorAll('input, textarea, select');\n" +
	"    for (var j = 0; j < inSel.length && inputs.length < 30; j++) {\n" +
	"      var ip = inSel[j];\n" +
	"      inputs.push({\n" +
	"        name: ip.name || '',\n" +
	"        type: ip.type || ip.tagName.toLowerCase(),\n" +
	"        placeholder: ip.placeholder || '',\n" +
	"        value: (ip.type === 'password' ? '' : (ip.value || '')).slice(0, 200)\n" +
	"      });\n" +
	"    }\n" +
	"    forms.push({ action: f.action || '', method: (f.method || 'GET').toUpperCase(), inputs: inputs });\n" +
	"  }\n" +
	"  var body = document.body ? document.body.innerText || '' : '';\n" +
	"  return {\n" +
	"    url: document.location.href,\n" +
	"    title: document.title || '',\n" +
	"    headings: headings,\n" +
	"    links: links,\n" +
	"    forms: forms,\n" +
	"    text: body.slice(0, %d),\n" +
	"    text_length: body.length\n" +
	"  };\n" +
	"})()"

// snapshotJS is the materialised script with the caps baked in.
var snapshotJS = fmt.Sprintf(snapshotJSTemplate, maxHeadings, maxLinks, maxForms, maxText)

// snapshotPage runs the extractor JS in the page and decodes the
// result. ctx already has a chromedp Context attached.
func snapshotPage(ctx context.Context) (Snapshot, error) {
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify(`+snapshotJS+`)`, &raw)); err != nil {
		return Snapshot{}, fmt.Errorf("dev-browser: snapshot evaluate: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return Snapshot{}, fmt.Errorf("dev-browser: snapshot decode: %w", err)
	}
	// JS already capped at maxText; flag truncation if the original
	// body was longer.
	if snap.TextLength > maxText {
		snap.WasTruncated = true
	}
	// Trim post-JS for defense in depth.
	snap.Title = strings.TrimSpace(snap.Title)
	return snap, nil
}

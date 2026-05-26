// Package security — encoding-aware scan layer.
//
// Plain regex on input bytes misses encoded bypasses. An agent
// (hallucinating or adversarial) can write:
//
//   echo cm0gLXJmIC8K | base64 -d | sh
//
// where `cm0gLXJmIC8K` decodes to `rm -rf /`. The original Scan
// pattern set only matches plain-text — without this file, the
// dangerous command slips through every deny pattern.
//
// Approach: decode every plausible-looking base64 / hex run in the
// input and feed each decoded fragment back through the same Scan
// pipeline. If a decoded fragment matches a deny pattern, the
// finding gets reported as if it had been written in plain text.
// Original Scan still runs first so legitimate plain-text commands
// behave exactly as before.
//
// Defenses against the obvious counter:
//
//   - Bare base64-looking strings (JWTs, image data URIs, hashes)
//     decode to garbage that won't match a command pattern. False
//     positives only fire when the decoded text actually contains
//     a dangerous command.
//   - We don't decode unboundedly — runs shorter than 12 chars
//     skip (too noisy), longer than 4KB skip (almost certainly
//     not a command).
//   - "decode-and-exec" SHAPES (base64 -d | sh, eval $(...|base64),
//     xxd -r -p | bash) get a dedicated high-severity pattern
//     regardless of decode success — those constructs have no
//     legitimate purpose in agent traffic.
package security

import (
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
)

// base64Run matches contiguous base64-alphabet runs of plausible
// command length. Covers BOTH the standard RFC 4648 alphabet
// [A-Za-z0-9+/=] and the URL-safe variant [A-Za-z0-9_-=] (RFC 4648
// §5). Old pattern omitted `_` and `-`, so an attacker could encode
// `rm -rf /` with `-`/`_` substitutions to skip every base64 check.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/_\-]{12,}={0,2}`)

// hexRun matches hex runs of plausible command length. Even length
// (hex is always even); min 24 chars (12 bytes) so we skip noise
// like color codes.
var hexRun = regexp.MustCompile(`(?:[0-9a-fA-F]{2}){12,}`)

// decodeExecShapes is the catch-all for the obvious bypass patterns.
// Matching ANY of these is itself a high-severity finding regardless
// of what the surrounding decoded bytes contain — there's no benign
// reason for the agent to construct one.
var decodeExecShapes = []*regexp.Regexp{
	// `echo ... | base64 -d | sh|bash|zsh|fish`
	regexp.MustCompile(`(?i)base64\s+(?:-d|--decode)\s*\|\s*(?:sh|bash|zsh|fish|dash|ksh)`),
	// `eval $(... base64 -d ...)`
	regexp.MustCompile(`(?i)eval\s*[\$\(\x60].*base64.*(?:-d|--decode)`),
	// `xxd -r ... | sh`
	regexp.MustCompile(`(?i)xxd\s+-r\s*(?:-p)?\s*\|\s*(?:sh|bash|zsh)`),
	// `printf '%b' ... | bash`
	regexp.MustCompile(`(?i)printf\s+['"]?%b['"]?\s+.*\|\s*(?:sh|bash|zsh)`),
	// `bash <(... base64 -d)` — process substitution
	regexp.MustCompile(`(?i)(?:bash|sh|zsh)\s*<\s*\(.*base64`),
	// `curl ... | base64 ...` — fetch-decode-exec chain
	regexp.MustCompile(`(?i)curl\s+[^|]+\|\s*base64`),
}

// maxDecodedRunBytes caps how big a single decode candidate can be
// before we bail. Real commands are short; a 50KB base64 blob is
// almost certainly an image embed and not worth scanning the
// decoded gibberish.
const maxDecodedRunBytes = 4 * 1024

// decodeAndScan returns extra findings produced by re-scanning the
// input's decoded base64/hex runs against the same patterns. Empty
// slice when nothing decoded suspiciously.
//
// Pure function on the scanner's patterns — no rate-limit side
// effects, no permission flips. The caller folds these into Scan's
// finding list.
func (s *CommandScanner) decodeAndScan(input string) []Finding {
	var out []Finding

	// Pass 1: decode-and-execute shapes. Matching any of these is
	// the finding — we don't even need to decode the payload.
	for _, re := range decodeExecShapes {
		if loc := re.FindStringIndex(input); loc != nil {
			out = append(out, Finding{
				Type:        "encoded_bypass",
				Level:       ThreatHigh,
				Description: "decode-and-execute pattern (likely deny-list bypass)",
				Match:       input[loc[0]:loc[1]],
				Confidence:  0.95,
			})
		}
	}

	// Pass 2: decode every plausible base64 run, re-scan against
	// the deny patterns. Duplicate findings (same description) are
	// deduped so a single decoded `rm -rf /` doesn't produce N
	// rows when N patterns match against it.
	seenDesc := map[string]bool{}
	for _, m := range base64Run.FindAllString(input, -1) {
		if len(m) > maxDecodedRunBytes {
			continue
		}
		decoded := tryBase64(m)
		if decoded == "" {
			continue
		}
		for _, p := range s.patterns {
			locs := p.regex.FindAllStringIndex(decoded, -1)
			for range locs {
				key := p.description
				if seenDesc[key] {
					continue
				}
				seenDesc[key] = true
				out = append(out, Finding{
					Type:        "encoded_bypass",
					Level:       p.level,
					Description: "encoded (base64): " + p.description,
					Match:       m,
					Confidence:  p.confidence,
				})
			}
		}
	}

	// Pass 3: hex runs. Same shape as base64.
	for _, m := range hexRun.FindAllString(input, -1) {
		if len(m) > maxDecodedRunBytes {
			continue
		}
		decoded := tryHex(m)
		if decoded == "" {
			continue
		}
		for _, p := range s.patterns {
			locs := p.regex.FindAllStringIndex(decoded, -1)
			for range locs {
				key := "hex/" + p.description
				if seenDesc[key] {
					continue
				}
				seenDesc[key] = true
				out = append(out, Finding{
					Type:        "encoded_bypass",
					Level:       p.level,
					Description: "encoded (hex): " + p.description,
					Match:       m,
					Confidence:  p.confidence,
				})
			}
		}
	}

	return out
}

// tryBase64 returns the decoded string when s is valid base64 AND
// the decoded bytes look like a command (mostly printable ASCII).
// Returns "" when decode fails or the result is binary noise — keeps
// false positives off legitimate base64 like image embeds or JWTs.
func tryBase64(s string) string {
	// Try standard then URL-safe. base64.StdEncoding accepts both
	// padded and unpadded only via WithPadding(NoPadding); we try
	// both to catch the "no = signs" variant the agent might write.
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		if !looksLikeCommand(decoded) {
			continue
		}
		return string(decoded)
	}
	return ""
}

// tryHex returns the decoded string when s is valid hex AND the
// decoded bytes look like a command.
func tryHex(s string) string {
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	if !looksLikeCommand(decoded) {
		return ""
	}
	return string(decoded)
}

// looksLikeCommand reports whether b is plausibly a shell command:
// mostly printable ASCII, contains a letter, doesn't contain NUL.
// Filters out the obvious "decoded to garbage" cases — JWT payloads,
// crypto material, image bytes — without rejecting actual commands.
func looksLikeCommand(b []byte) bool {
	if len(b) < 3 {
		return false
	}
	printable := 0
	hasLetter := false
	for _, c := range b {
		if c == 0 {
			return false
		}
		if c >= 0x20 && c < 0x7f {
			printable++
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				hasLetter = true
			}
		} else if c != '\n' && c != '\t' && c != '\r' {
			// Non-printable, non-whitespace byte → almost certainly
			// not a command.
			return false
		}
	}
	// At least 80% of the bytes must be printable, and there must
	// be at least one letter (binary blobs that happen to be all
	// printable like a fence of dashes don't qualify).
	if !hasLetter {
		return false
	}
	return printable*5 >= len(b)*4
}

// _ keeps strings imported when callers above strip the usage.
var _ = strings.Contains

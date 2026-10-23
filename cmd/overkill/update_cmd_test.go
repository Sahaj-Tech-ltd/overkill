package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const updateFixtureRepo = "fixture-owner/fixture-repo"
const updateFixtureTag = "v1.2.3"

func updateFixtureURL(name string) string {
	return "https://github.com/" + updateFixtureRepo + "/releases/download/" + updateFixtureTag + "/" + name
}

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Policy still sees production HTTPS origins; only the test transport sends
// approved requests to a controlled local HTTP server. No production URL knob.
func updateFixtureClient(server *httptest.Server, assets bool) *http.Client {
	client := newUpdateHTTPClient(assets)
	target, _ := url.Parse(server.URL)
	client.Transport = updateRoundTrip(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		copy.URL = &u
		copy.Host = ""
		return http.DefaultTransport.RoundTrip(copy)
	})
	return client
}
func updateDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
func updateFixtureRelease(content []byte) *release {
	name, _ := updateAssetName(runtime.GOOS, runtime.GOARCH)
	return &release{TagName: updateFixtureTag, Assets: []releaseAsset{{Name: name, BrowserDownloadURL: updateFixtureURL(name), Size: len(content)}, {Name: "checksums.txt", BrowserDownloadURL: updateFixtureURL("checksums.txt")}}}
}
func assertUpdatePreserved(t *testing.T, directory, dst string) {
	t.Helper()
	live, err := os.ReadFile(dst)
	if err != nil || string(live) != "previous executable" {
		t.Fatalf("live changed: %q %v", live, err)
	}
	backup, err := os.ReadFile(dst + ".bak")
	if err != nil || string(backup) != "previous retained backup" {
		t.Fatalf("backup changed: %q %v", backup, err)
	}
	leaks, _ := filepath.Glob(filepath.Join(directory, ".overkill-*-*"))
	if len(leaks) > 0 {
		t.Fatalf("temporary files leaked: %v", leaks)
	}
}
func TestUpdateAllPublishedTargetsRequireExactUniqueAsset(t *testing.T) {
	assets := []releaseAsset{}
	for _, name := range []string{"overkill-linux-amd64", "overkill-linux-arm64", "overkill-darwin-amd64", "overkill-darwin-arm64", "overkill-windows-amd64.exe", "checksums.txt"} {
		assets = append(assets, releaseAsset{Name: name})
	}
	for _, target := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"} {
		parts := strings.Split(target, "/")
		want, _ := updateAssetName(parts[0], parts[1])
		got, err := pickAssetForPlatform(assets, parts[0], parts[1])
		if err != nil || got.Name != want {
			t.Fatalf("%s: %v %v", target, got, err)
		}
		if _, err = pickAssetForPlatform(append(assets, releaseAsset{Name: want}), parts[0], parts[1]); err == nil {
			t.Fatal("duplicate exact target accepted")
		}
	}
	for _, name := range []string{"overkill.tar.gz", "overkill-" + runtime.GOOS + "-wrong", "overkill-other-" + runtime.GOARCH, "prefix-overkill-" + runtime.GOOS + "-" + runtime.GOARCH} {
		if _, err := pickAsset([]releaseAsset{{Name: name}}); err == nil {
			t.Fatalf("fallback accepted %s", name)
		}
	}
	if _, err := pickAssetForPlatform(assets, "windows", "arm64"); err == nil {
		t.Fatal("unsupported target accepted")
	}
}
func TestUpdateChecksumStrictPublishedFormats(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, body := range []string{hash + "  overkill-linux-amd64\n", strings.ToUpper(hash) + " *overkill-linux-amd64\n", "# comment\n\n" + hash + "  overkill-linux-amd64\r\n"} {
		got, err := parseUpdateChecksum([]byte(body), "overkill-linux-amd64")
		if err != nil || got != hash {
			t.Fatalf("%q: %q %v", body, got, err)
		}
	}
	for _, body := range []string{"", "deadbeef  overkill-linux-amd64\n", strings.Repeat("g", 64) + "  overkill-linux-amd64\n", hash + "  other\n", hash + "  overkill-linux-amd64 extra\n", hash + "  overkill-linux-amd64\n" + hash + "  overkill-linux-amd64\n", hash + "  ../overkill-linux-amd64\n", hash + "  OVERKILL-linux-amd64\n"} {
		if _, err := parseUpdateChecksum([]byte(body), "overkill-linux-amd64"); err == nil {
			t.Fatalf("bad manifest accepted %q", body)
		}
	}
}
func TestUpdateIntegrityFailuresPreserveLiveAndBackup(t *testing.T) {
	payload := []byte("verified replacement bytes")
	cases := []string{"missing_manifest", "duplicate_manifest_asset", "manifest404", "manifest_malformed", "manifest_unlisted", "manifest_duplicate", "manifest_oversized", "wrong_hash", "download500", "truncated", "metadata_size_mismatch", "binary_oversized", "wrong_asset_url", "wrong_manifest_url"}
	for _, scenario := range cases {
		t.Run(scenario, func(t *testing.T) {
			rel := updateFixtureRelease(payload)
			name := rel.Assets[0].Name
			manifest := updateDigest(payload) + "  " + name + "\n"
			binaryCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
					switch scenario {
					case "manifest404":
						http.Error(w, "missing", 404)
						return
					case "manifest_malformed":
						manifest = "invalid  " + name
					case "manifest_unlisted":
						manifest = updateDigest(payload) + "  other"
					case "manifest_duplicate":
						manifest += manifest
					case "manifest_oversized":
						manifest = strings.Repeat(" ", maxUpdateManifestBytes+1)
					case "wrong_hash":
						manifest = strings.Repeat("0", 64) + "  " + name
					}
					fmt.Fprint(w, manifest)
					return
				}
				binaryCalls++
				if scenario == "download500" {
					http.Error(w, "failed", 500)
					return
				}
				if scenario == "truncated" {
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)+10))
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			switch scenario {
			case "missing_manifest":
				rel.Assets = rel.Assets[:1]
			case "duplicate_manifest_asset":
				rel.Assets = append(rel.Assets, rel.Assets[1])
			case "metadata_size_mismatch":
				rel.Assets[0].Size++
			case "binary_oversized":
				rel.Assets[0].Size = maxUpdateBinaryBytes + 1
			case "wrong_asset_url":
				rel.Assets[0].BrowserDownloadURL = "https://evil.invalid/payload"
			case "wrong_manifest_url":
				rel.Assets[1].BrowserDownloadURL = "https://github.com/other/repo/releases/download/v1/checksums.txt"
			}
			directory := t.TempDir()
			dst := filepath.Join(directory, "overkill")
			_ = os.WriteFile(dst, []byte("previous executable"), 0755)
			_ = os.WriteFile(dst+".bak", []byte("previous retained backup"), 0755)
			if _, err := installUpdateRelease(t.Context(), updateFixtureRepo, rel, dst, runtime.GOOS, runtime.GOARCH, updateFixtureClient(server, true)); err == nil {
				t.Fatal("unverified install accepted")
			}
			assertUpdatePreserved(t, directory, dst)
			if strings.HasPrefix(scenario, "manifest") || scenario == "missing_manifest" || scenario == "duplicate_manifest_asset" || scenario == "wrong_manifest_url" {
				if binaryCalls != 0 {
					t.Fatal("binary downloaded before required manifest accepted")
				}
			}
		})
	}
}
func TestUpdateVerifiedHTTPReplacementAndAtomicFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX live rename acceptance; Windows uses verified manual stage")
	}
	payload := []byte("verified new executable")
	rel := updateFixtureRelease(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			fmt.Fprintf(w, "%s  %s\n", updateDigest(payload), rel.Assets[0].Name)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	directory := t.TempDir()
	dst := filepath.Join(directory, "overkill")
	_ = os.WriteFile(dst, []byte("previous executable"), 0755)
	_ = os.WriteFile(dst+".bak", []byte("older backup"), 0755)
	result, err := installUpdateRelease(t.Context(), updateFixtureRepo, rel, dst, runtime.GOOS, runtime.GOARCH, updateFixtureClient(server, true))
	if err != nil || result.SHA256 != updateDigest(payload) {
		t.Fatalf("install: %+v %v", result, err)
	}
	live, _ := os.ReadFile(dst)
	backup, _ := os.ReadFile(dst + ".bak")
	if string(live) != string(payload) || string(backup) != "previous executable" {
		t.Fatal("verified publication/backup mismatch")
	}
	temporary := filepath.Join(directory, "candidate")
	_ = os.WriteFile(temporary, []byte("next"), 0755)
	calls := 0
	err = publishVerifiedUpdate(dst, temporary, func(from, to string) error {
		calls++
		current, _ := os.ReadFile(dst)
		if string(current) != string(payload) {
			t.Fatal("live path disappeared before atomic publication")
		}
		if calls == 2 {
			return errors.New("injected final rename failure")
		}
		return os.Rename(from, to)
	})
	if err == nil {
		t.Fatal("rename failure lost")
	}
	live, _ = os.ReadFile(dst)
	if string(live) != string(payload) {
		t.Fatal("publication failure replaced live executable")
	}
}
func TestUpdateWindowsVerifiedManualStage(t *testing.T) {
	payload := []byte("verified windows PE fixture")
	name := "overkill-windows-amd64.exe"
	rel := &release{TagName: updateFixtureTag, Assets: []releaseAsset{{Name: name, Size: len(payload), BrowserDownloadURL: updateFixtureURL(name)}, {Name: "checksums.txt", BrowserDownloadURL: updateFixtureURL("checksums.txt")}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			fmt.Fprintf(w, "%s  %s\n", updateDigest(payload), name)
		} else {
			_, _ = w.Write(payload)
		}
	}))
	defer server.Close()
	dst := filepath.Join(t.TempDir(), "overkill.exe")
	_ = os.WriteFile(dst, []byte("original"), 0755)
	result, err := installUpdateRelease(t.Context(), updateFixtureRepo, rel, dst, "windows", "amd64", updateFixtureClient(server, true))
	if err != nil || result.StagedPath != dst+".new" {
		t.Fatalf("%+v %v", result, err)
	}
	live, _ := os.ReadFile(dst)
	staged, _ := os.ReadFile(result.StagedPath)
	if string(live) != "original" || string(staged) != string(payload) {
		t.Fatal("manual stage changed live binary")
	}
}
func TestUpdateDesktopBundleMutationGuard(t *testing.T) {
	for _, path := range []string{"/Applications/Overkill.app/Contents/Resources/bin/overkill", "/tmp/Overkill Development.app/Contents/Resources/bin/overkill"} {
		if err := rejectDesktopSelfUpdate(path); err == nil || !strings.Contains(err.Error(), "DMG") {
			t.Fatal("bundle mutation was not explicitly rejected")
		}
	}
	if err := rejectDesktopSelfUpdate("/tmp/overkill/bin/overkill"); err != nil {
		t.Fatal(err)
	}
}
func TestUpdateTrustedURLAndRedirectPolicy(t *testing.T) {
	for _, raw := range []string{"http://github.com/a/b/releases/download/v1/x", "https://evil.invalid/a/b/releases/download/v1/x", "https://github.com:444/a/b/releases/download/v1/x", "https://user:secret@github.com/a/b/releases/download/v1/x", "https://github.com/a/b/releases/download/v1/x?token=x", "https://github.com/a/b/releases/download/v1/x#fragment"} {
		if err := validateReleaseAssetURL(raw, "a/b", "v1", "x"); err == nil {
			t.Fatalf("untrusted URL accepted %s", raw)
		}
	}
	for _, repo := range []string{"https://evil.invalid/x", "a/b?x", "a/../b", "/b", "a/b/c"} {
		if err := validateUpdateRepo(repo); err == nil {
			t.Fatalf("invalid repo %s", repo)
		}
	}
	for _, destination := range []string{"https://evil.invalid/steal", "http://release-assets.githubusercontent.com/x", "https://github.com/other/repo/releases/download/v1/x"} {
		req, _ := http.NewRequest("GET", destination, nil)
		original, _ := http.NewRequest("GET", updateFixtureURL("binary"), nil)
		if err := newUpdateHTTPClient(true).CheckRedirect(req, []*http.Request{original}); err == nil {
			t.Fatalf("bad redirect accepted %s", destination)
		}
	}
	req, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/payload?signature=fixture", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "secret=x")
	original, _ := http.NewRequest("GET", updateFixtureURL("binary"), nil)
	if err := newUpdateHTTPClient(true).CheckRedirect(req, []*http.Request{original}); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
		t.Fatal("credentials forwarded to release CDN")
	}
	if err := newUpdateHTTPClient(false).CheckRedirect(req, []*http.Request{original}); err == nil {
		t.Fatal("API redirect accepted")
	}
}
func TestUpdateReleaseAPIIsBoundedAndAuthenticatedWithoutRedirectLeak(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-token")
	for _, mode := range []string{"valid", "redirect", "oversized", "trailing", "bad_status"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer fixture-github-token" {
					t.Error("API authentication missing")
				}
				switch mode {
				case "redirect":
					http.Redirect(w, r, "https://evil.invalid/steal", 302)
					return
				case "oversized":
					fmt.Fprint(w, strings.Repeat(" ", maxUpdateAPIBytes+1))
					return
				case "trailing":
					fmt.Fprint(w, `{"tag_name":"v1"} {}`)
					return
				case "bad_status":
					http.Error(w, "failure", 503)
					return
				}
				_ = json.NewEncoder(w).Encode(updateFixtureRelease([]byte("binary")))
			}))
			defer server.Close()
			rel, err := fetchLatestReleaseWithClient(context.Background(), updateFixtureRepo, updateFixtureClient(server, false))
			if mode == "valid" {
				if err != nil || rel.TagName != updateFixtureTag {
					t.Fatalf("%+v %v", rel, err)
				}
			} else if err == nil {
				t.Fatal("invalid API response accepted")
			}
			if requests != 1 {
				t.Fatal("API redirect transmitted another request")
			}
		})
	}
}
func TestUpdateDownloadedNativeExecutableRuns(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	rel := updateFixtureRelease(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			fmt.Fprintf(w, "%s  %s\n", updateDigest(payload), rel.Assets[0].Name)
		} else {
			_, _ = w.Write(payload)
		}
	}))
	defer server.Close()
	dst := filepath.Join(t.TempDir(), "overkill")
	_ = os.WriteFile(dst, []byte("previous executable"), 0755)
	result, err := installUpdateRelease(t.Context(), updateFixtureRepo, rel, dst, runtime.GOOS, runtime.GOARCH, updateFixtureClient(server, true))
	if err != nil {
		t.Fatal(err)
	}
	if result.StagedPath != "" {
		dst = result.StagedPath
	}
	command := exec.Command(dst, "-test.run=^TestUpdateDownloadedExecutableProbe$", "-test.v")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "native downloaded executable probe") {
		t.Fatalf("downloaded native binary did not execute: %v %s", err, output)
	}
}
func TestUpdateDownloadedExecutableProbe(t *testing.T) { t.Log("native downloaded executable probe") }

func TestUpdateActualHTTPAssetRedirectPolicyAndCancellation(t *testing.T) {
	for _, mode := range []string{"approved_cdn", "untrusted_redirect", "canceled", "oversized_body", "manifest_truncated"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("redirected verified bytes")
			rel := updateFixtureRelease(payload)
			stolen := false
			cdn := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/steal":
					stolen = true
					_, _ = w.Write(payload)
					return
				case "/cdn":
					cdn = true
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("release CDN received credentials")
					}
					_, _ = w.Write(payload)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
					manifest := updateDigest(payload) + "  " + rel.Assets[0].Name + "\n"
					if mode == "manifest_truncated" {
						w.Header().Set("Content-Length", fmt.Sprint(len(manifest)+10))
					}
					fmt.Fprint(w, manifest)
					return
				}
				switch mode {
				case "approved_cdn":
					http.Redirect(w, r, "https://release-assets.githubusercontent.com/cdn?signature=fixture", 302)
				case "untrusted_redirect":
					http.Redirect(w, r, "https://evil.invalid/steal", 302)
				case "oversized_body":
					_, _ = w.Write(append(payload, 'x'))
				default:
					_, _ = w.Write(payload)
				}
			}))
			defer server.Close()
			dst := filepath.Join(t.TempDir(), "overkill")
			_ = os.WriteFile(dst, []byte("previous executable"), 0755)
			_ = os.WriteFile(dst+".bak", []byte("previous retained backup"), 0755)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			_, err := installUpdateRelease(ctx, updateFixtureRepo, rel, dst, runtime.GOOS, runtime.GOARCH, updateFixtureClient(server, true))
			if mode == "approved_cdn" {
				if err != nil || !cdn {
					t.Fatalf("valid CDN redirect failed: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe/failed response accepted")
				}
				assertUpdatePreserved(t, filepath.Dir(dst), dst)
			}
			if stolen {
				t.Fatal("unsafe redirect transmitted a request")
			}
		})
	}
}
func TestUpdateRollbackAndBundleGuardUseOwnedPaths(t *testing.T) {
	directory := t.TempDir()
	dst := filepath.Join(directory, "overkill")
	if err := rollbackUpdateAt(dst); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(dst, []byte("current"), 0755)
	_ = os.WriteFile(dst+".bak", []byte("previous"), 0755)
	if err := rollbackUpdateAt(dst); err != nil {
		t.Fatal(err)
	}
	live, _ := os.ReadFile(dst)
	if string(live) != "previous" {
		t.Fatal("owned rollback did not restore previous binary")
	}
	bundle := filepath.Join(directory, "Overkill.app", "Contents", "Resources", "bin", "overkill")
	if err := os.MkdirAll(filepath.Dir(bundle), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(bundle, []byte("signed bundle bytes"), 0755)
	_ = os.WriteFile(bundle+".bak", []byte("backup"), 0755)
	if err := rollbackUpdateAt(bundle); err == nil {
		t.Fatal("bundled rollback accepted")
	}
	if _, err := installUpdateRelease(t.Context(), updateFixtureRepo, updateFixtureRelease([]byte("new")), bundle, runtime.GOOS, runtime.GOARCH, newUpdateHTTPClient(true)); err == nil {
		t.Fatal("bundle update accepted")
	}
	live, _ = os.ReadFile(bundle)
	backup, _ := os.ReadFile(bundle + ".bak")
	if string(live) != "signed bundle bytes" || string(backup) != "backup" {
		t.Fatal("guard mutated bundle")
	}
}

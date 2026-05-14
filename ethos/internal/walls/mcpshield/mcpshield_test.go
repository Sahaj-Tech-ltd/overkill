package mcpshield

import (
	"testing"
)

func TestPolicy_DeniesUnknownServer(t *testing.T) {
	p := NewPolicy()
	d := p.CheckCall("mystery", "tool", nil, 0)
	if d.Allow {
		t.Errorf("unknown server should deny: %+v", d)
	}
}

func TestPolicy_AllowsListedTool(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{
		ServerName:   "github",
		AllowedTools: []string{"pr_create", "issue_open"},
	})
	d := p.CheckCall("github", "pr_create", nil, 0)
	if !d.Allow {
		t.Errorf("listed tool should allow: %+v", d)
	}
}

func TestPolicy_DeniesUnlistedTool(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{
		ServerName:   "github",
		AllowedTools: []string{"pr_create"},
	})
	d := p.CheckCall("github", "delete_repo", nil, 0)
	if d.Allow {
		t.Errorf("unlisted tool should deny: %+v", d)
	}
}

func TestPolicy_TrustedSkipsToolAllowList(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{ServerName: "internal", Trusted: true})
	d := p.CheckCall("internal", "anything", nil, 0)
	if !d.Allow {
		t.Errorf("trusted server should bypass tool list: %+v", d)
	}
}

func TestPolicy_EnforcesByteCap(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{
		ServerName:      "x",
		MaxBytesPerCall: 100,
		Trusted:         true,
	})
	d := p.CheckCall("x", "tool", nil, 200)
	if d.Allow {
		t.Errorf("over-budget call should deny: %+v", d)
	}
	d = p.CheckCall("x", "tool", nil, 50)
	if !d.Allow {
		t.Errorf("under-budget call should allow: %+v", d)
	}
}

func TestPolicy_PathAllowList(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{
		ServerName:          "fs",
		Trusted:             true,
		AllowedPathPrefixes: []string{"/home/user/projects"},
	})
	in := []string{"/home/user/projects/foo/bar.go"}
	out := []string{"/etc/passwd"}
	if d := p.CheckCall("fs", "read", in, 0); !d.Allow {
		t.Errorf("path under prefix should allow: %+v", d)
	}
	if d := p.CheckCall("fs", "read", out, 0); d.Allow {
		t.Errorf("path outside prefix should deny: %+v", d)
	}
}

func TestPolicy_RemoveDropsCapability(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{ServerName: "x", Trusted: true})
	p.Remove("x")
	d := p.CheckCall("x", "tool", nil, 0)
	if d.Allow {
		t.Errorf("removed server should deny: %+v", d)
	}
}

func TestPolicy_SetRequiresName(t *testing.T) {
	p := NewPolicy()
	if err := p.Set(Capability{}); err == nil {
		t.Error("empty server name should error")
	}
}

func TestPolicy_AllReturnsDeclarations(t *testing.T) {
	p := NewPolicy()
	_ = p.Set(Capability{ServerName: "a"})
	_ = p.Set(Capability{ServerName: "b"})
	all := p.All()
	if len(all) != 2 {
		t.Errorf("expected 2 caps, got %d", len(all))
	}
}

func TestPolicy_PathPrefixDoesNotMatchSubstring(t *testing.T) {
	// /foo should not match /foobar — prefix must be a clean
	// path boundary.
	p := NewPolicy()
	_ = p.Set(Capability{
		ServerName:          "x",
		Trusted:             true,
		AllowedPathPrefixes: []string{"/foo"},
	})
	d := p.CheckCall("x", "tool", []string{"/foobar/x"}, 0)
	if d.Allow {
		t.Errorf("substring match should NOT pass: %+v", d)
	}
}

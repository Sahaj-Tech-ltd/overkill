package discord

import (
	"testing"
)

func TestStripMention_RemovesBothForms(t *testing.T) {
	// Discord mention syntax: <@ID> (regular) and <@!ID> (nickname).
	// Both should be stripped so the agent sees clean text.
	cases := []struct {
		in, self, want string
	}{
		{"<@123> hello", "123", " hello"},
		{"<@!123> nick hello", "123", " nick hello"},
		{"prefix <@123> middle <@!123> suffix", "123", "prefix  middle  suffix"},
		{"no mention here", "123", "no mention here"},
		{"<@123>", "123", ""},
	}
	for _, c := range cases {
		got := stripMention(c.in, c.self)
		if got != c.want {
			t.Errorf("strip(%q, %q) = %q want %q", c.in, c.self, got, c.want)
		}
	}
}

func TestStripMention_EmptySelfIDIsNoOp(t *testing.T) {
	// Before the bot's Ready event fires we don't know our own ID.
	// stripMention with empty selfID must NOT remove arbitrary
	// snowflake-like substrings — only exact self-mentions.
	if got := stripMention("<@123> hi", ""); got != "<@123> hi" {
		t.Errorf("empty selfID should leave content alone, got %q", got)
	}
}

func TestNewBot_BuildsAllowSets(t *testing.T) {
	b := NewBot("tok", nil,
		[]string{"guild-a", "guild-b"},
		[]string{"chan-1"},
		true,
	)
	if !b.AllowedGuilds["guild-a"] || !b.AllowedGuilds["guild-b"] {
		t.Errorf("guilds: %v", b.AllowedGuilds)
	}
	if !b.AllowedChannels["chan-1"] {
		t.Errorf("channels: %v", b.AllowedChannels)
	}
	if !b.RequireMention {
		t.Error("require mention should propagate")
	}
}

func TestNewBot_EmptyAllowListsAcceptAny(t *testing.T) {
	b := NewBot("tok", nil, nil, nil, false)
	// Allow-list maps should be empty (length 0) so the runtime check
	// "len > 0 && !contains" is false — i.e. any guild passes.
	if len(b.AllowedGuilds) != 0 {
		t.Errorf("expected empty guild set, got %v", b.AllowedGuilds)
	}
	if len(b.AllowedChannels) != 0 {
		t.Errorf("expected empty channel set, got %v", b.AllowedChannels)
	}
}

func TestBot_Name(t *testing.T) {
	if got := (&Bot{}).Name(); got != "discord" {
		t.Errorf("Name: %q want discord", got)
	}
}

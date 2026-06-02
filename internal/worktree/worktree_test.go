package worktree

import "testing"

func TestParsePorcelain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Worktree
	}{
		{
			name: "single main worktree",
			in:   "worktree /home/u/repo\nHEAD abcdef\nbranch refs/heads/main\n\n",
			want: []Worktree{{Path: "/home/u/repo", HEAD: "abcdef", Branch: "refs/heads/main"}},
		},
		{
			name: "multiple worktrees with detached and locked",
			in: "worktree /home/u/repo\nHEAD aaa\nbranch refs/heads/main\n\n" +
				"worktree /home/u/wt-feature\nHEAD bbb\nbranch refs/heads/feature\nlocked\n\n" +
				"worktree /home/u/wt-detached\nHEAD ccc\ndetached\n\n",
			want: []Worktree{
				{Path: "/home/u/repo", HEAD: "aaa", Branch: "refs/heads/main"},
				{Path: "/home/u/wt-feature", HEAD: "bbb", Branch: "refs/heads/feature", Locked: true},
				{Path: "/home/u/wt-detached", HEAD: "ccc", Detached: true},
			},
		},
		{
			name: "bare repository",
			in:   "worktree /home/u/bare.git\nbare\n\n",
			want: []Worktree{{Path: "/home/u/bare.git", Bare: true}},
		},
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
		{
			name: "trailing without blank line",
			in:   "worktree /tmp/x\nHEAD aaa\nbranch refs/heads/x\n",
			want: []Worktree{{Path: "/tmp/x", HEAD: "aaa", Branch: "refs/heads/x"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePorcelain(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: want %d, got %d (%+v)", len(tc.want), len(got), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

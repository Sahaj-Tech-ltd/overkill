package input

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		// Shell signals — leading $.
		{"$ls", KindShell},
		{"$rm -rf build", KindShell},
		{"$  echo hi  ", KindShell},

		// Shell metacharacters.
		{"ls | grep foo", KindShell},
		{"echo hi > out.txt", KindShell},
		{"cat *.go", KindShell},
		{"git log && git status", KindShell},

		// Known command + flag → shell.
		{"ls -la", KindShell},
		{"rm -rf ./build", KindShell},
		{"docker ps -a", KindShell},

		// Known command + path → shell.
		{"cat /etc/hosts", KindShell},
		{"cd ./internal", KindShell},
		{"ls ~/projects", KindShell},

		// Known tool + likely subcommand → shell.
		{"git status", KindShell},
		{"npm install", KindShell},
		{"docker ps", KindShell},
		{"kubectl get pods", KindShell},

		// Single known command alone → ambiguous (could be command or
		// shorthand chat). User picks via $ prefix or just hits Enter.
		{"ls", KindAmbiguous},
		{"pwd", KindAmbiguous},

		// Natural language — known command embedded in a sentence.
		{"how does git work in this repo", KindNL},
		{"explain the agent loop", KindNL},
		{"fix the bug in the auth module", KindNL},
		{"what does ls do here", KindNL},

		// Question mark alone shouldn't trip the meta-char branch.
		{"what is this codebase about?", KindNL},
		// Known limitation: NL questions that quote shell syntax with
		// backticks/pipes get flagged as shell. The user can either
		// avoid the backticks or just hit Enter — the agent will still
		// answer; we just won't show a hint. Documenting via test so
		// the behaviour is explicit and future refinement is gated by
		// updating this assertion intentionally.
		{"how do globs work with `?`", KindShell},

		// Empty / whitespace.
		{"", KindNL},
		{"   \t  ", KindNL},

		// Unrecognised first token → NL.
		{"refactor the whole module", KindNL},
		{"make me a sandwich", KindNL}, // `make` excluded from common list
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := Classify(tc.in)
			if got != tc.want {
				t.Errorf("Classify(%q) = %s; want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		KindShell:     "shell",
		KindNL:        "nl",
		KindAmbiguous: "ambiguous",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

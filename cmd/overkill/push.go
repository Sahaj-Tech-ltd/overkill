// Package main — `overkill push --preview` (master plan §4.8).
//
// Renders an ASCII pre-push window: per-commit summary, total diff stats,
// and a confirmation prompt. Designed to surface "wait, this is going to
// push 47 commits" before you regret it.
//
// Without --preview, falls through to plain `git push` so users can wire
// the binary as a git alias.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var (
	pushPreview bool
	pushRemote  string
	pushBranch  string
	pushForce   bool
)

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push current branch with an optional ASCII preview window",
	RunE:  runPush,
}

func init() {
	pushCmd.Flags().BoolVar(&pushPreview, "preview", false, "show an ASCII summary and confirm before pushing")
	pushCmd.Flags().StringVar(&pushRemote, "remote", "origin", "remote to push to")
	pushCmd.Flags().StringVar(&pushBranch, "branch", "", "branch to push (default: current)")
	pushCmd.Flags().BoolVar(&pushForce, "force", false, "force-push (use with caution)")
	rootCmd.AddCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	branch := pushBranch
	if branch == "" {
		out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("push: getting current branch: %w", err)
		}
		branch = strings.TrimSpace(string(out))
	}
	if pushPreview {
		if err := renderPushPreview(pushRemote, branch); err != nil {
			return err
		}
		ok, err := confirmPush()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Printf("%saborted%s\n", colorYellow, colorReset)
			return nil
		}
	}
	gitArgs := []string{"push"}
	if pushForce {
		gitArgs = append(gitArgs, "--force-with-lease")
	}
	gitArgs = append(gitArgs, pushRemote, branch)
	c := exec.Command("git", gitArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// renderPushPreview prints a window like:
//   ─── push origin main ────────────────────────────────────
//   3 commits ahead · +127 / -42 lines
//   abc1234  feat: ship the thing
//   def5678  fix: typo in readme
//   9876543  test: cover the new branch
//   ─────────────────────────────────────────────────────────
func renderPushPreview(remote, branch string) error {
	upstream := remote + "/" + branch

	// `git log A..B` exits non-zero when A doesn't exist (e.g. no
	// upstream yet) — swallowing that and printing "nothing to push"
	// is the misleading-empty-output bug. Surface the error so the
	// caller sees "no upstream" instead of "all good".
	logOut, logErr := exec.Command("git", "log", "--oneline", upstream+".."+branch).Output()
	commits := strings.TrimRight(string(logOut), "\n")

	diffOut, _ := exec.Command("git", "diff", "--shortstat", upstream+".."+branch).Output()
	stat := strings.TrimSpace(string(diffOut))

	header := fmt.Sprintf(" push %s/%s ", remote, branch)
	bar := strings.Repeat("─", 65-len(header))
	fmt.Printf("%s───%s%s%s\n", colorBlue, header, bar, colorReset)
	if logErr != nil {
		// Most common cause: upstream ref doesn't exist locally. Tell
		// the user instead of pretending the branch is in sync.
		fmt.Printf("%scannot compare against %s: %v%s\n", colorYellow, upstream, logErr, colorReset)
		fmt.Printf("%s(run `git fetch %s` or `git push -u %s %s` for a new branch)%s\n", colorYellow, remote, remote, branch, colorReset)
		fmt.Printf("%s%s%s\n", colorBlue, strings.Repeat("─", 65), colorReset)
		return logErr
	}
	if commits == "" {
		fmt.Printf("%snothing to push (already up to date with %s)%s\n", colorYellow, upstream, colorReset)
	} else {
		nLines := strings.Count(commits, "\n") + 1
		fmt.Printf("%s%d commit(s) ahead%s", colorGreen, nLines, colorReset)
		if stat != "" {
			fmt.Printf(" · %s", stat)
		}
		fmt.Println()
		fmt.Println(commits)
	}
	fmt.Printf("%s%s%s\n", colorBlue, strings.Repeat("─", 65), colorReset)
	return nil
}

func confirmPush() (bool, error) {
	fmt.Printf("Push? [y/N] ")
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return true, nil
	}
	if answer == "" {
		return false, nil
	}
	if answer == "n" || answer == "no" {
		return false, nil
	}
	return false, errors.New("push: unrecognized response")
}

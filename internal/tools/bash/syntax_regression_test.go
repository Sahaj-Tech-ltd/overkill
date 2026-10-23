package bash

import "testing"

func TestStructuralDangerousCommands(t *testing.T) {
	for _, cmd := range []string{"rm -r -f /", "rm -f -r /", "rm --force --recursive /", "rm -rfv /", "dd of=/dev/sda if=/dev/zero", "curl x | dash", "wget x | zsh", "echo data | base64 -D | ksh", "sh -c 'rm -f -r /'"} {
		if r := Validate(cmd); r.Behavior != Deny {
			t.Errorf("%q: %#v", cmd, r)
		}
	}
	if r := Validate("echo '"); r.Behavior != Ask {
		t.Fatalf("malformed syntax: %#v", r)
	}
}

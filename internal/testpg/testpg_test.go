package testpg

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSchemaDSN(t *testing.T) {
	original := "postgres://user:example@localhost/test?sslmode=require&search_path=public"
	scoped, err := schemaDSN(original, "overkill_test_fixture")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(scoped)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("sslmode") != "require" || u.Query().Get("search_path") != "overkill_test_fixture" {
		t.Fatal("schema configuration must preserve connection settings and replace search_path")
	}
	scoped, err = schemaDSN("dbname=test sslmode=disable", "overkill_test_fixture")
	if err != nil || !strings.HasSuffix(scoped, " search_path=overkill_test_fixture") {
		t.Fatal("keyword DSN must include schema")
	}
}

// The subprocess exercises the real testing.T skip/fail policy without
// deliberately failing its parent test or relying on a reachable server.
func TestOpenPolicy(t *testing.T) {
	mode := os.Getenv("OVERKILL_TESTPG_POLICY_CASE")
	if mode != "" {
		t.Setenv("PG_TEST_URL", "")
		t.Setenv("DATABASE_URL", "")
		t.Setenv("OVERKILL_REQUIRE_TEST_DB", "")
		if mode == "required" {
			t.Setenv("OVERKILL_REQUIRE_TEST_DB", "1")
		}
		if mode == "unavailable" {
			t.Setenv("PG_TEST_URL", "postgres://unused@127.0.0.1:1/unused?sslmode=disable&connect_timeout=1")
		}
		_, cleanup := Open(t)
		cleanup()
		t.Fatal("Open should skip or fail for this fixture")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, message string
		success       bool
	}{
		{"local", "SKIP", true},
		{"required", "is required", false},
		{"unavailable", "initialize isolated database", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestOpenPolicy$", "-test.v")
			cmd.Env = append(os.Environ(), "OVERKILL_TESTPG_POLICY_CASE="+c.name)
			output, runErr := cmd.CombinedOutput()
			if (runErr == nil) != c.success {
				t.Fatalf("unexpected test exit: %v\n%s", runErr, output)
			}
			if !strings.Contains(string(output), c.message) {
				t.Fatalf("missing policy outcome %q\n%s", c.message, output)
			}
		})
	}
}

func TestOpenIsolatesEveryConnectionAndCleanup(t *testing.T) {
	first, cleanup := Open(t)
	second, _ := Open(t)
	var firstSchema, secondSchema string
	if err := first.QueryRow("SELECT current_schema()").Scan(&firstSchema); err != nil {
		t.Fatal(err)
	}
	if err := second.QueryRow("SELECT current_schema()").Scan(&secondSchema); err != nil {
		t.Fatal(err)
	}
	if firstSchema == secondSchema || !strings.HasPrefix(firstSchema, "overkill_test_") {
		t.Fatal("tests must have distinct schemas")
	}
	if _, err := first.Exec("CREATE TABLE isolation_probe (value TEXT); INSERT INTO isolation_probe VALUES ('first')"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Exec("CREATE TABLE isolation_probe (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := second.QueryRow("SELECT count(*) FROM isolation_probe").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-test data leak: count=%d err=%v", count, err)
	}
	// Force an additional connection: search_path must be a pool connection
	// parameter, not a SET affecting only whichever connection was idle.
	conn, err := first.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var poolSchema string
	if err := first.QueryRow("SELECT current_schema()").Scan(&poolSchema); err != nil || poolSchema != firstSchema {
		t.Fatalf("pool lost schema: %s %v", poolSchema, err)
	}
	conn.Close()
	cleanup()
	cleanup()
	if err := second.Ping(); err != nil {
		t.Fatalf("cleanup affected another schema: %v", err)
	}
}

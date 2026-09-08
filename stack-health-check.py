#!/usr/bin/env python3
"""End-to-end stack health check — pushes to Kuma push monitors.

Uses curl for HTTP checks (HTTP/2 works through Cloudflare).
Protected services redirect to Authelia login — that's a PASS (backend is alive).
"""
import subprocess, sys, json, urllib.request

KUMA_BASE = "http://localhost:3001/api/push"
TIMEOUT = 15

SERVICES = [
    # (name, push_token, url, protected_by_authelia)
    ("Authelia",           "RprTAN9FHj", "https://auth.harshvardhanrathi.com",      False),
    ("Caddy",              "iV0rxk7NuG", "https://status1.databunker.uk",     False),
    ("Cloudflared",        "x8LdlgUEx1", "https://status1.databunker.uk",     False),
    ("PDF",                "4t1Jc9gyyL", "https://pdf.databunker.uk",        True),
    ("Sync",               "WihMA9I4KJ", "https://sync.databunker.uk",       False),  # Syncthing has built-in auth; bunker serves it without Authelia (since Aug 2026 migration)
    ("Affine",             "6wA9lomyEL", "https://affine.databunker.uk",     False),
    ("Still Here API",     "d7d68e78c8", "http://127.0.0.1:8000/health",     False),
    ("Still Here Worker",  "01f5883677", None,                                False),
    ("Still Here Beat",    "ffd7c1e46d", None,                                False),
    ("Still Here Web",     "27b432f109", "https://stillherehq.com",          False),
    ("ntfy",               "0d481f0ede", "https://ntfy.databunker.uk",       False),
    ("Docuseal",           "5976b57439", "https://sign.databunker.uk",  False),
    ("Attentive",          "d45c76df83", "https://attentive.fyi",            False),
    ("Status",             "afd3edbde4", "https://status1.databunker.uk",     False),
]


def push_kuma(token: str, ok: bool, msg: str):
    try:
        status = "0" if ok else "1"
        url = f"{KUMA_BASE}/{token}?status={status}&msg={msg}&ping="
        urllib.request.urlopen(url, timeout=5)
    except Exception:
        pass


def check_http(url: str, protected: bool) -> tuple[bool, str]:
    """Check URL via curl. Returns (ok, error_message)."""
    try:
        # Get HTTP status and final redirect URL
        r = subprocess.run(
            ["curl", "-sk", "-L", "-o", "/dev/null", "-w", "%{http_code}|%{url_effective}",
             "--max-time", str(TIMEOUT), url],
            capture_output=True, text=True, timeout=TIMEOUT + 5
        )
        if r.returncode != 0:
            return False, f"curl exit {r.returncode}"

        parts = r.stdout.strip().split("|", 1)
        code = int(parts[0]) if parts[0].isdigit() else 0
        final_url = parts[1] if len(parts) > 1 else ""

        if protected:
            # Protected services redirect to Authelia login
            ok = "auth.harshvardhanrathi.com" in final_url or "auth.databunker.uk" in final_url
            return ok, "" if ok else f"HTTP {code}, expected redirect to auth, got {final_url[:80]}"
        else:
            ok = (code == 200)
            return ok, "" if ok else f"HTTP {code}"

    except Exception as e:
        return False, str(e)


def check_celery(container: str) -> tuple[bool, str]:
    try:
        r = subprocess.run(
            ["docker", "exec", container, "celery", "-A", "tasks.escalation", "inspect", "ping"],
            capture_output=True, text=True, timeout=10
        )
        ok = r.returncode == 0 and "pong" in r.stdout.lower()
        return ok, "" if ok else "celery ping failed"
    except Exception as e:
        return False, str(e)


def main():
    results = []
    authelia_up = True

    # Phase 1: Check Authelia
    for name, token, url, protected in SERVICES:
        if name == "Authelia":
            ok, err = check_http(url, False)
            authelia_up = ok
            results.append((name, token, ok, err))

    # Phase 2: Check everything else
    for name, token, url, protected in SERVICES:
        if name == "Authelia":
            continue

        if name in ("Still Here Worker", "Still Here Beat"):
            container = f"stillhere-{name.split()[-1].lower()}"
            ok, err = check_celery(container)
            results.append((name, token, ok, err))
        elif url:
            ok, err = check_http(url, protected)
            # If Authelia is down, accept any response for protected services
            if protected and not authelia_up and not ok:
                ok = True
                err = "Authelia down, accepting response"
            results.append((name, token, ok, err))

    # Phase 3: Push to Kuma
    for name, token, ok, err in results:
        push_kuma(token, ok, "OK" if ok else (err or "failed"))

    # Phase 4: Print summary
    print("=" * 60)
    print("STACK HEALTH CHECK")
    print("=" * 60)
    for name, _, ok, err in results:
        icon = "\u2713" if ok else "\u2717"
        detail = "" if ok else f"  \u2192 {err}"
        print(f"  {icon} {name}{detail}")
    print("=" * 60)
    total = len(results)
    passing = sum(1 for _, _, ok, _ in results if ok)
    print(f"  {passing}/{total} passing")
    print()

    if passing < total:
        failing = [n for n, _, ok, _ in results if not ok]
        print(f"FAILING: {', '.join(failing)}")

    sys.exit(0 if passing == total else 1)


if __name__ == "__main__":
    main()

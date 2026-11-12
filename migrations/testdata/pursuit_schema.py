"""Regenerate pursuit_columns.json from a pursuit checkout.

    python3 migrations/testdata/pursuit_schema.py ../pursuit/internal/sqlstore/migrate.go

Applies every pursuit migration to an in-memory SQLite database and records
the columns and indexes of the tables appkit's set owns. The test compares
appkit's set against this file.
"""
import json
import os
import re
import sqlite3
import sys

TABLES = [
    "users", "api_tokens", "idempotency_keys", "audit_log", "rate_limits",
    "oauth_grants", "oauth_clients", "oauth_refresh_tokens",
    "webauthn_credentials", "webauthn_challenges",
]

src = open(sys.argv[1]).read()
src = src[src.index("var migrations"):]
db = sqlite3.connect(":memory:")
for stmt in re.findall(r"`([^`]*)`", src):
    db.execute(stmt)

out = {}
for t in TABLES:
    cols = [
        {"name": name, "type": typ.upper(), "notnull": bool(notnull), "default": dflt, "pk": bool(pk)}
        for _, name, typ, notnull, dflt, pk in db.execute(f"PRAGMA table_info({t})")
    ]
    assert cols, t
    idx = sorted(
        re.sub(r"\s+", " ", sql)
        for (sql,) in db.execute("SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL", (t,))
    )
    out[t] = {"columns": sorted(cols, key=lambda c: c["name"]), "indexes": idx}

path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pursuit_columns.json")
json.dump(out, open(path, "w"), indent=1)
open(path, "a").write("\n")

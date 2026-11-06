#!/usr/bin/env python3
"""Actual container isolation: grants, distinct UIDs, /proc, persistence and revoke."""
import base64
import json
import re
import secrets
import shlex
import sys
import urllib.error
import urllib.request
import uuid
from smoke import docker, eventually


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "lacron:ci"
    name = "lacron-env-" + uuid.uuid4().hex[:10]
    volume = name + "-data"
    token, secret_a, secret_b = (secrets.token_urlsafe(32) for _ in range(3))
    key = base64.b64encode(secrets.token_bytes(32)).decode()
    docker("volume", "create", volume)
    try:
        docker("run", "-d", "--name", name, "-p", "127.0.0.1::8080", "-v", volume + ":/data",
               "-e", "LACRON_TOKEN=" + token, "-e", "LACRON_ENCRYPTION_KEY=" + key, "--read-only", "--tmpfs", "/run", "--tmpfs", "/tmp:mode=1777",
               "--cap-drop=ALL", "--cap-add=SETUID", "--cap-add=SETGID", "--cap-add=KILL", image)
        base = "http://127.0.0.1:" + docker("port", name, "8080/tcp").stdout.strip().rsplit(":", 1)[1]

        def api(path, method="GET", body=None, status=200, auth=True):
            headers = {"Authorization": "Bearer " + token} if auth else {}
            if body is not None:
                headers["Content-Type"] = "application/json"
            req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
            try:
                res = urllib.request.urlopen(req, timeout=5)
            except urllib.error.HTTPError as exc:
                res = exc
            with res:
                raw = res.read()
                assert res.status == status, (path, res.status, raw)
                assert secret_a.encode() not in raw and secret_b.encode() not in raw, "API leaked variable values"
                return json.loads(raw) if raw else None

        eventually(lambda: api("/healthz", auth=False))
        api("/api/variables", auth=False, status=401)

        def node(source):
            return "node -e " + shlex.quote(source)

        def job(title, command):
            return {"name": title, "schedule": "* * * * * *", "command": command, "working_dir": "/scripts", "timeout_sec": 30, "enabled": False}

        private_file = "/tmp/" + name + "-private"
        owner = job("Authorized owner", node('require("node:fs").writeFileSync(' + json.dumps(private_file) + ',process.env.CHECKIN_COOKIE);console.log("pid="+process.pid); console.log("cookie="+Boolean(process.env.CHECKIN_COOKIE)); setTimeout(()=>console.log("finished"),10000)'))
        other = job("Unrelated task", "printf unrelated")
        a = api("/api/jobs", "POST", owner, status=201)
        b = api("/api/jobs", "POST", other, status=201)
        identities = {j["id"]: (j["run_uid"], j["run_gid"]) for j in api("/api/jobs")}
        assert identities[a["id"]] != identities[b["id"]]
        variable_a = api("/api/variables", "POST", {"name": "CHECKIN_COOKIE", "value": secret_a, "job_ids": [a["id"]]}, status=201)
        assert variable_a["has_value"] and "value" not in variable_a
        stat = docker("exec", name, "stat", "-c", "%a", "/data/lacron.sqlite3").stdout.strip()
        assert stat == "600", stat

        run_a = api("/api/jobs/" + a["id"] + "/run", "POST", status=202)

        def owner_pid():
            output = api("/api/runs/" + run_a["id"] + "/log")["output"]
            m = re.search(r"pid=(\d+)", output)
            assert "cookie=true" in output
            return int(m.group(1)) if m else None

        pid = eventually(owner_pid, timeout=5)
        probe = f'''const fs=require("node:fs"),assert=require("node:assert/strict");
assert.equal(process.env.CHECKIN_COOKIE,undefined);
assert.equal(process.env.LACRON_ENCRYPTION_KEY,undefined);
for(const path of ["/proc/{pid}/environ","/data/lacron.sqlite3",{json.dumps(private_file)}]){{
  let denied=false;try{{fs.readFileSync(path);}}catch(e){{denied=["EACCES","EPERM"].includes(e.code);}}
  assert(denied,"cross-task secret source readable: "+path);
}}
console.log("isolated-ok");'''
        other["command"] = node(probe)
        api("/api/jobs/" + b["id"], "PUT", other)

        def finish(run):
            result = eventually(lambda: next((r for r in api("/api/runs") if r["id"] == run["id"] and r["status"] != "running"), None))
            output = api("/api/runs/" + run["id"] + "/log")["output"]
            assert result["status"] == "success", (result, output)
            return output

        assert "isolated-ok" in finish(api("/api/jobs/" + b["id"] + "/run", "POST", status=202))
        print("Unauthorized task cannot read Cookie, another UID's /proc environment, or the root-owned store: passed", flush=True)

        # Same variable name, different task and value. Overlapping grants fail.
        variable_b = api("/api/variables", "POST", {"name": "CHECKIN_COOKIE", "value": secret_b, "job_ids": [b["id"]]}, status=201)
        api("/api/variables", "POST", {"name": "CHECKIN_COOKIE", "value": "conflict", "job_ids": [b["id"]]}, status=400)
        api("/api/variables/" + variable_a["id"], "PUT", {"name": "CHECKIN_COOKIE", "job_ids": []})
        finish(run_a)
        owner["command"] = node('console.log("hasCookie="+("CHECKIN_COOKIE" in process.env))')
        other["command"] = node('console.log("hasCookie="+Boolean(process.env.CHECKIN_COOKIE)); console.log("uid="+process.getuid())')
        api("/api/jobs/" + a["id"], "PUT", owner)
        api("/api/jobs/" + b["id"], "PUT", other)
        assert "hasCookie=false" in finish(api("/api/jobs/" + a["id"] + "/run", "POST", status=202))
        assert "hasCookie=true" in finish(api("/api/jobs/" + b["id"] + "/run", "POST", status=202))

        docker("restart", "-t", "10", name)
        base = "http://127.0.0.1:" + docker("port", name, "8080/tcp").stdout.strip().rsplit(":", 1)[1]
        eventually(lambda: api("/healthz", auth=False))
        assert {j["id"]: (j["run_uid"], j["run_gid"]) for j in api("/api/jobs")} == identities
        assert "hasCookie=true" in finish(api("/api/jobs/" + b["id"] + "/run", "POST", status=202))
        api("/api/variables/" + variable_b["id"], "DELETE", status=204)
        assert "hasCookie=false" in finish(api("/api/jobs/" + b["id"] + "/run", "POST", status=202))
        # Grants also apply to real second-level scheduled runs, not only manual ones.
        api("/api/variables/" + variable_a["id"], "PUT", {"name": "CHECKIN_COOKIE", "job_ids": [a["id"]]})
        owner["enabled"], owner["command"] = True, node('if(!process.env.CHECKIN_COOKIE)process.exit(9);console.log("scheduled-env-ok")')
        api("/api/jobs/" + a["id"], "PUT", owner)
        scheduled = eventually(lambda: next((r for r in api("/api/runs") if r["job_id"] == a["id"] and r["source"] == "cron" and r["status"] == "success"), None))
        assert "scheduled-env-ok" in api("/api/runs/" + scheduled["id"] + "/log")["output"]
        print("Same-name account variables, conflict checks, revocation, deletion, identity/grant persistence and scheduled environment injection: passed", flush=True)
    except BaseException:
        logs = docker("logs", name, check=False)
        print(logs.stdout + logs.stderr, file=sys.stderr)
        raise
    finally:
        docker("rm", "-f", name, check=False)
        docker("volume", "rm", volume, check=False)


if __name__ == "__main__":
    main()

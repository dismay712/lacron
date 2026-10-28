#!/usr/bin/env python3
"""Exercise the real Alpine image, including an actual minute-boundary cron run."""
import json
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid


def docker(*args, check=True):
    return subprocess.run(["docker", *args], check=check, text=True, capture_output=True)


def eventually(fn, timeout=30):
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        try:
            value = fn()
            if value:
                return value
        except (OSError, ValueError, AssertionError) as exc:
            last_error = exc
        time.sleep(0.5)
    raise AssertionError(f"condition not met in {timeout}s: {last_error}")


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "lacron:test"
    name = "lacron-smoke-" + uuid.uuid4().hex[:10]
    volume = name + "-data"
    token = secrets.token_urlsafe(32)
    docker("volume", "create", volume)
    try:
        docker("run", "-d", "--name", name, "-p", "127.0.0.1::8080",
               "-e", "LACRON_TOKEN=" + token, "-e", "TZ=Asia/Shanghai",
               "--read-only", "--tmpfs", "/run:rw,noexec,nosuid,size=8m,mode=0755",
               "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m,mode=1777",
               "--cap-drop", "ALL", "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "KILL",
               "--security-opt", "no-new-privileges:true", "--pids-limit", "256",
               "-v", volume + ":/data", image)
        port = docker("port", name, "8080/tcp").stdout.strip().rsplit(":", 1)[1]
        base = "http://127.0.0.1:" + port

        def api(path, method="GET", body=None, auth=True, origin=None, status=200):
            headers = {"Authorization": "Bearer " + token} if auth else {}
            if body is not None:
                headers["Content-Type"] = "application/json"
            if origin:
                headers["Origin"] = origin
            req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
            try:
                response = urllib.request.urlopen(req, timeout=5)
            except urllib.error.HTTPError as exc:
                response = exc
            with response:
                data = response.read()
                assert response.status == status, (path, response.status, data)
                return json.loads(data) if data else None

        eventually(lambda: api("/healthz", auth=False))
        api("/api/jobs", auth=False, status=401)
        system = api("/api/system")
        assert system["cron_running"] and system["timezone"] == "Asia/Shanghai"
        lts = docker("exec", name, "node", "-p", "process.release.lts").stdout.strip()
        assert lts and lts != "undefined", lts
        alpine = docker("exec", name, "cat", "/etc/alpine-release").stdout.strip()
        docker("exec", name, "npm", "--version")
        print(f"Runtime: Alpine {alpine}, Node {system['node_version']} LTS ({lts})", flush=True)

        job = {"name": "Node cron integration", "schedule": "* * * * *", "command": "node /scripts/hello.js", "working_dir": "/scripts", "timeout_sec": 10, "enabled": True}
        created = api("/api/jobs", "POST", job, status=201)
        jid = created["id"]

        def cron_completed():
            return next((r for r in api("/api/runs") if r["job_id"] == jid and r["source"] == "cron" and r["status"] == "success"), None)

        run = eventually(cron_completed, timeout=90)
        output = api("/api/runs/" + run["id"] + "/log")["output"]
        assert "Hello from lacron!" in output and "uid=10001 gid=10001" in output, output
        print("Actual BusyBox cron -> Go runner -> non-root Node.js: passed", flush=True)

        job["enabled"] = False
        job["command"] = 'node -e \'console.log("hasToken=" + ("LACRON_TOKEN" in process.env))\''
        api("/api/jobs/" + jid, "PUT", job)
        cron = docker("exec", name, "cat", "/data/crontabs/root").stdout
        assert jid not in cron, cron
        manual = api("/api/jobs/" + jid + "/run", "POST", status=202)
        finished = eventually(lambda: next((r for r in api("/api/runs") if r["id"] == manual["id"] and r["status"] == "success"), None))
        assert finished["source"] == "manual"
        assert "hasToken=false" in api("/api/runs/" + manual["id"] + "/log")["output"]
        assert docker("exec", "--user", "10001:10001", name, "cat", "/data/jobs.json", check=False).returncode != 0

        # The root supervisor needs CAP_KILL to terminate a different UID.
        # Check this under exactly the same capability set as compose.yaml.
        timeout_job = dict(job, name="Timeout process group", command="sleep 30 & wait", timeout_sec=1)
        timed = api("/api/jobs", "POST", timeout_job, status=201)
        timed_run = api("/api/jobs/" + timed["id"] + "/run", "POST", status=202)
        api("/api/jobs/" + timed["id"] + "/run", "POST", status=409)
        eventually(lambda: next((r for r in api("/api/runs") if r["id"] == timed_run["id"] and r["status"] == "timeout"), None), timeout=8)
        eventually(lambda: "sleep 30" not in docker("exec", name, "/bin/busybox", "ps").stdout)
        api("/api/jobs/" + timed["id"], "DELETE", status=204)
        invalid = dict(job, schedule="60 * * * *")
        api("/api/jobs", "POST", invalid, status=400)
        api("/api/jobs", "POST", job, origin="http://evil.test", status=403)

        docker("restart", "-t", "10", name)
        eventually(lambda: api("/healthz", auth=False))
        persisted = api("/api/jobs")
        assert len(persisted) == 1 and persisted[0]["id"] == jid and not persisted[0]["enabled"]
        assert len(api("/api/runs")) >= 2
        api("/api/jobs/" + jid, "DELETE", status=204)
        assert api("/api/jobs") == []
        print("Auth, pause/manual run, secret isolation, process-group timeout, no overlap, restart persistence, CRUD: passed", flush=True)

        docker("exec", name, "/bin/busybox", "pkill", "-x", "crond")
        exit_code = docker("wait", name).stdout.strip()
        assert exit_code != "0", "server must exit when its scheduler dies"
        print("Scheduler failure propagates to container exit: passed", flush=True)
    except BaseException:
        logs = docker("logs", name, check=False)
        print(logs.stdout + logs.stderr, file=sys.stderr)
        raise
    finally:
        docker("rm", "-f", name, check=False)
        docker("volume", "rm", volume, check=False)


if __name__ == "__main__":
    main()

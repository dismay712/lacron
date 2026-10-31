#!/usr/bin/env python3
"""Exercise second-resolution scheduling, Node/sh, and Alpine package installs."""
import json
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime


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
    readonly = "--read-only" in sys.argv[2:]
    name = "lacron-smoke-" + uuid.uuid4().hex[:10]
    volume = name + "-data"
    token = secrets.token_urlsafe(32)
    docker("volume", "create", volume)
    try:
        options = ["--read-only", "--tmpfs", "/run:rw,noexec,nosuid,size=8m,mode=0755",
                   "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m,mode=1777",
                   "--cap-drop", "ALL", "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "KILL",
                   "--security-opt", "no-new-privileges:true"] if readonly else ["-e", "LACRON_APK_PACKAGES=bash curl jq"]
        docker("run", "-d", "--name", name, "-p", "127.0.0.1::8080",
               "-e", "LACRON_TOKEN=" + token, "-e", "TZ=Asia/Shanghai",
               *options, "--pids-limit", "256",
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

        eventually(lambda: api("/healthz", auth=False), timeout=90)
        api("/api/jobs", auth=False, status=401)
        system = api("/api/system")
        assert system["cron_running"] and system["timezone"] == "Asia/Shanghai"
        assert system["scheduler"] == "go" and system["schedule_precision"] == "second"
        lts = docker("exec", name, "node", "-p", "process.release.lts").stdout.strip()
        assert lts and lts != "undefined", lts
        alpine = docker("exec", name, "cat", "/etc/alpine-release").stdout.strip()
        docker("exec", name, "npm", "--version")
        print(f"Runtime: Alpine {alpine}, Node {system['node_version']} LTS ({lts})", flush=True)

        if not readonly:
            for package in ("bash", "curl", "jq"):
                docker("exec", name, "apk", "info", "-e", package)
            docker("exec", "--user", "0:0", name, "apk", "add", "--no-cache", "file")
            docker("exec", name, "file", "--version")
            print("Startup package list and administrator apk add: passed", flush=True)
        else:
            assert docker("exec", name, "touch", "/read-only-check", check=False).returncode != 0

        job = {"name": "Node second cron integration", "schedule": "*/2 * * * * *", "command": "node /scripts/hello.js", "working_dir": "/scripts", "timeout_sec": 10, "enabled": True}
        created = api("/api/jobs", "POST", job, status=201)
        jid = created["id"]

        def cron_completed():
            return next((r for r in api("/api/runs") if r["job_id"] == jid and r["source"] == "cron" and r["status"] == "success"), None)

        run = eventually(cron_completed, timeout=10)
        output = api("/api/runs/" + run["id"] + "/log")["output"]
        assert "Hello from lacron!" in output and "uid=10001 gid=10001" in output, output
        def two_cron_runs():
            completed = [r for r in api("/api/runs") if r["job_id"] == jid and r["source"] == "cron" and r["status"] == "success"]
            return completed if len(completed) >= 2 else None

        runs = eventually(two_cron_runs, timeout=10)
        started = sorted(datetime.fromisoformat(r["started_at"].replace("Z", "+00:00")) for r in runs)
        assert 1 < (started[1] - started[0]).total_seconds() < 3.5, started
        print("Actual second cron -> non-root Node.js, two consecutive two-second runs: passed", flush=True)

        job["enabled"] = False
        job["command"] = 'node -e \'console.log("hasToken=" + ("LACRON_TOKEN" in process.env))\''
        api("/api/jobs/" + jid, "PUT", job)
        manual = api("/api/jobs/" + jid + "/run", "POST", status=202)
        finished = eventually(lambda: next((r for r in api("/api/runs") if r["id"] == manual["id"] and r["status"] == "success"), None))
        assert finished["source"] == "manual"
        assert "hasToken=false" in api("/api/runs/" + manual["id"] + "/log")["output"]
        assert docker("exec", "--user", "10001:10001", name, "cat", "/data/jobs.json", check=False).returncode != 0

        shell_command = "sh /scripts/hello.sh; id -u"
        if not readonly:
            shell_command += "; bash -c 'echo bash-ok'; printf '{\"ok\":true}' | jq -r .ok; curl --version"
        shell = api("/api/jobs", "POST", dict(job, name="Shell second cron", schedule="* * * * * *", command=shell_command, enabled=True), status=201)
        shell_run = eventually(lambda: next((r for r in api("/api/runs") if r["job_id"] == shell["id"] and r["source"] == "cron" and r["status"] == "success"), None), timeout=10)
        shell_job = dict(job, name="Shell second cron", schedule="* * * * * *", command=shell_command, enabled=False)
        api("/api/jobs/" + shell["id"], "PUT", shell_job)
        shell_output = api("/api/runs/" + shell_run["id"] + "/log")["output"]
        assert "Hello from lacron!" in shell_output and "10001" in shell_output, shell_output
        if not readonly:
            assert "bash-ok" in shell_output and "true" in shell_output and "curl" in shell_output, shell_output
        eventually(lambda: not next(j for j in api("/api/jobs") if j["id"] == shell["id"])["running"])
        api("/api/jobs/" + shell["id"], "DELETE", status=204)
        print("Scheduled sh script and installed package commands under UID 10001: passed", flush=True)

        # The root supervisor needs CAP_KILL to terminate a different UID.
        # Verify both default capabilities and the optional minimal set.
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
        # Docker may allocate a new host port for a dynamically published port
        # when the container restarts. Resolve it again before checking HTTP.
        port = docker("port", name, "8080/tcp").stdout.strip().rsplit(":", 1)[1]
        base = "http://127.0.0.1:" + port
        eventually(lambda: api("/healthz", auth=False), timeout=90)
        persisted = api("/api/jobs")
        assert len(persisted) == 1 and persisted[0]["id"] == jid and not persisted[0]["enabled"]
        assert len(api("/api/runs")) >= 2
        api("/api/jobs/" + jid, "DELETE", status=204)
        assert api("/api/jobs") == []
        print("Auth, pause/manual run, secret isolation, process-group timeout, no overlap, restart persistence, CRUD: passed", flush=True)

        docker("stop", "-t", "10", name)
        assert docker("inspect", "--format", "{{.State.ExitCode}}", name).stdout.strip() == "0"
        print("Go scheduler and container stop cleanly: passed", flush=True)
    except BaseException:
        logs = docker("logs", name, check=False)
        print(logs.stdout + logs.stderr, file=sys.stderr)
        raise
    finally:
        docker("rm", "-f", name, check=False)
        docker("volume", "rm", volume, check=False)


if __name__ == "__main__":
    main()

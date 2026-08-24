#!/usr/bin/env python3
"""Run one existing-account relogin with isolated, permission-restricted artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent
RUNTIME_ROOT = ROOT / "runtime"
CANDIDATE_STATE_PATH = RUNTIME_ROOT / "candidate-state.json"
CDP_PORT = 9224
REQUIRED_CONFIGS = (ROOT / "config.json", ROOT / "config.jsonc")
REQUIRED_MODULES = ("playwright", "pyotp", "curl_cffi")
AUTH_TOKEN_KEY_NAMES = {
    "accesstoken",
    "sessiontoken",
    "refreshtoken",
    "idtoken",
}


def set_mode(path: Path, mode: int) -> None:
    path.chmod(mode)


def iso_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def command_available(name: str) -> bool:
    return shutil.which(name) is not None


def redact_text(value: Any, limit: int = 240) -> str:
    text = str(value or "").strip().replace("\n", " ")
    if len(text) > limit:
        return text[:limit] + "..."
    return text


def secret_values(payload: dict[str, Any]) -> list[str]:
    values: list[str] = []

    def collect(item: Any, sensitive: bool = False) -> None:
        if isinstance(item, dict):
            for key, value in item.items():
                key_is_sensitive = key.lower() in {"password", "secret", "access_token", "session_token", "id_token", "refresh_token"}
                collect(value, sensitive or key_is_sensitive)
        elif sensitive and isinstance(item, str) and len(item.strip()) >= 4:
            values.append(item.strip())

    collect(payload)
    return sorted(set(values), key=len, reverse=True)


def redact_script_output(output: str, secrets: list[str]) -> str:
    for value in secrets:
        output = output.replace(value, "[REDACTED]")
    field = r"access[_ -]?token|session[_ -]?token|refresh[_ -]?token|id[_ -]?token|password|totp(?:[_ -]?secret)?|cookie|proxy"
    output = re.sub(
        rf"(?i)([\"']?(?:{field})[\"']?\s*:\s*[\"'])[^\"']*",
        r"\1[REDACTED]",
        output,
    )
    output = re.sub(
        rf"(?i)([\"']?(?:{field})[\"']?\s*:\s*)[^\s,}}\]]+",
        r"\1[REDACTED]",
        output,
    )
    output = re.sub(
        rf"(?i)(\b(?:{field})\b\s*=\s*)[^\s,]+",
        r"\1[REDACTED]",
        output,
    )
    return output


def classify_script_result(output: str) -> tuple[bool, str, str]:
    """Classify the single-account CLI result without trusting input tokens."""
    if re.search(r"^\s*\[OK\]\s+", output, re.MULTILINE):
        return True, "", ""
    if "External identity provider redirect detected" in output:
        return False, "external_identity_provider", (
            "Account requires an external identity provider; email OTP M1 does not support this login path"
        )
    if "Account DEACTIVATED" in output:
        return False, "account_deactivated", "Account has been deleted or deactivated by OpenAI (account_deactivated)"
    if "OTP timeout" in output:
        return False, "otp_timeout", "OTP not received within timeout"
    return False, "script_failed_without_result", "login script did not report a successful single-account result"


def account_summary(payload: dict[str, Any]) -> dict[str, Any]:
    totp = payload.get("totp")
    return {
        "email_domain": str(payload.get("email", "")).rsplit("@", 1)[-1].lower() if "@" in str(payload.get("email", "")) else "",
        "has_password": bool(str(payload.get("password", "")).strip()),
        "has_totp": isinstance(totp, dict) and bool(str(totp.get("secret", "")).strip()),
        "has_fingerprint": isinstance(payload.get("fingerprint"), dict),
        "has_access_token": bool(str(payload.get("access_token", "")).strip()),
        "has_session_token": bool(str(payload.get("session_token", "")).strip()),
    }


def validate_account_input(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        raise ValueError(f"account JSON cannot be read: {exc}") from exc
    if not isinstance(payload, dict):
        raise ValueError("account JSON must be an object")
    if not str(payload.get("email", "")).strip():
        raise ValueError("account JSON is missing email")
    if not str(payload["email"]).strip().lower().endswith("@zainy.art"):
        raise ValueError("M1 only supports @zainy.art accounts")
    if not str(payload.get("password", "")).strip():
        raise ValueError("account JSON is missing password")
    if payload.get("is-ban") is True:
        raise ValueError("account JSON is marked is-ban=true and cannot be relogged")
    return payload


def inspect_cdp() -> list[str]:
    if not command_available("ss"):
        return []
    result = subprocess.run(
        ["ss", "-ltnp", f"sport = :{CDP_PORT}"],
        capture_output=True,
        text=True,
        check=False,
    )
    return [line.strip() for line in result.stdout.splitlines()[1:] if line.strip()]


def clean_stale_cdp() -> tuple[bool, str]:
    listeners = inspect_cdp()
    if not listeners:
        return True, "no listener on CDP port"
    if not command_available("pkill"):
        return False, "CDP port is occupied and pkill is unavailable"
    result = subprocess.run(
        ["pkill", "-f", f"--remote-debugging-port={CDP_PORT}"],
        capture_output=True,
        text=True,
        check=False,
    )
    time.sleep(1)
    if inspect_cdp():
        return False, "CDP port remains occupied after targeted Chrome cleanup"
    if result.returncode not in (0, 1):
        return False, "targeted Chrome cleanup returned an unexpected status"
    return True, "cleared stale CDP listener"


def preflight() -> dict[str, Any]:
    checks: list[dict[str, Any]] = []

    def add(name: str, ok: bool, detail: str) -> None:
        checks.append({"name": name, "ok": ok, "detail": detail})

    add("platform", sys.platform.startswith("linux"), f"platform={sys.platform}")
    add("display", bool(os.environ.get("DISPLAY")), "DISPLAY is set" if os.environ.get("DISPLAY") else "DISPLAY is not set")
    add("chrome", command_available("google-chrome") or command_available("google-chrome-stable"), "Google Chrome found" if command_available("google-chrome") or command_available("google-chrome-stable") else "Google Chrome not found on PATH")
    add("xvfb", command_available("Xvfb"), "Xvfb found" if command_available("Xvfb") else "Xvfb not found on PATH")
    add("config.json", REQUIRED_CONFIGS[0].is_file(), "exists" if REQUIRED_CONFIGS[0].is_file() else "missing")
    add("config.jsonc", REQUIRED_CONFIGS[1].is_file(), "exists" if REQUIRED_CONFIGS[1].is_file() else "missing")
    for path in REQUIRED_CONFIGS:
        if path.is_file():
            mode = stat.S_IMODE(path.stat().st_mode)
            add(f"{path.name} mode", mode == 0o600, f"mode={mode:03o}")
    for module in REQUIRED_MODULES:
        result = subprocess.run([sys.executable, "-c", f"import {module}"], capture_output=True, text=True, check=False)
        add(f"python:{module}", result.returncode == 0, "importable" if result.returncode == 0 else "not importable")
    listeners = inspect_cdp()
    add("cdp_port", not listeners, "available" if not listeners else "occupied")
    return {"checked_at": iso_now(), "checks": checks, "ok": all(item["ok"] for item in checks)}


def write_json(path: Path, payload: dict[str, Any], mode: int = 0o600) -> None:
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    set_mode(path, mode)


def persist_verified_session(source: Path, refreshed_copy: Path, expected_source_sha256: str) -> None:
    """Atomically replace the source JSON only if it was not concurrently changed."""
    current_sha256 = hashlib.sha256(source.read_bytes()).hexdigest()
    if current_sha256 != expected_source_sha256:
        raise RuntimeError("source account JSON changed during relogin; refusing to overwrite it")

    # Validate the refreshed payload before replacing the source file.
    json.loads(refreshed_copy.read_text(encoding="utf-8"))
    source_mode = stat.S_IMODE(source.stat().st_mode)
    temporary = source.with_name(f".{source.name}.{uuid.uuid4().hex}.tmp")
    try:
        shutil.copyfile(refreshed_copy, temporary)
        set_mode(temporary, source_mode)
        os.replace(temporary, source)
    finally:
        temporary.unlink(missing_ok=True)


def atomic_write_json(path: Path, payload: dict[str, Any], mode: int, *, ensure_ascii: bool) -> None:
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    try:
        temporary.write_text(json.dumps(payload, ensure_ascii=ensure_ascii, indent=2) + "\n", encoding="utf-8")
        set_mode(temporary, mode)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def clear_auth_tokens(payload: Any) -> None:
    if isinstance(payload, dict):
        for key, value in payload.items():
            normalized_key = "".join(character for character in str(key).lower() if character.isalnum())
            if normalized_key in AUTH_TOKEN_KEY_NAMES:
                payload[key] = ""
            else:
                clear_auth_tokens(value)
    elif isinstance(payload, list):
        for value in payload:
            clear_auth_tokens(value)


def mark_source_deactivated(source: Path, expected_source_sha256: str) -> str:
    """Mark a source account as unusable only after an explicit OpenAI response."""
    current_sha256 = hashlib.sha256(source.read_bytes()).hexdigest()
    if current_sha256 != expected_source_sha256:
        raise RuntimeError("source account JSON changed during relogin; refusing to mark it deactivated")
    payload = json.loads(source.read_text(encoding="utf-8"))
    if not isinstance(payload, dict):
        raise RuntimeError("source account JSON must be an object")
    clear_auth_tokens(payload)
    payload["is-ban"] = True
    payload["success"] = False
    payload["stage"] = "account_deactivated"
    payload["error_message"] = "Account has been deleted or deactivated by OpenAI (account_deactivated)"
    payload["session_refreshed_at"] = iso_now()
    source_mode = stat.S_IMODE(source.stat().st_mode)
    atomic_write_json(source, payload, source_mode, ensure_ascii=False)
    return hashlib.sha256(source.read_bytes()).hexdigest()


def record_deactivated_candidate(source_sha256: str) -> None:
    """Store only the deactivated source hash so future M1 runs skip it."""
    state: dict[str, Any] = {}
    if CANDIDATE_STATE_PATH.exists():
        loaded = json.loads(CANDIDATE_STATE_PATH.read_text(encoding="utf-8"))
        if isinstance(loaded, dict):
            state = loaded
    confirmed = {item for item in state.get("confirmed_deactivated_sha256", []) if isinstance(item, str)}
    tested = {item for item in state.get("tested_sha256", []) if isinstance(item, str)}
    confirmed.add(source_sha256)
    tested.add(source_sha256)
    state["confirmed_deactivated_sha256"] = sorted(confirmed)
    state["tested_sha256"] = sorted(tested)
    state["notes"] = "SHA-256-only candidate state; no account identifiers or credentials."
    state_mode = stat.S_IMODE(CANDIDATE_STATE_PATH.stat().st_mode) if CANDIDATE_STATE_PATH.exists() else 0o600
    atomic_write_json(CANDIDATE_STATE_PATH, state, state_mode, ensure_ascii=True)


def main() -> int:
    parser = argparse.ArgumentParser(description="Run one isolated email-relogin attempt")
    parser.add_argument("--account-json", type=Path, required=False, help="Local account JSON; copied into this run directory")
    parser.add_argument("--run-id", default="", help="Optional safe run identifier")
    parser.add_argument("--timeout", type=int, default=300, help="Maximum script runtime in seconds")
    parser.add_argument("--clean-stale-cdp", action="store_true", help="Clear a Chrome process listening on CDP 9224 before running")
    parser.add_argument("--preflight-only", action="store_true", help="Write environment checks without running a login")
    args = parser.parse_args()
    if not args.preflight_only and not args.account_json:
        parser.error("--account-json is required unless --preflight-only is used")

    run_id = args.run_id.strip() or datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:8]
    if not run_id.replace("-", "").replace("_", "").isalnum():
        parser.error("run-id may contain only letters, digits, hyphens, and underscores")
    run_dir = RUNTIME_ROOT / run_id
    if run_dir.exists():
        parser.error(f"run directory already exists: {run_dir}")
    run_dir.mkdir(parents=True, mode=0o700)
    set_mode(RUNTIME_ROOT, 0o700)
    screenshots_dir = run_dir / "screenshots"
    screenshots_dir.mkdir(mode=0o700)
    log_path = run_dir / "run.log"
    cleanup_result: dict[str, Any] | None = None
    if args.clean_stale_cdp:
        ok, detail = clean_stale_cdp()
        cleanup_result = {"checked_at": iso_now(), "ok": ok, "detail": detail}
        write_json(run_dir / "cdp_cleanup.json", cleanup_result)
    preflight_result = preflight()
    write_json(run_dir / "preflight.json", preflight_result)

    if args.preflight_only:
        write_json(run_dir / "summary.json", {"run_id": run_id, "status": "preflight", "preflight_ok": preflight_result["ok"]})
        print(run_dir)
        return 0 if preflight_result["ok"] else 2
    if not preflight_result["ok"]:
        write_json(run_dir / "summary.json", {"run_id": run_id, "status": "blocked", "reason": "preflight_failed"})
        print(run_dir)
        return 2
    if cleanup_result is not None:
        if not cleanup_result["ok"]:
            write_json(run_dir / "summary.json", {"run_id": run_id, "status": "blocked", "reason": "cdp_cleanup_failed"})
            print(run_dir)
            return 2
    payload = validate_account_input(args.account_json)
    source_sha256 = hashlib.sha256(args.account_json.read_bytes()).hexdigest()
    account_copy = run_dir / "account.json"
    shutil.copyfile(args.account_json, account_copy)
    set_mode(account_copy, 0o600)
    started_at = iso_now()
    command = [sys.executable, "email_login_turnstile.py", "--fingerprint-json", str(account_copy), "--no-adjust-settings", "--no-chat"]
    environment = os.environ.copy()
    environment["EMAIL_RELOGIN_SCREENSHOT_DIR"] = str(screenshots_dir)
    environment["PYTHONUNBUFFERED"] = "1"
    try:
        completed = subprocess.run(command, cwd=ROOT, env=environment, capture_output=True, text=True, timeout=args.timeout, check=False)
        exit_code = completed.returncode
        timed_out = False
        script_output = completed.stdout or ""
    except subprocess.TimeoutExpired as exc:
        exit_code = None
        timed_out = True
        script_output = exc.stdout or ""
        if isinstance(script_output, bytes):
            script_output = script_output.decode(errors="replace")
    log_path.write_text(redact_script_output(script_output, secret_values(payload)), encoding="utf-8")
    set_mode(log_path, 0o600)
    result: dict[str, Any] = {}
    try:
        result = json.loads(account_copy.read_text(encoding="utf-8"))
    except Exception:
        pass
    script_succeeded, detected_stage, detected_error = classify_script_result(script_output)
    if timed_out:
        stage = "timeout"
        error_message = "login process exceeded the configured timeout"
        summary_account = account_summary(payload)
        # Input JSON may contain an old session. A timed-out run did not
        # establish or verify a current token, so do not report one here.
        summary_account["has_access_token"] = False
        summary_account["has_session_token"] = False
    else:
        if script_succeeded and result.get("success") is True:
            stage = redact_text(result.get("stage", "script_result_unavailable"))
            error_message = redact_script_output(redact_text(result.get("error_message", "")), secret_values(payload))
            summary_account = account_summary(result)
        else:
            stage = detected_stage
            error_message = detected_error
            summary_account = account_summary(payload)
            summary_account["has_access_token"] = False
            summary_account["has_session_token"] = False
    verified_success = exit_code == 0 and script_succeeded and result.get("success") is True
    persisted_source = False
    deactivation_marked = False
    deactivation_marker_error = ""
    if verified_success:
        try:
            persist_verified_session(args.account_json, account_copy, source_sha256)
            persisted_source = True
        except Exception as exc:
            verified_success = False
            stage = "session_persist_failed"
            error_message = redact_text(exc)
    elif stage == "account_deactivated":
        try:
            deactivated_source_sha256 = mark_source_deactivated(args.account_json, source_sha256)
            record_deactivated_candidate(deactivated_source_sha256)
            deactivation_marked = True
        except Exception as exc:
            deactivation_marker_error = redact_text(exc)
    summary = {
        "run_id": run_id,
        "status": "success" if verified_success else "failed",
        "started_at": started_at,
        "finished_at": iso_now(),
        "exit_code": exit_code,
        "timed_out": timed_out,
        "stage": stage,
        "error_message": error_message,
        "account": summary_account,
        "source_json_updated": persisted_source,
        "source_deactivation_marked": deactivation_marked,
        "source_deactivation_marker_error": deactivation_marker_error,
        "screenshot_count": len(list(screenshots_dir.glob("*.png"))),
        "log_file": "run.log",
    }
    write_json(run_dir / "summary.json", summary)
    account_copy.unlink(missing_ok=True)
    print(run_dir)
    return 0 if summary["status"] == "success" else 1


if __name__ == "__main__":
    raise SystemExit(main())

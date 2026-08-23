"""
Email Registration via Log-In Flow — Playwright + Real Chrome CDP.

⚠️  IMPORTANT: This script registers NEW accounts through the email-OTP
    log-in route.  The normal flow is:
      首页点击 "Log in" → 输入邮箱 → 邮箱验证码(OTP) → about_you → 登录即注册
    If OpenAI redirects to create-account/password, that is an unexpected
    deviation from this flow — the account should be abandoned and retried
    with a fresh session, NOT handled by filling in a password.

Uses real Chrome browser via CDP.  Fetches /api/auth/session to extract
accessToken + sessionToken after successful login.

Usage:
    # Auto-generate email + password (default, no args needed):
    python email_login.py

    # Specify email explicitly:
    python email_login.py --email "test@zainy.art"

    # Full control:
    python email_login.py --email "test@zainy.art" --password "MyP@ssword123" --proxy "socks5h://127.0.0.1:1080"

    # Skip TOTP setup:
    python email_login.py --no-totp

    # Batch register 5 accounts, 10s between each:
    python email_login.py --count 5 --delay 10

    # Batch with retry on failure, interactive mode for debugging:
    python email_login.py --count 3 --on-failure retry --retry-max 3 --interactive

Parameters (defaults shown in []):
    --email EMAIL            Email to use. Auto-generated from zoho catch-all if omitted.
    --password PASSWORD      Password to use. Auto-generated (12 chars) if omitted.
    --proxy PROXY            Proxy URL. Read from email-reg/config.json if omitted.
    --headless               Run Chrome headless. [off]
    --password-length N      Length of auto-generated password. [12]
    --recon                  Print detailed page snapshot (visible buttons, inputs,
                             headings, cookies, body text) at key steps. Useful for
                             diagnosing "stuck at X page" issues. [off]
    --keep-logs              Include verbose logs in output JSON. [off]
    --no-totp                Skip TOTP authenticator setup. TOTP is enabled by default.
    --fingerprint-json PATH  Restore fingerprint from a previous session JSON.
    --count N                Number of accounts to register. [1]
    --delay SECONDS          Seconds to wait between registrations. [2.0]
    --on-failure STRATEGY    What to do when a registration fails:
                               skip  — log the failure and continue to the next slot
                               stop  — abort the entire batch immediately
                               retry — generate a NEW email+fingerprint and try again
                                       (up to --retry-max attempts per slot)  [skip]
    --retry-max N            Max attempts per slot when --on-failure=retry. [10]
    --interactive            When an unexpected page or error occurs, pause and wait
                             for you to press Enter (inspect browser, take manual
                             action). Without this flag, errors auto-continue. [off]
"""

import json
import os
import sys
import time
import secrets
import random
import re
import base64
import argparse
import urllib.parse
from datetime import datetime, timezone, timedelta
from pathlib import Path

# All runtime dependencies are intentionally colocated with this script so
# the deployment bundle has no dependency on the repository layout.
_THIS_DIR = Path(__file__).resolve().parent

from fingerprint import Fingerprint
from mailbox import MailboxAccount, ZohoMailbox

try:
    from playwright.sync_api import sync_playwright
except ImportError:
    print("[Error] pip install playwright && playwright install chromium")
    raise SystemExit(1)

try:
    from turnstile_controller import (
        TurnstileController,
        TurnstilePolicy,
        turnstile_extension_path,
    )
except ImportError:
    print("[WARN] turnstile_controller not found — CF challenges will NOT be auto-solved")
    TurnstileController = None
    TurnstilePolicy = None
    turnstile_extension_path = None


class _PlaywrightTurnstilePage:
    """Adapt a Playwright page to the small DrissionPage surface TurnstileController needs.

    TurnstileController calls ``page.run_js(script, *args)`` for inspection,
    reset, token injection and the JS-fallback click, and ``page.ele(...)``
    only inside its shadow-DOM click path. We implement ``run_js`` over
    Playwright's ``evaluate`` and leave ``ele`` returning ``None`` so the
    shadow path is skipped and trigger() falls through to the pure-JS click
    (which the bundled extension also performs at ``document_start``).
    """

    def __init__(self, page):
        self.page = page

    def run_js(self, script, *args):
        # DrissionPage run_js treats `script` as a function body. Wrap it so
        # top-level `return` statements and `arguments[0]` work under
        # Playwright.evaluate (which otherwise parses it as an expression).
        if not args:
            expr = f"(function() {{ {script} }})()"
        else:
            args_js = ", ".join(json.dumps(a) for a in args)
            expr = f"(function() {{ {script} }})({args_js})"
        return self.page.evaluate(expr)

    def ele(self, locator, timeout=None, **kwargs):
        # Not needed — shadow-DOM trigger path is bypassed in favour of the
        # JS fallback click + bundled extension.
        return None


def _wait_for_element_after_cf(page, target_selectors, timeout=360, **kwargs):
    """Wait for one of *target_selectors*, solving Cloudflare Turnstile via TurnstileController.

    Replaces the old ``cf_turnstile_solver`` (Playwright network detection +
    OS-level mouse / OpenCV solving) with the reusable ``turnstile_controller``
    module. Keeps the same call signature and return dict shape so the four
    call sites in this file need no changes.
    """
    force_cf = bool(kwargs.get("force_cf", False))
    if isinstance(target_selectors, str):
        target_selectors = [target_selectors]

    start = time.time()
    deadline = start + timeout
    matched = None
    cf_triggered = False
    cf_solved = False

    if TurnstileController is None:
        # Module missing — passive visibility wait only.
        return _passive_target_wait(page, target_selectors, deadline, start)

    adapter = _PlaywrightTurnstilePage(page)
    controller = TurnstileController(
        adapter,
        policy=TurnstilePolicy(timeout=60, reset_on_start=True),
        log=lambda m: print(f"    [CF] {m}"),
    )

    # If the page already shows a challenge (force_cf), solve it up front.
    if force_cf:
        cf_triggered = True
        r = controller.wait_for_token()
        if r.ok:
            cf_solved = True
        try:
            page.wait_for_timeout(2000)  # let the challenge redirect the page
        except Exception:
            time.sleep(2)

    while time.time() < deadline:
        for sel in target_selectors:
            try:
                if page.locator(sel).first.is_visible(timeout=1000):
                    matched = sel
                    break
            except Exception:
                continue
        if matched:
            break

        # If a Turnstile widget is present, solve it, then resume waiting.
        try:
            snap = controller.inspect()
        except Exception:
            snap = None
        if snap is not None and snap.present:
            cf_triggered = True
            r = controller.wait_for_token()
            if r.ok:
                cf_solved = True
                try:
                    page.wait_for_timeout(2000)
                except Exception:
                    time.sleep(2)
            else:
                try:
                    page.wait_for_timeout(1500)
                except Exception:
                    time.sleep(1.5)
        else:
            try:
                page.wait_for_timeout(1500)
            except Exception:
                time.sleep(1.5)

    return {
        "target_found": matched is not None,
        "cf_triggered": cf_triggered,
        "cf_solved": cf_solved,
        "cf_proc_launched": False,
        "elapsed": time.time() - start,
        "matched_selector": matched,
    }


def _passive_target_wait(page, target_selectors, deadline, start):
    """Pure-polling wait — used when turnstile_controller is unavailable."""
    while time.time() < deadline:
        for sel in target_selectors:
            try:
                if page.locator(sel).first.is_visible(timeout=1000):
                    return {
                        "target_found": True, "cf_triggered": False,
                        "cf_solved": False, "cf_proc_launched": False,
                        "elapsed": time.time() - start, "matched_selector": sel,
                    }
            except Exception:
                continue
        try:
            page.wait_for_timeout(1500)
        except Exception:
            time.sleep(1.5)
    return {
        "target_found": False, "cf_triggered": False, "cf_solved": False,
        "cf_proc_launched": False, "elapsed": time.time() - start,
        "matched_selector": None,
    }

try:
    from human_delay import calculate_inter_char_delay as _inter_char_delay
except ImportError:
    _inter_char_delay = None

# Sound alarm (optional — requires winsound or playsound)
try:
    from sound_alarm import play_alarm as _play_alarm
except ImportError:
    _play_alarm = None

from constants import generate_random_user_info

# ── Text-based page-state fallback matcher ──
try:
    from page_state_matcher import PageStateMatcher
except ImportError:
    PageStateMatcher = None

# ── Human Simulator toggle (default on, HUMAN_SIM=0 to disable) ──
_HUMAN_SIM = os.environ.get("HUMAN_SIM", "1") == "1"

try:
    from page_interaction import PageInteractionBridge
except ImportError:
    PageInteractionBridge = None

# ═══════════════════════════════════════════════════════════════════════════
# CDP anti-detection script (shared — also used by browser restart)
# ═══════════════════════════════════════════════════════════════════════════

_CDP_ANTI_DETECTION_SCRIPT = """
// ── 1. Navigator webdriver ──────────────────────────────────────────
if (navigator.webdriver === true) {
    Object.defineProperty(navigator, 'webdriver', {get: () => false});
}

// ── 2. window.chrome (realistic structure) ──────────────────────────
// CF enumerates chrome.runtime to detect shallow fakes.
const _fakeRuntime = {
    id: undefined,
    lastError: undefined,
    connect: function() { return { name: '', disconnect: function() {}, onDisconnect: {addListener:function(){},removeListener:function(){}}, onMessage: {addListener:function(){},removeListener:function(){}}, sender: undefined, postMessage: function() {} }; },
    sendMessage: function() {},
    getManifest: function() { return {}; },
    getURL: function(path) { return 'chrome-extension://invalid/' + path; },
    getBackgroundPage: function() { return null; },
    onConnect: { addListener: function() {}, removeListener: function() {}, hasListeners: function() { return false; } },
    onMessage: { addListener: function() {}, removeListener: function() {}, hasListeners: function() { return false; } },
    onInstalled: { addListener: function() {}, removeListener: function() {}, hasListeners: function() { return false; } },
};
// Patch runtime.toString to return the native string
_fakeRuntime.toString = function() { return 'function () { [native code] }'; };
window.chrome = {
    app: {},
    runtime: _fakeRuntime,
    loadTimes: function() { return {}; },
    csi: function() { return {}; },
};

// ── 3. Navigator plugins (Chrome PDF Viewer etc.) ───────────────────
// CF checks navigator.plugins.length and plugin names.
try {
    const _makePlugin = (name, filename, desc) => {
        const p = { name, filename, description: desc, length: 0 };
        p.item = function(i) { return i === 0 ? {type: '', description: ''} : null; };
        p.namedItem = function(n) { return null; };
        p.refresh = function() {};
        Object.setPrototypeOf(p, Plugin.prototype);
        return p;
    };
    const _plugins = [
        _makePlugin('Chrome PDF Plugin', 'internal-pdf-viewer', 'Portable Document Format'),
        _makePlugin('Chrome PDF Viewer', 'mhjfbmdgcfjbbpaeojofohoefgiehjai', ''),
    ];
    // Make it look like a PluginArray
    _plugins.item = function(i) { return this[i] || null; };
    _plugins.namedItem = function(n) { return this.find(p => p.name === n) || null; };
    _plugins.refresh = function() {};
    Object.setPrototypeOf(_plugins, PluginArray.prototype);
    Object.defineProperty(navigator, 'plugins', { get: () => _plugins, configurable: true });
    Object.defineProperty(navigator, 'mimeTypes', {
        get: () => {
            const mt = Object.setPrototypeOf([], MimeTypeArray.prototype);
            mt.item = function(i) { return null; };
            mt.namedItem = function(n) { return null; };
            mt.refresh = function() {};
            return mt;
        },
        configurable: true,
    });
} catch(e) {}

// ── 4. Block CDP port detection ─────────────────────────────────────
// CF probes 127.0.0.1:<port> from the page to detect debugger/automation.
// We must intercept fetch / WebSocket / XMLHttpRequest BEFORE CF scripts run.
(function() {
    const _BLOCKED = /127\\.0\\.0\\.1:(9222|9223|9224|9225|9226|9227|9228|9229)/;
    const _BLOCKED_HOST = /localhost:(922[2-9])/;

    // --- fetch ---
    const _fetch = window.fetch;
    window.fetch = function(input, init) {
        let url = '';
        if (typeof input === 'string') url = input;
        else if (input && input.url) url = input.url;
        else if (input && input.href) url = input.href;
        try { url = String(url); } catch(e) {}
        if (_BLOCKED.test(url) || _BLOCKED_HOST.test(url)) {
            return Promise.reject(new TypeError('Failed to fetch'));
        }
        if (typeof Request !== 'undefined' && input instanceof Request) {
            return _fetch.call(this, input, init);
        }
        return _fetch.call(this, input, init);
    };

    // --- WebSocket ---
    if (typeof WebSocket !== 'undefined') {
        const _WS = WebSocket;
        const _WSProto = WebSocket.prototype;
        window.WebSocket = function(url, protocols) {
            const u = String(url || '');
            if (_BLOCKED.test(u) || _BLOCKED_HOST.test(u)) {
                const err = new DOMException('Connection refused', 'NetworkError');
                // Throw in a way that looks like a native WebSocket failure
                throw err;
            }
            if (protocols !== undefined) return new _WS(url, protocols);
            return new _WS(url);
        };
        window.WebSocket.prototype = _WSProto;
        window.WebSocket.CONNECTING = _WS.CONNECTING;
        window.WebSocket.OPEN = _WS.OPEN;
        window.WebSocket.CLOSING = _WS.CLOSING;
        window.WebSocket.CLOSED = _WS.CLOSED;
    }

    // --- XMLHttpRequest.open ---
    if (typeof XMLHttpRequest !== 'undefined') {
        const _open = XMLHttpRequest.prototype.open;
        XMLHttpRequest.prototype.open = function(method, url, async, user, password) {
            const u = String(url || '');
            if (_BLOCKED.test(u) || _BLOCKED_HOST.test(u)) {
                throw new DOMException('NetworkError', 'NetworkError');
            }
            return _open.call(this, method, url, async !== false, user, password);
        };
    }
})();
"""


# ═══════════════════════════════════════════════════════════════════════════
# Helpers
# ═══════════════════════════════════════════════════════════════════════════

def _email_derive_name(email: str) -> str:
    """Derive a human name from an email address's local part.

    Parses patterns like ``john.smith`` → "John Smith", ``lisa.jones`` →
    "Lisa Jones".  Strips digits (e.g. janice73 → Janice) because ChatGPT
    rejects names containing numbers.
    Falls back to a random name when the local part is a hex token
    (zhuce6_abc123) or otherwise unparseable.
    """
    local = email.split("@")[0]
    # Try "first.last" pattern (Faker free_email output)
    if "." in local:
        parts = [re.sub(r'\d+', '', p) for p in local.split(".")]
        parts = [p for p in parts if p]
        if len(parts) >= 2:
            return " ".join(p.capitalize() for p in parts[:2])

    # Strip digits and underscores, check if anything usable remains
    cleaned = re.sub(r'[\d_]+', '', local).strip()
    if cleaned and len(cleaned) >= 2 and not cleaned.lower().startswith("zhuce6"):
        return cleaned.capitalize()

    # Fallback: random name (same pool as constants.FIRST_NAMES)
    return generate_random_user_info()["name"]


def _page_has(page, *selectors, timeout: int = 1000) -> bool:
    """Check if any of the given selectors is visible on the page."""
    for sel in selectors:
        try:
            if page.locator(sel).first.is_visible(timeout=timeout):
                return True
        except Exception:
            continue
    return False


def _fill_otp_inputs(page, code: str) -> bool:
    """Fill 6-digit OTP into split inputs or a single input. Returns True if filled."""
    # Try split inputs first
    digit_inputs = []
    for selector in [
        "input[data-testid*='code']",
        "input[aria-label*='code' i]",
        "input[name*='code' i]",
        "input[inputmode='numeric']",
        "input[type='tel']",
    ]:
        try:
            visible = [el for el in page.locator(selector).all() if el.is_visible(timeout=300)]
            if len(visible) >= 6:
                digit_inputs = visible[:6]
                break
        except Exception:
            continue

    if digit_inputs:
        for i, digit in enumerate(code):
            try:
                digit_inputs[i].click(force=True)
                digit_inputs[i].fill(digit)
            except Exception:
                pass
        return True

    # Try single input
    for selector in [
        "input[autocomplete='one-time-code']",
        "input[inputmode='numeric']",
        "input[name*='code' i]",
        "input[type='text']",
    ]:
        try:
            el = page.locator(selector).first
            if el.is_visible(timeout=1000):
                el.click(force=True)
                el.fill(code)
                return True
        except Exception:
            continue

    return False


def _setup_totp_on_page(page, email: str, mailbox=None, skip_welcome_check: bool = False) -> dict | None:
    """Set up TOTP authenticator on the currently-logged-in chatgpt.com page.

    Navigates to Settings → Security, clicks the MFA toggle, intercepts the
    ``POST /backend-api/accounts/mfa/enroll`` response to capture the TOTP
    secret, fills the verification code into the page UI, and submits.

    Returns ``{"secret": ..., "issuer": "OpenAI", "label": email, "setup_at": ...}``
    on success, ``None`` if TOTP is already enabled or setup fails.

    When *skip_welcome_check* is True, the proactive welcome-modal dismissal
    at the start of the function is skipped (the caller has already handled
    it, e.g. Step 9).  The caller should dismiss the modal itself before
    retrying on failure.
    """
    try:
        import pyotp
    except ImportError:
        print("[!] pyotp not installed. Run: pip install pyotp")
        return None

    # ── State captured from API response interception ──
    _state = {"enroll_secret": None, "enroll_session_id": None, "activate_ok": False}

    def _on_totp_response(response):
        url = response.url
        if "/backend-api/accounts/mfa/enroll" in url and response.status == 200:
            try:
                body = response.json()
                s = body.get("secret", "")
                sid = body.get("session_id", "")
                if s:
                    _state["enroll_secret"] = s
                    _state["enroll_session_id"] = sid
                    print(f"  [intercept] mfa/enroll → secret={s[:8]}... session_id={sid[:16]}...")
            except Exception:
                pass
        if "/backend-api/accounts/mfa/user/activate_enrollment" in url and response.status == 200:
            try:
                body = response.json()
                if body.get("success"):
                    _state["activate_ok"] = True
                    print("  [intercept] TOTP activate_enrollment → success")
            except Exception:
                pass

    page.on("response", _on_totp_response)

    try:
        if not skip_welcome_check:
            # ── Proactive: dismiss any welcome modal before navigating ──
            # The modal might have rendered late and was missed by Step 9.
            print("  Checking for undismissed welcome modal...")
            _dismiss_welcome_modal(page)
        else:
            print("  Welcome modal already handled — skipping proactive check")

        # ── Navigate to Settings → Security ──
        print("  Navigating to Settings → Security...")
        settings_url = "https://chatgpt.com/#settings/Security"
        try:
            page.goto(settings_url, wait_until="domcontentloaded", timeout=15000)
        except Exception:
            page.goto(settings_url, wait_until="commit", timeout=15000)

        # Wait for page to settle
        try:
            page.wait_for_load_state("domcontentloaded", timeout=5000)
        except Exception:
            pass
        try:
            page.wait_for_load_state("networkidle", timeout=5000)
        except Exception:
            pass
        page.wait_for_timeout(3000)

        _save_screenshot(page, "09-settings-page", level="debug")

        # ── Pre-create MailboxAccount + snapshot for potential email verification ──
        # OpenAI may require a second email OTP after toggling MFA.
        # The verification email is sent immediately on click, so we MUST
        # snapshot current mailbox state BEFORE clicking the toggle.
        fake_account = MailboxAccount(email=email, account_id=email)
        otp_before_ids_for_totp: set[str] | None = None

        # ── Click MFA authenticator toggle ──
        print("  Looking for MFA authenticator toggle...")
        toggle_clicked = False
        for sel in [
            "[data-testid='mfa-authenticator-toggle']",
            "button[role='switch'][aria-checked='false']",
            "[role='switch'][aria-checked='false']",
        ]:
            try:
                el = page.locator(sel).first
                if el.is_visible(timeout=5000):
                    checked = el.get_attribute("aria-checked") or ""
                    print(f"  Found toggle ({sel}), aria-checked={checked}")
                    if checked == "true":
                        print("  TOTP already enabled — skipping setup")
                        return None
                    else:
                        # Snapshot mailbox BEFORE toggle click —
                        # verification email is sent immediately.
                        if mailbox is not None:
                            otp_before_ids_for_totp = mailbox.get_current_ids(fake_account)
                        el.click(force=True)
                        page.wait_for_timeout(2000)
                        toggle_clicked = True
                        print("  Toggle clicked")
                    break
            except Exception:
                continue

        if not toggle_clicked:
            print("  [!] Could not find/click MFA toggle")
            _save_screenshot(page, "09-no-toggle")
            return None

        # ── Handle email verification redirect ──
        page.wait_for_timeout(3000)
        landed = page.url.lower()
        if "email-verification" in landed:
            print("  Email verification triggered for TOTP setup")
            _save_screenshot(page, "09-email-verify-totp")
            otp = None
            if mailbox is not None:
                # Use the snapshot taken BEFORE toggle click —
                # the verification email was already sent by now.
                otp = mailbox.wait_for_code(fake_account, timeout=120, before_ids=otp_before_ids_for_totp)
            if otp:
                _fill_otp_inputs(page, otp)
                page.wait_for_timeout(500)
                _find_and_click(page, [
                    "button[data-dd-action-name='Continue']",
                    "button[type='submit']",
                    "button:has-text('Continue')",
                ], label="OTP continue")
                page.wait_for_timeout(3000)
                _save_screenshot(page, "09-after-email-verify")
            else:
                print("  [!] Email OTP timeout for TOTP setup — continuing anyway")

        # ── Wait for mfa/enroll response interception ──
        if not _state["enroll_secret"]:
            print("  Waiting for mfa/enroll response...")
            deadline = time.time() + 30
            while time.time() < deadline and not _state["enroll_secret"]:
                page.wait_for_timeout(1000)
                if _page_has(page, "input[autocomplete='one-time-code']", timeout=500):
                    print("  Code input visible — enroll likely completed (missed intercept)")
                    break

        if _state["enroll_secret"]:
            secret = _state["enroll_secret"]
            session_id = _state["enroll_session_id"]
            print(f"  Got TOTP secret: {secret[:8]}... session_id: {session_id[:16]}...")

            # ── Generate TOTP code ──
            totp_code = pyotp.TOTP(secret).now()
            print(f"  TOTP code: {totp_code}")

            # ── Fill verification code into page UI ──
            _save_screenshot(page, "09-totp-setup-modal", level="debug")

            code_filled = _fill_otp_inputs(page, totp_code)
            if not code_filled:
                for sel in [
                    "input#totp_otp",
                    "input[name='totp_otp']",
                    "input[placeholder*='6 位验证码' i]",
                    "input[placeholder*='verification code' i]",
                    "input[autocomplete='one-time-code']",
                    "input[name='code']",
                    "input[inputmode='numeric']",
                    "input[placeholder*='验证码' i]",
                ]:
                    try:
                        el = page.locator(sel).first
                        if el.is_visible(timeout=1000):
                            el.click(force=True)
                            el.fill(totp_code)
                            code_filled = True
                            print(f"  Filled TOTP code via {sel}")
                            break
                    except Exception:
                        continue
            if not code_filled:
                print("  Using keyboard fallback for TOTP code...")
                page.keyboard.type(totp_code, delay=50)

            page.wait_for_timeout(500)

            # ── Click submit → SPA calls activate_enrollment ──
            _find_and_click(page, [
                "button[type='submit']",
                "button:has-text('Verify')",
                "button:has-text('Continue')",
                "button:has-text('Next')",
                "button:has-text('确认')",
                "button:has-text('验证')",
                "button[data-dd-action-name='Continue']",
            ], label="TOTP submit")

            page.wait_for_timeout(3000)

            # ── Wait for activation confirmation ──
            deadline_confirm = time.time() + 20
            while time.time() < deadline_confirm and not _state["activate_ok"]:
                page.wait_for_timeout(1000)
                try:
                    el = page.locator("[data-testid='mfa-authenticator-toggle']").first
                    if el.is_visible(timeout=500):
                        if el.get_attribute("aria-checked") == "true":
                            _state["activate_ok"] = True
                            print("  Toggle aria-checked=true — activation confirmed")
                            break
                except Exception:
                    pass

            if _state["activate_ok"]:
                print("  TOTP activated!")
                _save_screenshot(page, "09-totp-done", level="debug")
            else:
                print("  [!] TOTP activation not confirmed via intercept. Saving secret anyway.")
                _save_screenshot(page, "09-totp-unconfirmed")

            return {
                "secret": secret,
                "issuer": "OpenAI",
                "label": email,
                "setup_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
            }
        else:
            print("  [!] Did not capture mfa/enroll secret")
            _save_screenshot(page, "09-no-enroll")
            return None

    except Exception as e:
        print(f"  [!] TOTP setup failed: {e}")
        import traceback
        traceback.print_exc()
        _save_screenshot(page, "09-totp-error")
        return None

    finally:
        try:
            page.remove_listener("response", _on_totp_response)
        except Exception:
            pass


def _adjust_privacy_settings(page, email: str, interactive: bool = False) -> dict:
    """Adjust ChatGPT privacy settings after login.

    Performs two adjustments on the currently-logged-in chatgpt.com page:

    1. **Disable "Memory"** on the Personalization page.
       Tries keyboard shortcut ``Ctrl+Shift+I`` first (which opens Settings
       and usually lands on Personalization).  Falls back to navigating
       directly to ``https://chatgpt.com/#settings/Personalization``.
       Finds the ``role="switch"`` button and clicks it only if
       ``aria-checked="true"`` (i.e. currently enabled).

    2. **Disable "Improve model for everyone"** on the Data Controls page.
       Navigates to ``https://chatgpt.com/#settings/DataControls``,
       clicks the ``data-testid="improve-model-open-modal-button"`` button
       to open the confirmation modal, then toggles the
       ``data-testid="improve-model-toggle"`` switch off if it is on.
       Dismisses the modal afterward with the Escape key.

    Returns a dict with the status of each adjustment::

        {
            "attempted_at": "2026-06-24T10:30:00+08:00",
            "memory_disabled": true,
            "improve_model_disabled": true,
            "details": {
                "memory": {"success": true, "was_enabled": true, "now_disabled": true, "method": "keyboard_shortcut"},
                "improve_model": {"success": true, "was_enabled": true, "now_disabled": true},
            }
        }
    """
    result = {
        "attempted_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
        "memory_disabled": False,
        "improve_model_disabled": False,
        "details": {
            "memory": {"success": False, "was_enabled": None, "method": None},
            "improve_model": {"success": False, "was_enabled": None},
        },
    }

    # ═════════════════════════════════════════════════════════════════════
    # 1. Disable Memory on Personalization page
    # ═════════════════════════════════════════════════════════════════════
    print("  [settings] ── Adjusting Memory setting ──")
    _save_screenshot(page, "settings-01-before-memory", level="debug")

    landed_on_personalization = False

    # ── Method A: Keyboard shortcut Ctrl+Shift+I ──
    try:
        print("  [settings] Trying Ctrl+Shift+I shortcut...")
        page.keyboard.press("Control+Shift+i")
        page.wait_for_timeout(4000)

        # Check if we landed on a settings page
        current_url = page.url.lower()
        if "settings" in current_url or "personalization" in current_url:
            print(f"  [settings] Landed on settings page via shortcut: {page.url[:120]}")
            landed_on_personalization = True
            _save_screenshot(page, "settings-02-shortcut-result", level="debug")
        else:
            print(f"  [settings] Shortcut did not open settings — URL: {page.url[:120]}")
    except Exception as e:
        print(f"  [settings] Keyboard shortcut failed: {e}")

    # ── Method B: Direct URL navigation (fallback) ──
    if not landed_on_personalization:
        print("  [settings] Navigating to Personalization page via URL...")
        personalization_url = "https://chatgpt.com/#settings/Personalization"
        try:
            page.goto(personalization_url, wait_until="domcontentloaded", timeout=15000)
        except Exception:
            page.goto(personalization_url, wait_until="commit", timeout=15000)
        page.wait_for_timeout(4000)
        try:
            page.wait_for_load_state("networkidle", timeout=5000)
        except Exception:
            pass
        _save_screenshot(page, "settings-03-personalization-url", level="debug")
        current_url = page.url.lower()
        if "settings" in current_url:
            landed_on_personalization = True
            print(f"  [settings] Landed on: {page.url[:120]}")
        else:
            print(f"  [settings] URL navigation result: {page.url[:120]}")

    # ── Find and toggle the Memory switch ──
    # The "Ask anything" / "Skip Tour" onboarding overlay can re-appear
    # asynchronously even after we land on the settings page.  Dismiss it
    # first so the switches are reachable.
    _dismiss_tour_overlay(page, timeout=2000)
    _dismiss_overlays(page, timeout=1000)

    if landed_on_personalization:
        memory_toggled = False
        # Strategy: find all role="switch" buttons, look for the Memory one.
        # The Memory switch is typically the first/only switch on this page.
        switch_selectors = [
            "button[role='switch']",
            "[role='switch']",
        ]
        for sw_sel in switch_selectors:
            if memory_toggled:
                break
            try:
                switches = page.locator(sw_sel).all()
                for i, sw in enumerate(switches):
                    try:
                        if not sw.is_visible(timeout=500):
                            continue
                        checked = sw.get_attribute("aria-checked") or ""
                        label_id = sw.get_attribute("aria-labelledby") or ""
                        print(f"  [settings] Switch found: aria-checked={checked}, labelledby={label_id}")
                        if checked == "true":
                            # Memory is currently ENABLED — click to disable
                            print("  [settings] Memory is ON — clicking to disable...")
                            sw.click(force=True)
                            page.wait_for_timeout(1500)
                            # The tour overlay can re-appear after clicking the
                            # switch (page re-render triggers it).  Dismiss it
                            # first, then re-find the switch — the old locator
                            # reference may be stale.
                            _dismiss_tour_overlay(page, timeout=2000)
                            _dismiss_overlays(page, timeout=1000)
                            # Re-find the i-th switch to check the new state
                            try:
                                fresh_switches = page.locator(sw_sel).all()
                                if i < len(fresh_switches):
                                    new_checked = (fresh_switches[i].get_attribute("aria-checked") or "")
                                else:
                                    new_checked = (page.locator(sw_sel).first.get_attribute("aria-checked") or "")
                            except Exception:
                                new_checked = ""
                            if new_checked == "false":
                                result["memory_disabled"] = True
                                result["details"]["memory"] = {
                                    "success": True,
                                    "was_enabled": True,
                                    "now_disabled": True,
                                    "method": "keyboard_shortcut" if landed_on_personalization else "url",
                                }
                                print("  [settings] Memory disabled successfully!")
                            else:
                                result["details"]["memory"] = {
                                    "success": False,
                                    "was_enabled": True,
                                    "error": f"Click did not change state (still {new_checked})",
                                }
                                print(f"  [settings] [!] Toggle click may not have worked: aria-checked={new_checked}")
                            memory_toggled = True
                            _save_screenshot(page, "settings-04-memory-done", level="debug")
                        elif checked == "false":
                            # Memory is already DISABLED — nothing to do
                            result["memory_disabled"] = True  # already in desired state
                            result["details"]["memory"] = {
                                "success": True,
                                "was_enabled": False,
                                "now_disabled": True,
                                "already_set": True,
                                "method": "keyboard_shortcut" if landed_on_personalization else "url",
                            }
                            print("  [settings] Memory is already OFF — skipping")
                            memory_toggled = True
                            break
                        else:
                            print(f"  [settings] Unexpected aria-checked value: '{checked}' — skipping this switch")
                    except Exception as e:
                        print(f"  [settings] Switch inspection error: {e}")
                        continue
            except Exception as e:
                print(f"  [settings] Switch selector error ({sw_sel}): {e}")
                continue

        if not memory_toggled:
            print("  [settings] [!] Could not find Memory switch on Personalization page")
            _save_screenshot(page, "settings-05-memory-not-found")
            result["details"]["memory"] = {
                "success": False,
                "was_enabled": None,
                "error": "Could not locate the Memory toggle switch",
            }
    else:
        print("  [settings] [!] Failed to reach Personalization page")
        _save_screenshot(page, "settings-06-personalization-failed")
        result["details"]["memory"] = {
            "success": False,
            "was_enabled": None,
            "error": "Could not navigate to Personalization page",
        }

    # ═════════════════════════════════════════════════════════════════════
    # 2. Disable "Improve model for everyone" on Data Controls page
    # ═════════════════════════════════════════════════════════════════════
    print("  [settings] ── Adjusting 'Improve model' setting ──")
    _save_screenshot(page, "settings-10-before-datacontrols", level="debug")

    datacontrols_url = "https://chatgpt.com/#settings/DataControls"
    try:
        page.goto(datacontrols_url, wait_until="domcontentloaded", timeout=15000)
    except Exception:
        page.goto(datacontrols_url, wait_until="commit", timeout=15000)
    page.wait_for_timeout(4000)
    try:
        page.wait_for_load_state("networkidle", timeout=5000)
    except Exception:
        pass
    # The "Ask anything" tour overlay can re-appear after navigation
    _dismiss_tour_overlay(page, timeout=2000)
    _dismiss_overlays(page, timeout=1000)
    _save_screenshot(page, "settings-11-datacontrols", level="debug")

    # ── Click the "Improve model" button to open the modal ──
    modal_opened = False
    open_btn_selectors = [
        "[data-testid='improve-model-open-modal-button']",
        "button:has-text('Improve model for everyone')",
        "button:has-text('为所有用户改进模型')",
        "button:has-text('improve model')",
    ]
    for obs in open_btn_selectors:
        try:
            btn = page.locator(obs).first
            if btn.is_visible(timeout=3000):
                text = (btn.text_content() or "").strip()[:60]
                print(f"  [settings] Clicking open-modal button: '{text}' ({obs})")
                btn.click(force=True)
                page.wait_for_timeout(2000)
                modal_opened = True
                _save_screenshot(page, "settings-12-modal-opened", level="debug")
                break
        except Exception:
            continue

    if not modal_opened:
        print("  [settings] [!] Could not find/open the 'Improve model' modal button")
        _save_screenshot(page, "settings-13-modal-not-opened")
        result["details"]["improve_model"] = {
            "success": False,
            "was_enabled": None,
            "error": "Could not open the Improve model modal",
        }
    else:
        # ── Find the toggle switch inside the modal ──
        improve_toggled = False
        toggle_selectors = [
            "[data-testid='improve-model-toggle']",
            "div[role='dialog'] button[role='switch']",
            "[role='dialog'] button[role='switch']",
            "button[role='switch']",
        ]
        for tgs in toggle_selectors:
            if improve_toggled:
                break
            try:
                toggles = page.locator(tgs).all()
                for tg in toggles:
                    try:
                        if not tg.is_visible(timeout=500):
                            continue
                        checked = tg.get_attribute("aria-checked") or ""
                        data_testid = tg.get_attribute("data-testid") or ""
                        print(f"  [settings] Modal switch: aria-checked={checked}, data-testid={data_testid}")
                        if checked == "true":
                            print("  [settings] 'Improve model' is ON — clicking to disable...")
                            tg.click(force=True)
                            page.wait_for_timeout(1500)
                            new_checked = tg.get_attribute("aria-checked") or ""
                            if new_checked == "false":
                                result["improve_model_disabled"] = True
                                result["details"]["improve_model"] = {
                                    "success": True,
                                    "was_enabled": True,
                                    "now_disabled": True,
                                }
                                print("  [settings] 'Improve model' disabled successfully!")
                            else:
                                result["details"]["improve_model"] = {
                                    "success": False,
                                    "was_enabled": True,
                                    "error": f"Click did not change state (still {new_checked})",
                                }
                            improve_toggled = True
                            _save_screenshot(page, "settings-14-improve-toggled", level="debug")
                        elif checked == "false":
                            result["improve_model_disabled"] = True
                            result["details"]["improve_model"] = {
                                "success": True,
                                "was_enabled": False,
                                "now_disabled": True,
                                "already_set": True,
                            }
                            print("  [settings] 'Improve model' is already OFF — skipping")
                            improve_toggled = True
                            break
                    except Exception as e:
                        print(f"  [settings] Modal switch inspection error: {e}")
                        continue
            except Exception as e:
                print(f"  [settings] Toggle selector error ({tgs}): {e}")
                continue

        if not improve_toggled:
            print("  [settings] [!] Could not find the Improve model toggle in modal")
            _save_screenshot(page, "settings-15-toggle-not-found")
            result["details"]["improve_model"] = {
                "success": False,
                "was_enabled": None,
                "error": "Could not locate the Improve model toggle switch in modal",
            }

        # ── Dismiss the modal ──
        print("  [settings] Dismissing modal (Escape key)...")
        try:
            page.keyboard.press("Escape")
            page.wait_for_timeout(1500)
            _save_screenshot(page, "settings-16-modal-dismissed", level="debug")
        except Exception:
            pass

    # ── Summary ──
    print(f"  [settings] Adjustment complete: memory_disabled={result['memory_disabled']}, "
          f"improve_model_disabled={result['improve_model_disabled']}")
    return result


def _handle_totp_challenge_during_login(page, totp_secret: str) -> bool:
    """Fill a TOTP challenge during re-login using the stored secret.

    When re-logging into an account that already has TOTP enabled, OpenAI
    may present a second-factor challenge after email+OTP.  This function
    detects the TOTP input field, generates a 6‑digit code from *totp_secret*
    using ``pyotp``, fills it, and submits.

    Returns True if a TOTP input was found and filled, False otherwise.
    """
    try:
        import pyotp
    except ImportError:
        print("  [totp-challenge] pyotp not installed — cannot generate TOTP code")
        return False

    # Check if TOTP input is visible on the page
    totp_input_selectors = [
        "input[autocomplete='one-time-code']",
        "input[inputmode='numeric']",
        "input[name*='code' i]",
        "input[placeholder*='6-digit' i]",
        "input[placeholder*='authenticator' i]",
        "input[type='tel']",
    ]
    totp_input_found = None
    for sel in totp_input_selectors:
        try:
            el = page.locator(sel).first
            if el.is_visible(timeout=1000):
                totp_input_found = el
                print(f"  [totp-challenge] TOTP input detected: {sel}")
                break
        except Exception:
            continue

    if totp_input_found is None:
        # Also check body text for TOTP-related keywords
        try:
            body_text = (page.locator("body").inner_text(timeout=1000) or "").lower()
            if any(kw in body_text for kw in
                   ["authenticator", "two-factor", "2fa", "totp",
                    "verification code", "6-digit"]):
                print("  [totp-challenge] TOTP keywords found in page text but no input detected")
        except Exception:
            pass
        return False

    # Generate TOTP code
    try:
        totp_code = pyotp.TOTP(totp_secret).now()
        print(f"  [totp-challenge] Generated TOTP code: {totp_code}")
    except Exception as e:
        print(f"  [totp-challenge] Failed to generate TOTP code: {e}")
        return False

    # Fill the code
    try:
        totp_input_found.click(force=True)
        totp_input_found.fill(totp_code)
        page.wait_for_timeout(500)
        print("  [totp-challenge] TOTP code filled")
    except Exception as e:
        print(f"  [totp-challenge] Failed to fill TOTP code: {e}")
        # Try keyboard fallback
        try:
            page.keyboard.type(totp_code, delay=50)
            page.wait_for_timeout(500)
            print("  [totp-challenge] TOTP code typed via keyboard fallback")
        except Exception:
            return False

    # Submit
    submit_selectors = [
        "button[type='submit']",
        "button:has-text('Verify')",
        "button:has-text('Continue')",
        "button:has-text('Submit')",
        "button:has-text('确认')",
        "button:has-text('验证')",
    ]
    for ss in submit_selectors:
        try:
            btn = page.locator(ss).first
            if btn.is_visible(timeout=1000):
                btn.click(force=True)
                page.wait_for_timeout(2000)
                print(f"  [totp-challenge] Submitted via {ss}")
                return True
        except Exception:
            continue

    # Try Enter key as last resort
    try:
        page.keyboard.press("Enter")
        page.wait_for_timeout(2000)
        print("  [totp-challenge] Submitted via Enter key")
        return True
    except Exception:
        pass

    return False


def _load_warming_prompts() -> list[dict]:
    """Load warming prompts from warming-prompts.json.

    Returns a list of ``{"topic": ..., "prompt": ...}`` dicts.
    Returns an empty list if the file is missing or unparseable.
    """
    prompts_path = _THIS_DIR / "warming-prompts.json"
    if not prompts_path.exists():
        print(f"  [!] warming-prompts.json not found at {prompts_path}")
        return []
    try:
        with open(prompts_path, "r", encoding="utf-8") as f:
            data = json.load(f)
        if not isinstance(data, list):
            print(f"  [!] warming-prompts.json is not a list")
            return []
        return data
    except Exception as e:
        print(f"  [!] Failed to load warming-prompts.json: {e}")
        return []


def _send_chat_message(page, prompt_text: str, timeout: int = 120,
                       bridge=None, wait_for_reply: bool = True) -> bool:
    """Send a message on chatgpt.com and optionally wait for the reply.

    Assumes the page is already on chatgpt.com with a logged-in session.
    Types *prompt_text* into the chat composer, presses Enter to send,
    then optionally polls until the Stop button disappears.

    When *wait_for_reply* is False, returns as soon as the message is
    sent and generation has started — useful when you want to do other
    work (e.g. settings adjustment) while the AI reply generates, then
    call ``_wait_for_chat_reply()`` later to check completion.

    Returns True if the message was sent (and reply completed, when
    *wait_for_reply* is True), False otherwise.
    """
    print(f"  [chat] Sending message: {prompt_text[:80]}...")

    # ── 1. Navigate to chatgpt.com (TOTP setup may have left us on
    #    /#settings/Security — go back to the main chat page).
    print("  [chat] Navigating to chatgpt.com...")
    try:
        page.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=15000)
    except Exception:
        page.goto("https://chatgpt.com/", wait_until="commit", timeout=15000)
    page.wait_for_timeout(1000)

    # ── 2. (welcome modal already handled in Step 9 — skip) ──
    # The "You're all set" modal only appears once per session, right after
    # the about-you page.  By the time we reach this function (post-TOTP
    # Step 16), it has already been dismissed.  Skipping the expensive
    # _dismiss_welcome_modal() poll saves ~6 s on every chat turn.

    # ── 3. Locate & focus the prompt textarea (with retry) ──
    # The real input is div#prompt-textarea (contenteditable ProseMirror).
    # There is also a hidden <textarea name="prompt-textarea"> fallback — ignore it.
    # New accounts may take a few seconds to render the chat UI after first
    # login (onboarding animations, sidebar loading, etc.).
    textarea_selectors = [
        "#prompt-textarea",
        "div[contenteditable=\"true\"][role=\"textbox\"]",
        "div.ProseMirror",
    ]
    textarea = None
    _ta_deadline = time.time() + 5
    while time.time() < _ta_deadline:
        for sel in textarea_selectors:
            try:
                el = page.locator(sel).first
                if el.is_visible(timeout=1000):
                    textarea = el
                    print(f"  [chat] Found textarea: {sel}")
                    break
            except Exception:
                continue
        if textarea:
            break
        # Dismiss any overlays / tours that might be blocking the textarea
        _dismiss_overlays(page, timeout=1000)
        _dismiss_tour_overlay(page, timeout=1000)
        page.wait_for_timeout(500)

    if not textarea:
        print("  [!] Could not find chat textarea after 5s — skipping chat step")
        _save_screenshot(page, "16-no-textarea")
        return False

    # ── 4. Input the prompt via copy-paste (fast) ──
    # ChatGPT uses a contenteditable ProseMirror div, not a regular
    # <input>/<textarea>.  Char-by-char typing is slow (300+ chars can
    # take 20-30s).  Write the text to the system clipboard and paste
    # it — ProseMirror intercepts the paste event and inserts correctly.
    try:
        textarea.click(force=True)
        page.wait_for_timeout(300)
        # Clear any existing placeholder text (ProseMirror may have a <p> child)
        page.keyboard.press("Control+a")
        page.keyboard.press("Backspace")
        page.wait_for_timeout(100)
        # Write prompt to clipboard via evaluate, then paste
        page.evaluate("text => navigator.clipboard.writeText(text)", prompt_text)
        page.wait_for_timeout(200)
        page.keyboard.press("Control+v")
        page.wait_for_timeout(500)
        print("  [chat] Prompt pasted (clipboard)")
    except Exception:
        # Fallback: keyboard.insertText dispatches only input events and is
        # faster than per-char type().  If that also fails, drop back to the
        # original char-by-char typing as a last resort.
        try:
            page.keyboard.insertText(prompt_text)
            page.wait_for_timeout(500)
            print("  [chat] Prompt inserted (insertText fallback)")
        except Exception as e:
            print(f"  [chat] Clipboard/insertText failed ({e}) — falling back to char-by-char typing")
            try:
                page.keyboard.type(prompt_text, delay=random.randint(40, 100))
                page.wait_for_timeout(500)
                print("  [chat] Prompt typed (keyboard fallback)")
            except Exception as e2:
                print(f"  [!] Failed to input prompt: {e2}")
                return False

    # ── 5. Send the message ──
    # Primary: press Enter (natural human behaviour).
    # Fallback: click the send button (only visible when input has content).
    sent = False
    try:
        page.keyboard.press("Enter")
        page.wait_for_timeout(1000)
        sent = True
        print("  [chat] Sent via Enter")
    except Exception:
        pass

    if not sent:
        send_selectors = [
            "button[data-testid=\"send-button\"]",
            "button[aria-label=\"Send prompt\"]",
        ]
        for sel in send_selectors:
            try:
                btn = page.locator(sel).first
                if btn.is_visible(timeout=2000):
                    btn.click(force=True)
                    page.wait_for_timeout(500)
                    sent = True
                    print(f"  [chat] Sent via click: {sel}")
                    break
            except Exception:
                continue

    if not sent:
        print("  [!] Could not send message — no send method worked")
        _save_screenshot(page, "16-no-send")
        return False

    # ── 6. Wait for the Stop button to appear (confirms generation started) ──
    # Short replies may complete before we even see the Stop button, so this
    # is best-effort.
    try:
        page.locator("button[data-testid=\"stop-button\"]").wait_for(
            state="visible", timeout=10000
        )
        print("  [chat] Generation started (Stop button visible)")
    except Exception:
        print("  [chat] Stop button did not appear — reply may already be done")

    if not wait_for_reply:
        print("  [chat] Message sent — deferring reply check")
        return True

    # ── 7. Wait for the Stop button to disappear (generation complete) ──
    try:
        page.locator("button[data-testid=\"stop-button\"]").wait_for(
            state="hidden", timeout=timeout * 1000
        )
        page.wait_for_timeout(2000)  # settle
        print("  [chat] Reply complete (Stop button hidden)")
        return True
    except Exception:
        print(f"  [!] Reply wait timed out ({timeout}s) — continuing anyway")
        _save_screenshot(page, "16-chat-timeout")
        return False


def _wait_for_chat_reply(page, timeout: int = 60) -> bool:
    """Wait for an in-progress chat reply to finish on chatgpt.com.

    Call this after ``_send_chat_message(..., wait_for_reply=False)`` once
    other work (e.g. settings adjustment) is done.  Navigates back to
    chatgpt.com and polls until the Stop button disappears.
    """
    print("  [chat] Navigating back to check reply...")
    try:
        page.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=15000)
    except Exception:
        page.goto("https://chatgpt.com/", wait_until="commit", timeout=15000)
    page.wait_for_timeout(2000)

    # Dismiss any tour overlay that may have appeared
    _dismiss_tour_overlay(page, timeout=2000)

    try:
        page.locator("button[data-testid=\"stop-button\"]").wait_for(
            state="hidden", timeout=timeout * 1000
        )
        page.wait_for_timeout(2000)  # settle
        print("  [chat] Reply complete (Stop button hidden)")
        return True
    except Exception:
        print(f"  [chat] Reply wait timed out ({timeout}s) — continuing anyway")
        _save_screenshot(page, "16-chat-timeout")
        return False


# ═══════════════════════════════════════════════════════════════════════════
# Config
# ═══════════════════════════════════════════════════════════════════════════

CDP_PORT = 9224  # Different from phone script (9223) to avoid conflicts

_OUTPUT_DIR = _THIS_DIR / "output"
_OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
_FAILED_OUTPUT_DIR = _OUTPUT_DIR / "failed"
_FAILED_OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

SCREENSHOT_DIR = Path(os.environ.get("EMAIL_RELOGIN_SCREENSHOT_DIR", _THIS_DIR / "screenshots"))
SCREENSHOT_DIR.mkdir(parents=True, exist_ok=True)

# The default shared directory is short-lived. A wrapper-provided per-run
# directory is retained for diagnosis and cleaned up by the wrapper policy.
if SCREENSHOT_DIR == _THIS_DIR / "screenshots":
    _today = datetime.now().date()
    for _f in SCREENSHOT_DIR.iterdir():
        if _f.is_file():
            _mtime = datetime.fromtimestamp(_f.stat().st_mtime).date()
            if _mtime < _today:
                _f.unlink()

# ── Interactive alarm sound ─────────────────────────────────────────
# Set this to a .wav file path to use a custom sound instead of the
# built-in beep when --interactive pauses the script.
# Example:
#   ALARM_SOUND = r"C:\My_project\PRM-mail\email-reg\alert.wav"
ALARM_SOUND: str | None = None  # None = built-in 880 Hz beep


def _load_config() -> dict:
    config_path = _THIS_DIR / "config.json"
    if config_path.exists():
        with open(config_path, "r", encoding="utf-8") as f:
            return json.load(f)
    return {}


def _load_proxy_from_config() -> str | None:
    cfg = _load_config()
    proxy = str(cfg.get("proxy", {}).get("default", "") or "").strip()
    return proxy or None


# ═══════════════════════════════════════════════════════════════════════════
# Recon helpers
# ═══════════════════════════════════════════════════════════════════════════

_RECON_INDENT = 0


def _prompt_user(reason: str = "unexpected page", *,
                 interactive: bool = True):
    """Pause execution — browser stays open for manual inspection.

    User can inspect the page, take screenshots, check DOM, etc.
    Press Enter to continue, type 'q' + Enter to raise exit.

    When *interactive* is False, the call logs the reason and returns
    immediately without waiting for user input (batch mode).

    When *interactive* is True, plays the interactive alarm sound
    (see ALARM_SOUND constant at the top of this file).
    """
    print(f"\n  [!] {reason}")
    if not interactive:
        print(f"  [!] Non-interactive mode — auto-continuing...")
        return

    # Play an audible alarm to get the operator's attention (fire-and-forget).
    if _play_alarm:
        _play_alarm(ALARM_SOUND, block=False)

    print(f"  [!] Browser is still open at: http://127.0.0.1:{CDP_PORT}")
    print(f"  [>] Press Enter to continue, or type 'q' to quit...")
    try:
        choice = input().strip().lower()
        if choice == 'q':
            raise SystemExit(0)
    except (EOFError, KeyboardInterrupt):
        raise SystemExit(0)


def _recon_print(title: str, detail: str = "", *, indent: int | None = None):
    """Heavy recon logging — prints timestamped diagnostic info."""
    ts = datetime.now().strftime("%H:%M:%S")
    if indent is None:
        indent = _RECON_INDENT
    prefix = "    " * indent
    header = f"[RECON {ts}] {prefix}{title}"
    print(header)
    if detail:
        for line in detail.splitlines():
            print(f"{prefix}  {line}")


def _recon_page_snapshot(page, label: str = "PAGE SNAPSHOT", *, indent: int = 0):
    """Print comprehensive recon info about the current page."""
    _recon_print("", indent=indent)
    _recon_print(f"═══ {label} ═══", indent=indent)
    try:
        url = page.url
        title = page.title()
        _recon_print(f"URL:   {url[:200]}", indent=indent)
        _recon_print(f"TITLE: {title[:200]}", indent=indent)
    except Exception as e:
        _recon_print(f"URL/TITLE error: {e}", indent=indent)

    # Print all visible buttons
    try:
        buttons = page.locator("button").all()
        visible_buttons = []
        for btn in buttons:
            try:
                if btn.is_visible(timeout=500):
                    text = (btn.text_content() or "").strip()[:120]
                    if text:
                        visible_buttons.append(text)
            except Exception:
                continue
        if visible_buttons:
            _recon_print(f"Visible buttons ({len(visible_buttons)}):", indent=indent)
            for bt in visible_buttons[:20]:
                _recon_print(f"  [{bt}]", indent=indent)
            if len(visible_buttons) > 20:
                _recon_print(f"  ... and {len(visible_buttons) - 20} more", indent=indent)
        else:
            _recon_print("Visible buttons: NONE", indent=indent)
    except Exception as e:
        _recon_print(f"Button scan error: {e}", indent=indent)

    # Print all visible input elements
    try:
        inputs = page.locator("input").all()
        visible_inputs = []
        for inp in inputs:
            try:
                if inp.is_visible(timeout=500):
                    tp = inp.get_attribute("type") or "text"
                    name = inp.get_attribute("name") or ""
                    placeholder = inp.get_attribute("placeholder") or ""
                    visible_inputs.append(f"[type={tp}] name='{name}' placeholder='{placeholder[:60]}'")
            except Exception:
                continue
        if visible_inputs:
            _recon_print(f"Visible inputs ({len(visible_inputs)}):", indent=indent)
            for vi in visible_inputs[:15]:
                _recon_print(f"  {vi}", indent=indent)
            if len(visible_inputs) > 15:
                _recon_print(f"  ... and {len(visible_inputs) - 15} more", indent=indent)
    except Exception as e:
        _recon_print(f"Input scan error: {e}", indent=indent)

    # Print heading / key text on page
    try:
        headings = page.locator("h1, h2, [role='heading']").all()
        visible_headings = []
        for h in headings:
            try:
                if h.is_visible(timeout=300):
                    text = (h.text_content() or "").strip()[:100]
                    if text:
                        visible_headings.append(text)
            except Exception:
                continue
        if visible_headings:
            _recon_print(f"Visible headings:", indent=indent)
            for vh in visible_headings[:8]:
                _recon_print(f"  [{vh}]", indent=indent)
    except Exception:
        pass

    # Print body text snippet (first 300 chars of visible text)
    try:
        body_text = page.locator("body").inner_text(timeout=2000)
        snippet = body_text.strip()[:500].replace("\n", " | ")
        _recon_print(f"Body snippet: {snippet}", indent=indent)
    except Exception:
        pass

    # Print cookies (key ones only)
    try:
        cookies = page.context.cookies()
        key_cookies = [
            c for c in cookies
            if any(kw in (c.get("name") or "").lower()
                   for kw in ["session", "oai", "auth", "csrf", "did", "token"])
        ]
        if key_cookies:
            _recon_print(f"Key cookies ({len(key_cookies)}):", indent=indent)
            for c in key_cookies:
                _recon_print(
                    f"  {c['name']} (domain={c.get('domain', '')})",
                    indent=indent,
                )
    except Exception:
        pass

    _recon_print(f"═══ END {label} ═══", indent=indent)
    _recon_print("", indent=indent)


# ── Debug-mode flag ────────────────────────────────────────────────────
# When False (default), only error/failure screenshots are saved.
# When True (--debug), milestone/normal-flow screenshots are also saved.
_DEBUG_MODE: bool = False


def _save_screenshot(page, name: str, level: str = "always"):
    """Save a debug screenshot.

    Parameters
    ----------
    level:
        ``"always"`` (default) — always save, even in non-debug mode.
        ``"debug"`` — only save when ``--debug`` is active (milestone /
        normal-flow checkpoints that are useful while developing but not
        needed in production batch runs).
    """
    if level == "debug" and not _DEBUG_MODE:
        return
    ts = datetime.now().strftime("%H%M%S")
    path = SCREENSHOT_DIR / f"{ts}_{name}.png"
    try:
        page.screenshot(path=str(path))
        _recon_print(f"Screenshot saved: {path}")
    except Exception as e:
        _recon_print(f"Screenshot error: {e}")


# ═══════════════════════════════════════════════════════════════════════════
# Chrome launcher (adapted from chatgpt_login.py)
# ═══════════════════════════════════════════════════════════════════════════

def _find_chrome() -> str | None:
    candidates = [
        "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
        "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
        os.path.expandvars("%LOCALAPPDATA%\\Google\\Chrome\\Application\\chrome.exe"),
        "/usr/bin/google-chrome",
        "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
        "/usr/bin/google-chrome-stable",
    ]
    for path in candidates:
        if path and os.path.exists(path):
            return path
    return None


def _kill_cdp_port(port: int):
    import subprocess as sp
    try:
        result = sp.run(
            ["netstat", "-ano", "-p", "TCP"],
            capture_output=True, text=True, timeout=5,
        )
        for line in result.stdout.splitlines():
            if f"127.0.0.1:{port}" in line and "LISTENING" in line:
                parts = line.split()
                pid = parts[-1]
                print(f"[*] Killing stale Chrome (PID {pid}) on port {port}...")
                sp.run(["taskkill", "/F", "/PID", pid],
                       capture_output=True, timeout=5)
                time.sleep(1)
                break
    except Exception:
        pass


def _launch_chrome(port: int, proxy: str | None = None) -> bool:
    chrome_path = _find_chrome()
    if not chrome_path:
        print("[!] Google Chrome not found.")
        return False

    user_data_dir = os.path.join(
        os.environ.get("TEMP", os.environ.get("TMP", "/tmp")),
        f"chrome-email-login-{secrets.token_hex(4)}"
    )

    # ── Pre-write Preferences file to block WebRTC non-proxied UDP ────
    # Chrome reads this on first launch of a fresh profile. Must be written
    # BEFORE the browser process starts.
    try:
        prefs_dir = os.path.join(user_data_dir, "Default")
        os.makedirs(prefs_dir, exist_ok=True)
        prefs_path = os.path.join(prefs_dir, "Preferences")
        with open(prefs_path, "w", encoding="utf-8") as _f:
            json.dump({"webrtc": {"ip_handling_policy": "disable_non_proxied_udp"}}, _f)
    except Exception:
        pass  # non-fatal — command-line flags are the fallback

    args = [
        chrome_path,
        f"--remote-debugging-port={port}",
        "--remote-debugging-address=127.0.0.1",
        "--disable-blink-features=AutomationControlled",
        "--webrtc-ip-handling-policy=disable_non_proxied_udp",
        "--force-webrtc-ip-handling-policy",
        "--no-first-run",
        "--no-default-browser-check",
        "--no-service-autorun",
        # ── Suppress Chromium background network traffic ────────────────
        # These requests are browser-engine-level (not page-level) and
        # waste proxy bandwidth on every fresh-profile launch.
        #   • optimizationguide-pa.googleapis.com  — ML model downloads,
        #     page-load hints, language-detection models
        #   • clients*.google.com  — component updates (CRLsets, subresource
        #     filters, certificate pinning lists)
        #   • safebrowsing.googleapis.com  — threat-list downloads
        #   • clients*.google.com/domain-reliability  — domain-reliability
        #     monitoring uploads
        #   • clients*.google.com/uma-*  — metrics/telemetry reporting
        #
        # --disable-features blocks the Optimization Guide feature group
        # at the source.  --disable-component-update prevents CRLset /
        # subresource-filter downloads.  The remaining flags suppress
        # telemetry, crash reports, domain-reliability uploads, and
        # safe-browsing threat-list fetches.
        "--disable-features=OptimizationGuideModelDownloading,OptimizationHintsFetching,OptimizationTargetPrediction,MediaRouter",
        "--disable-component-update",
        "--disable-background-networking",
        "--disable-sync",
        "--disable-domain-reliability",
        "--disable-client-side-phishing-detection",
        "--disable-breakpad",
        "--metrics-recording-only",
        f"--user-data-dir={user_data_dir}",
    ]

    # Convert socks5h to socks5 (Playwright/Chrome requirement)
    if proxy:
        proxy_for_chrome = proxy.strip()
        if proxy_for_chrome.startswith("socks5h://"):
            proxy_for_chrome = "socks5://" + proxy_for_chrome[len("socks5h://"):]
        args.append(f"--proxy-server={proxy_for_chrome}")

    # Load the Turnstile auto-click extension so the checkbox is triggered on
    # every page at document_start (replaces the old OS-mouse solver).
    _tc_ext = _THIS_DIR / "turnstile_controller" / "extension"
    if _tc_ext.is_dir():
        args.append(f"--load-extension={_tc_ext}")
        args.append(f"--disable-extensions-except={_tc_ext}")

    args.append("about:blank")  # URL must be LAST — flags after URL are ignored

    print(f"[*] Launching Chrome: CDP port={port}, proxy={'yes' if proxy else 'none'}")
    try:
        import subprocess
        # On Windows, instruct the OS to show the Chrome window normally
        # (not hidden / minimized). Without this, subsequent batch launches
        # can open behind the terminal due to Windows foreground-lock rules.
        startupinfo = None
        if sys.platform == "win32":
            startupinfo = subprocess.STARTUPINFO()
            startupinfo.dwFlags |= 0x1  # STARTF_USESHOWWINDOW
            startupinfo.wShowWindow = 1  # SW_SHOWNORMAL
        subprocess.Popen(
            args,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            startupinfo=startupinfo,
        )
        return True
    except Exception as e:
        print(f"[!] Failed to launch Chrome: {e}")
        return False


def _wait_for_cdp(port: int, timeout: int = 40) -> bool:
    import urllib.request
    url = f"http://127.0.0.1:{port}/json/version"
    deadline = time.time() + timeout
    print(f"[*] Waiting for CDP on {url} ...", end="", flush=True)
    while time.time() < deadline:
        try:
            urllib.request.urlopen(url, timeout=2)
            print(" ready!")
            return True
        except Exception:
            print(".", end="", flush=True)
            time.sleep(1.5)
    print(" TIMEOUT")
    return False


# ═══════════════════════════════════════════════════════════════════════════
# UI helpers (adapted from chatgpt_login.py)
# ═══════════════════════════════════════════════════════════════════════════

_OVERLAY_CLOSE_SELECTORS = [
    "button[data-testid='close-button']",
    "button[aria-label='Close']",
    "[data-testid='close-button']",
]


def _dismiss_overlays(page, timeout: int = 2000):
    for close_sel in _OVERLAY_CLOSE_SELECTORS:
        try:
            close_btn = page.locator(close_sel).first
            if close_btn.is_visible(timeout=timeout):
                text = (close_btn.text_content() or "").strip()
                print(f"    [!] Overlay detected! Closing via '{text}' ({close_sel})")
                close_btn.click(force=True)
                page.wait_for_timeout(800)
                return True
        except Exception:
            continue
    return False


def _dismiss_welcome_modal(page, bridge=None, timeout: int = 3000) -> bool:
    """Dismiss the 'You're all set' welcome/onboarding modal on chatgpt.com.

    **Two-phase approach** to avoid wasting time when no dialog exists:

    1. **Phase 1 — detect dialog container** (shared wait, max ~6 s).
       Poll a small set of container selectors until one is visible.
       If NO dialog appears within 6 s the function returns False
       immediately — no per-button timeout waste.

    2. **Phase 2 — find & click a dismiss button** (short 500 ms per
       selector).  Since the dialog is already in the DOM, each button
       selector resolves (or fails) near-instantly; the 500 ms cap is
       only a safety net.

    Returns True if a dialog button was clicked (modal likely dismissed),
    False if no matching button was found (no modal present).
    """

    # ═══════════════════════════════════════════════════════════════════
    # Phase 1 — wait for ANY dialog container (one shared budget)
    # ═══════════════════════════════════════════════════════════════════
    _DIALOG_CONTAINERS = [
        "[aria-label=\"You're all set\"]",
        "[aria-label*='all set' i]",
        "div[role='dialog']",
        "[role='dialog']",
        "div[role='alertdialog']",
    ]

    dialog_found = False
    _dl_deadline = time.time() + 6  # total budget: 6 s
    while time.time() < _dl_deadline:
        for ds in _DIALOG_CONTAINERS:
            try:
                if page.locator(ds).first.is_visible(timeout=500):
                    dialog_found = True
                    break
            except Exception:
                continue
        if dialog_found:
            break
        time.sleep(0.5)

    if not dialog_found:
        print("  [welcome] No dialog container detected (6 s) — skipping")
        return False

    print("  [welcome] Dialog container found — searching for dismiss button...")

    # ═══════════════════════════════════════════════════════════════════
    # Phase 2 — iterate button selectors (short timeout — dialog is here)
    # ═══════════════════════════════════════════════════════════════════
    _short = 500  # ms — dialog is in DOM, button is either visible or not

    continue_selectors = [
        # ── Match by aria-label (ChatGPT "You're all set" modal) ──
        "div[aria-label=\"You're all set\"] button:has-text('Continue')",
        "[aria-label=\"You're all set\"] button:has-text('Continue')",
        "[aria-label*='all set' i] button:has-text('Continue')",
        "[aria-label=\"You're all set\"] button",
        "[aria-label*='all set' i] button",
        # ── Standard role='dialog' selectors ──
        "div[role='dialog'] button:has-text('Continue')",
        "[role='dialog'] button:has-text('Continue')",
        "div[role='alertdialog'] button:has-text('Continue')",
        "div[role='dialog'] button:has-text('Next')",
        "[role='dialog'] button:has-text('Next')",
        "div[role='dialog'] button:has-text('Get started')",
        "div[role='dialog'] button:has-text(\"Let's go\")",
        "div[role='dialog'] button:has-text('Finish')",
        # ── Page-wide fallback ──
        "button:has-text('Continue')",
        # ── Wildcard: any button inside a visible dialog ──
        "div[role='dialog'] button",
        "[role='dialog'] button",
    ]

    _tried: list[str] = []

    for sel in continue_selectors:
        try:
            btn = page.locator(sel).first

            # ── Bare dialog-button wildcards: pre-check text ──
            if sel in ("div[role='dialog'] button", "[role='dialog'] button"):
                try:
                    text = (btn.text_content(timeout=_short) or "").strip().lower()
                    if not text or any(skip in text for skip in
                                       ("cancel", "back", "close", "skip",
                                        "maybe later", "dismiss")):
                        _tried.append(f"{sel} (skipped='{text[:30]}')")
                        continue
                except Exception:
                    _tried.append(f"{sel} (no match / timeout)")
                    continue

            # ── Read button text ──
            try:
                text = (btn.text_content(timeout=_short) or "").strip()[:60]
            except Exception:
                _tried.append(f"{sel} (no match / timeout)")
                continue

            if not text:
                _tried.append(f"{sel} (empty text)")
                continue

            print(f"  [welcome] Trying '{text}' ({sel})")
            _tried.append(f"{sel} → '{text}'")

            # ── Click: bridge first, fall through to native ──
            clicked = False
            if bridge and bridge.is_ready:
                try:
                    if bridge.simulate_click(sel):
                        clicked = True
                except Exception as e:
                    print(f"  [welcome] Bridge click exception: {e}")

            if not clicked:
                try:
                    btn.click(force=True, timeout=_short)
                    clicked = True
                except Exception as e:
                    print(f"  [welcome] Native click exception: {e}")
                    continue

            if clicked:
                page.wait_for_timeout(2000)
                print(f"  [welcome] Clicked '{text}' — modal should be dismissed")
                return True

        except Exception:
            _tried.append(f"{sel} (outer exception)")
            continue

    # ── JS fallback ──
    print(f"  [welcome] All {len(continue_selectors)} selectors exhausted")
    if _tried:
        print(f"  [welcome] Last selectors tried: {' | '.join(_tried[-6:])}")
    print(f"  [welcome] Trying JS fallback (evaluate → querySelector → click)...")
    try:
        result = page.evaluate("""() => {
            const buttons = [...document.querySelectorAll(
                'button, [role="button"], a[href="#"]'
            )];
            // ── Pass 0: "Skip Tour" — explicit onboarding tour dismissal ──
            for (const el of buttons) {
                if (!el.offsetParent) continue;
                const t = (el.textContent || '').trim();
                if (/skip\\s*tour/i.test(t)) {
                    el.click();
                    return {clicked: true, text: t.slice(0, 80)};
                }
            }
            // ── Pass 1: primary CTA keywords ──
            const primary = ['continue', 'next', 'get started', "let's go",
                             'finish', 'start chatting', 'start', 'ok'];
            for (const el of buttons) {
                if (!el.offsetParent) continue;
                const t = (el.textContent || '').trim();
                const tl = t.toLowerCase();
                if (primary.some(w => tl.includes(w)) &&
                    !/(cancel|back|close|skip|maybe later|dismiss)/i.test(tl)) {
                    el.click();
                    return {clicked: true, text: t.slice(0, 80)};
                }
            }
            // ── Pass 2: any short button that isn't a negative action ──
            for (const el of buttons) {
                if (!el.offsetParent) continue;
                const t = (el.textContent || '').trim();
                if (t.length >= 2 && t.length <= 25 &&
                    !/(cancel|back|close|skip|maybe later|dismiss)/i.test(t)) {
                    el.click();
                    return {clicked: true, text: t.slice(0, 80)};
                }
            }
            return {clicked: false};
        }""")
        if result.get("clicked"):
            print(f"  [welcome] JS fallback clicked: '{result['text']}'")
            page.wait_for_timeout(2000)
            return True
        print(f"  [welcome] JS fallback: no clickable button found")
    except Exception as e:
        print(f"  [welcome] JS fallback error: {e}")

    # ── Escape key ──
    print(f"  [welcome] Trying Escape key...")
    try:
        page.keyboard.press("Escape")
        page.wait_for_timeout(1000)
    except Exception:
        pass

    # ── Last resort: click outside the dialog ──
    print(f"  [welcome] Trying click at (10,10)...")
    try:
        page.mouse.click(10, 10)
        page.wait_for_timeout(500)
    except Exception:
        pass

    return False


def _dismiss_tour_overlay(page, bridge=None, timeout: int = 2000) -> bool:
    """Dismiss the ChatGPT onboarding tour overlay ("Ask anything" / "Skip Tour").

    A new post-registration welcome tour that appears on chatgpt.com/c/...
    showing "Ask anything", "From quick questions to big ideas, ChatGPT is
    here to help." with **Next** and **Skip Tour** buttons.

    Unlike _dismiss_welcome_modal (which targets ``role="dialog"``
    containers), this function targets the tour overlay directly by looking
    for the "Skip Tour" button — no container pre-scan phase.

    Returns True if the button was clicked, False otherwise.
    """

    # ── Phase 1: direct selector match (wide — Skip Tour can be any tag) ──
    skip_tour_selectors = [
        "button:has-text('Skip Tour')",
        "button:has-text('Skip tour')",
        "button.btn-ghost:has-text('Skip Tour')",
        ".btn-ghost:has-text('Skip Tour')",
        "a:has-text('Skip Tour')",
        "span:has-text('Skip Tour')",
        "[role='button']:has-text('Skip Tour')",
        "button:has-text('Skip')",
    ]

    for sel in skip_tour_selectors:
        try:
            btn = page.locator(sel).first
            if btn.is_visible(timeout=timeout):
                text = (btn.text_content() or "").strip()[:60]
                # Guard: only click if it actually says "Skip Tour" / "Skip"
                if not re.search(r'skip\s*tour', text, re.IGNORECASE) and \
                   not text.strip().lower() == "skip":
                    print(f"  [tour] Skipping ambiguous button: '{text}' ({sel})")
                    continue
                print(f"  [tour] Clicking '{text}' ({sel})")
                if bridge and bridge.is_ready:
                    try:
                        bridge.simulate_click(sel)
                    except Exception:
                        pass
                btn.click(force=True)
                page.wait_for_timeout(2000)
                return True
        except Exception:
            continue

    # ── Phase 2: JS fallback — search ALL visible elements ─────────────
    # The "Skip Tour" element may be an <a>, <span>, or any other tag
    # without a role attribute — we can't predict it.  Scan every visible
    # element in the DOM that contains "Skip Tour" text.
    print("  [tour] Trying JS fallback for Skip Tour...")
    try:
        result = page.evaluate("""() => {
            const all = [...document.querySelectorAll('*')];
            for (const el of all) {
                if (!el.offsetParent) continue;
                // Only check leaf-ish elements (≤3 children) to avoid
                // matching a huge container that happens to contain the
                // text somewhere deep inside.
                if (el.children.length > 3) continue;
                const t = (el.textContent || '').trim();
                if (t === 'Skip Tour' || t === 'Skip tour') {
                    el.click();
                    return {clicked: true, text: t, tag: el.tagName};
                }
            }
            return {clicked: false};
        }""")
        if result.get("clicked"):
            print(f"  [tour] JS fallback clicked: '{result['text']}' (tag={result.get('tag', '?')})")
            page.wait_for_timeout(2000)
            return True
        print("  [tour] JS fallback: no 'Skip Tour' element found")
    except Exception as e:
        print(f"  [tour] JS fallback error: {e}")

    # ── Phase 3: Escape key ─────────────────────────────────────────────
    print("  [tour] Trying Escape key...")
    try:
        page.keyboard.press("Escape")
        page.wait_for_timeout(1000)
    except Exception:
        pass

    return False


def _find_and_click(page, selectors, label: str = "element",
                    dismiss_overlay: bool = True, timeout: int = 2000,
                    bridge=None) -> tuple[bool, str]:
    """Click an element matching one of `selectors`. Returns (success, element_text).

    When `bridge` is provided and injected, uses human-simulator for
    natural mouse trajectory + hesitation; falls back to force-click.
    """
    for attempt in range(2):
        for sel in selectors:
            try:
                el = page.locator(sel).first
                if el.is_visible(timeout=timeout):
                    text = (el.text_content() or "").strip()[:80]
                    print(f"    Clicking: '{text}' ({sel})")
                    if bridge and bridge.is_ready:
                        if bridge.simulate_click(sel):
                            page.wait_for_timeout(300)
                            return True, text
                        # Bridge failed — fall through to native click
                    el.click(force=True)
                    page.wait_for_timeout(500)
                    return True, text
            except Exception:
                continue
        if attempt == 0 and dismiss_overlay:
            if _dismiss_overlays(page):
                print(f"    Retrying '{label}' after overlay dismissed...")
                continue
        break
    return False, ""


def _type_human(el, text: str, min_delay: int = 30, max_delay: int = 120,
                clear_first: bool = True, bridge=None, selector=None):
    """Type text into input with human-like per-character delays.

    When `bridge` is provided and injected, uses human-simulator for
    natural typing with errors/hesitation; falls back to native method.
    """
    # Try human-simulator bridge first
    if bridge and bridge.is_ready and selector:
        bridge_ok = bridge.simulate_typing(selector, text)
        if bridge_ok:
            # Verify typing survived (page may have navigated / re-rendered)
            try:
                actual = el.input_value()
                if actual and len(actual) >= len(text):
                    return
                if actual:
                    print(f"    [bridge] Typing incomplete: "
                          f"expected {len(text)} chars, got {len(actual)} — "
                          f"falling back to native typing")
                else:
                    print(f"    [bridge] Input empty after bridge typing — "
                          f"page likely navigated, falling back to native typing")
            except Exception:
                pass
        # Bridge failed or typing didn't stick — fall through to native method

    el.click(force=True)
    if clear_first:
        el.click(force=True, click_count=3)
        el.press("Backspace")
    if _inter_char_delay:
        for i, ch in enumerate(text):
            next_ch = text[i + 1] if i + 1 < len(text) else ''
            delay_ms = _inter_char_delay(ch, next_ch, i, text)
            el.type(ch, delay=max(10, min(500, int(delay_ms))))
    else:
        for ch in text:
            el.type(ch, delay=random.randint(min_delay, max_delay))


# ═══════════════════════════════════════════════════════════════════════════
# Page type detection
# ═══════════════════════════════════════════════════════════════════════════

def _login_entry_visible(page, body_text: str = "") -> bool:
    """Return whether the public ChatGPT login entry is visible.

    A ChatGPT home URL alone does not prove an authenticated session.  The
    public homepage can expose the login action as a button, link, or another
    interactive element depending on the current frontend deployment.
    """
    for selector in (
        "[data-testid='login-button']",
        "button:has-text('Log in')",
        "a:has-text('Log in')",
        "[role='button']:has-text('Log in')",
    ):
        try:
            if page.locator(selector).first.is_visible(timeout=500):
                return True
        except Exception:
            continue
    return bool(re.search(r"\bLog in\b", body_text, re.IGNORECASE))


def _detect_page_type(page) -> str:
    """Detect what page we're on. Returns one of:
    'login', 'email_input', 'otp', 'password', 'about_you',
    'chatgpt_home', 'auth_page', 'mfa_challenge', 'cf_challenge',
    'account_deactivated', 'getting_started', 'tour_welcome', 'unknown'
    """
    url = page.url.lower()
    try:
        body_text = (page.locator("body").inner_text(timeout=2000) or "").lower()
    except Exception:
        body_text = ""
    try:
        page_title = (page.title() or "").lower()
    except Exception:
        page_title = ""

    # CF challenge — detect by title OR body text
    if any(kw in body_text for kw in ["cloudflare", "verify you are human", "turnstile"]):
        return "cf_challenge"
    if any(kw in page_title for kw in ["just a moment", "attention required", "cloudflare"]):
        return "cf_challenge"
    # Getting Started / onboarding modal ("Tips for getting started" with
    # "Okay, let's go" button).  This modal appears AFTER the about-you form
    # is submitted (on chatgpt.com).  MUST be checked BEFORE the "about you"
    # body-text check below, otherwise the page is misidentified as about_you
    # and the script loops forever trying to fill fields that aren't there.
    try:
        if page.locator("[data-testid='getting-started-button']").first.is_visible(timeout=500):
            return "getting_started"
    except Exception:
        pass
    if any(kw in body_text for kw in ["tips for getting started", "okay, let's go"]):
        return "getting_started"

    # Tour/onboarding welcome overlay — new ChatGPT post-registration tour
    # showing "Ask anything" / "Skip Tour" on chatgpt.com/c/... pages.
    # MUST be checked BEFORE chatgpt_home (same URL domain; no auth path).
    # Also checked BEFORE the "about you" body-text check — the tour's
    # "big ideas" text could false-match "about" in some edge cases.
    try:
        if page.locator("button:has-text('Skip Tour')").first.is_visible(timeout=500):
            return "tour_welcome"
    except Exception:
        pass
    if any(kw in body_text for kw in ["ask anything"]) and \
       any(kw in body_text for kw in ["skip tour", "from quick questions"]):
        return "tour_welcome"

    # About you / profile completion
    if "about-you" in url or "about_you" in url:
        return "about_you"
    if any(kw in body_text for kw in ["about you", "tell us about yourself"]):
        return "about_you"

    # Session ended / stale session (e.g. "Your session has ended" on auth page)
    if any(kw in body_text for kw in ["your session has ended", "session has ended",
                                       "session ended"]):
        return "session_ended"

    # Account deactivated — MUST be checked BEFORE the email-verification
    # URL match below.  The error page lives on the same /email-verification
    # URL as the legitimate OTP form, so URL-based OTP detection would
    # false-match and cause an infinite OTP retry loop.
    if any(kw in body_text for kw in ["account_deactivated", "deleted or deactivated",
                                       "account has been deleted"]):
        return "account_deactivated"

    # Email OTP
    if any(kw in url for kw in ["email-otp", "email_otp", "verify-email", "email-verification"]):
        return "otp"
    if any(kw in body_text for kw in ["enter the code", "verification code",
                                       "enter this code", "verify your email"]):
        return "otp"

    # Password page
    if "password" in url:
        return "password"
    # Password input is present but not in URL
    try:
        pw = page.locator("input[type='password']").first
        if pw.is_visible(timeout=500):
            return "password"
    except Exception:
        pass
    if any(kw in body_text for kw in ["create a password", "set a password",
                                       "enter your password"]):
        return "password"

    # MFA/TOTP challenge page (must be before generic auth_page check —
    # URL is auth.openai.com/mfa-challenge/...)
    if "mfa-challenge" in url or "mfa_challenge" in url:
        return "mfa_challenge"

    # Auth pages (auth.openai.com)
    if "auth.openai.com" in url or "auth" in url:
        if any(kw in url for kw in ["log-in", "login"]):
            return "email_input"
        if any(kw in url for kw in ["sign-in", "signin"]):
            return "email_input"
        return "auth_page"
    # CF challenge on auth.openai.com: empty body + no visible buttons.
    # Placed AFTER URL-based checks so pages caught by specific URL patterns
    # (about-you, email-verification, etc.) don't get false-matched during
    # brief page transitions when the body is temporarily empty.
    if "auth.openai.com" in url and not body_text.strip():
        try:
            has_buttons = page.locator("button").first.is_visible(timeout=500)
        except Exception:
            has_buttons = False
        if not has_buttons:
            return "cf_challenge"


    # Check for login state on chatgpt.com BEFORE blanket URL match.
    # The "Log in" button and email input checks must come first, otherwise
    # a non-logged-in chatgpt.com homepage is misidentified as chatgpt_home.
    if "chatgpt.com" in url and "auth" not in url:
        # Check 1: email input visible inside the login modal → still logging in.
        # MUST come before the "Log in" button check — when the login modal is
        # open, the "Log in" button underneath is still visible to Playwright
        # (it doesn't check occlusion), so we'd never reach email_input retry.
        try:
            if page.locator("input[type='email']").first.is_visible(timeout=500):
                return "email_input"
        except Exception:
            pass
        # Check 2: public login entry visible → NOT logged in.
        if _login_entry_visible(page, body_text):
            return "login"
        # Check 3: no login indicators → truly logged in
        return "chatgpt_home"

    # Login modal / initial chatgpt.com page (non-chatgpt URLs)
    try:
        login_btn = page.locator("button:has-text('Log in')").first
        if login_btn.is_visible(timeout=500):
            return "login"
    except Exception:
        pass

    # Check for email-like input (the default input on login page)
    try:
        email_input = page.locator("#emailInput, input[type='email'], #phoneNumberInput").first
        if email_input.is_visible(timeout=500):
            # Check if there's a heading/subtitle suggesting email mode
            if "email" in body_text or "log in" in body_text:
                return "email_input"
    except Exception:
        pass

    return "unknown"


# ═══════════════════════════════════════════════════════════════════════════
# Main login function
# ═══════════════════════════════════════════════════════════════════════════

def email_login(
    email: str,
    password: str,
    *,
    fingerprint: Fingerprint | None = None,
    proxy: str | None = None,
    mailbox: ZohoMailbox | None = None,
    headless: bool = False,
    recon_enabled: bool = False,
    keep_logs: bool = False,
    setup_totp: bool = True,
    interactive: bool = False,
    use_matcher: bool = True,
    send_chat_message: bool = True,
    debug: bool = False,
    on_failure: str = "skip",
    adjust_settings: bool = False,
    totp_secret: str | None = None,
    existing_data: dict | None = None,
    _output_path_override: Path | None = None,
) -> dict:
    """
    Register a new account via the email-OTP log-in flow:
      Log in → Email → OTP → about_you → Logged In (= Registered).

    ⚠️  If OpenAI redirects to create-account/password instead of OTP,
    that is an unexpected deviation — the session fails immediately.

    When *adjust_settings* is True (the default from the CLI), after
    TOTP setup and chat warming (if enabled), the function navigates to
    ChatGPT settings and disables Memory + Improve Model.

    When re-logging into an existing account (via --input-dir /
    --fingerprint-json), the caller should also pass *totp_secret* (to
    auto-fill TOTP challenges) and *existing_data* (to preserve the
    original totp/fingerprint when re-saving the JSON).

    Returns dict with:
        success, stage, email, password, account_id,
        access_token, session_token, id_token, expires,
        user, fingerprint, oai_device_id, logs, error_message,
        settings_adjusted (when adjust_settings=True)
    """
    global _DEBUG_MODE
    _DEBUG_MODE = debug

    b = fingerprint.browser if fingerprint else {"ua": (
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36"
    ), "accept_language": "en-US,en;q=0.9", "platform": "Win32"}

    logs: list[str] = []

    def _log(msg: str):
        ts = datetime.now().strftime("%H:%M:%S")
        line = f"[{ts}] {msg}"
        logs.append(line)
        print(line)

    _log("=" * 65)
    _log(f"Email Registration via Log-In Flow — Playwright + Real Chrome CDP")
    _log(f"  Email:      {email}")
    _log(f"  Password:   {password[:4]}{'*' * (len(password) - 4) if len(password) > 4 else ''}")
    _log(f"  Proxy:      {proxy or 'none'}")
    _log(f"  Headless:   {headless}")
    _log(f"  CDP Port:   {CDP_PORT}")
    _log("=" * 65)

    # ── 1. Launch Chrome ──
    _kill_cdp_port(CDP_PORT)
    if not _launch_chrome(CDP_PORT, proxy=proxy):
        return {"success": False, "stage": "chrome_launch", "error_message": "Chrome launch failed", "logs": logs}
    if not _wait_for_cdp(CDP_PORT, timeout=40):
        return {"success": False, "stage": "cdp_wait", "error_message": "CDP timeout", "logs": logs}

    active_playwright = None
    browser = None
    context = None
    page = None
    cdp_session = None

    try:
        active_playwright = sync_playwright().start()

        # ── 2. Connect to Chrome via CDP ──
        _log("[Step 2] Connecting to Chrome via CDP...")
        browser = active_playwright.chromium.connect_over_cdp(f"http://127.0.0.1:{CDP_PORT}")
        _log(f"  Connected. Contexts: {len(browser.contexts)}")

        # ── 3. Create context with fingerprint ──
        _log("[Step 3] Creating browser context...")
        proxy_config = None
        if proxy:
            p = proxy.strip()
            if p.startswith("socks5h://"):
                p = "socks5://" + p[len("socks5h://"):]
            proxy_config = {"server": p}

        context = browser.new_context(
            viewport={"width": 1680, "height": 1200},
            locale="en-US",
            timezone_id="America/New_York",
            proxy=proxy_config,
            user_agent=b["ua"],
        )
        page = context.new_page()

        # Close other about:blank windows
        for ctx in browser.contexts:
            if ctx == context:
                continue
            try:
                for pg in ctx.pages:
                    pg.close()
            except Exception:
                pass

        # ── 4. CDP anti-detection ──
        _log("[Step 4] CDP anti-detection...")
        cdp_session = context.new_cdp_session(page)
        cdp_session.send("Network.setUserAgentOverride", {
            "userAgent": b["ua"],
            "acceptLanguage": b["accept_language"],
            "platform": b["platform"],
        })
        cdp_session.send("Page.addScriptToEvaluateOnNewDocument", {
            "source": _CDP_ANTI_DETECTION_SCRIPT
        })

        # Bring Chrome window to foreground (especially important for
        # batch runs where Windows foreground-lock may otherwise keep
        # the second/third launch behind the terminal).
        try:
            page.bring_to_front()
        except Exception:
            pass

        # Capture oai-session-id from requests
        _captured_oai_sid: list[str] = []

        def _on_request(request):
            if _captured_oai_sid:
                return
            try:
                h = {k.lower(): v for k, v in (request.headers or {}).items()}
                sid = h.get("oai-session-id", "")
                if sid:
                    _captured_oai_sid.append(sid)
                    _log(f"  Captured oai-session-id: {sid}")
            except Exception:
                pass

        page.on("request", _on_request)

        # Keep a small, metadata-only trace of requests caused by submitting
        # the email form.  This is enabled only for that transition so logs
        # can explain a failed handoff without retaining request bodies, URLs
        # with query parameters, cookies, or credentials.
        email_submit_trace: dict[str, Any] = {"active": False, "responses": []}

        def _on_email_submit_response(response):
            if not email_submit_trace["active"]:
                return
            try:
                from urllib.parse import urlsplit

                parsed = urlsplit(response.url)
                hostname = (parsed.hostname or "").lower()
                if not any(domain in hostname for domain in ("chatgpt.com", "openai.com")):
                    return
                responses = email_submit_trace["responses"]
                if len(responses) >= 16:
                    return
                responses.append({
                    "host": hostname,
                    "path": parsed.path,
                    "status": response.status,
                    "content_type": response.headers.get("content-type", "").split(";", 1)[0],
                })
            except Exception:
                pass

        page.on("response", _on_email_submit_response)

        # ── 4b. Text-based page-state fallback matcher ──
        matcher = None
        if use_matcher and PageStateMatcher is not None:
            try:
                matcher = PageStateMatcher(states_file=str(_THIS_DIR / "page_states.json"))
                _log(f"  [matcher] PageStateMatcher initialised")
            except Exception as e:
                _log(f"  [matcher] Failed to initialise: {e}")

        # ── 5. Visit chatgpt.com ──
        _log("[Step 5] Visiting chatgpt.com (CF-aware)...")
        # Single goto (aligned with chatgpt_login.py). If redirected to auth,
        # we handle it below by skipping the CF wait for "Log in" button.
        try:
            page.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=30000)
        except Exception:
            page.goto("https://chatgpt.com/", wait_until="commit", timeout=30000)

        # Check early if redirected to auth page (like chatgpt_login.py:1703-1707).
        # If already on auth, skip the CF wait for "Log in" button — it doesn't exist there.
        current_url_lower = page.url.lower()
        on_auth_page = (
            "auth.openai.com" in current_url_lower
            or "chatgpt.com/auth" in current_url_lower
        )

        if on_auth_page:
            _log(f"  Landed on auth page: {page.url[:120]} — skipping CF wait for 'Log in' button")
            page.wait_for_timeout(2000)
        else:
            # CF-aware wait for login UI (chatgpt.com homepage)
            _log("  Waiting for 'Log in' button (CF-aware)...")
            _wait_for_element_after_cf(
                page,
                target_selectors=[
                    "[data-testid='login-button']",
                    "button:has-text('Log in')",
                ],
                timeout=360,
            )
            try:
                page.wait_for_load_state("networkidle", timeout=10000)
            except Exception:
                pass
            # After CF solve the page HTML/buttons are visible but React/Next.js
            # may not have hydrated yet — clicking too early hits dead listeners.
            # A short settle gives the SPA time to attach event handlers.
            # (Recovery stage 2 provides a longer wait+retry as safety net.)
            page.wait_for_timeout(1500)
            _log(f"    Landed at: {page.url[:120]}")

        # ── 5b. Inject Human Simulator bridge (aligned with chatgpt_login.py) ──
        bridge = None
        if _HUMAN_SIM:
            try:
                if PageInteractionBridge is None:
                    _log("  [bridge] page_interaction module not available — skipping")
                else:
                    bridge = PageInteractionBridge(page, verbose=False,
                        error_rates={"typing": 0, "click_hesitation": 0.15,
                                     "misclick": 0, "scroll_drift": 0})
                    bridge.inject()
                    _log("  [bridge] Human simulator injected")
            except Exception as e:
                _log(f"  [bridge] Failed to initialize: {e}")
                bridge = None

        # ── State tracking for recovery loops ──────────────────────────
        _cf_streak = 0          # consecutive CF detections without progress
        _otp_streak = 0          # consecutive OTP rounds without progress (error page detection)
        _email_streak = 0        # consecutive email_input/auth_page rounds without advancing
        _CF_STREAK_LIMIT = 1    # max CF rounds before abandoning (solver internally
                                # retries up to _MAX_CF_SOLVES=3 per round)
        _restart_count = 0      # browser restart counter (fresh Chrome profile)
        _RESTART_LIMIT = 2      # max browser restarts before giving up
        _prev_url = page.url  # Track URL across rounds to avoid redundant 20s waits

        # ── Nested helper: restart browser with a fresh Chrome profile ──
        # Called when the email_input page is stuck (URL unchanged after
        # email submission), which usually means OpenAI has flagged the
        # current browser session.  A new Chrome user-data-dir gives us
        # clean cookies/localStorage and breaks the redirect loop.
        def _restart_browser():
            """Kill current Chrome, launch fresh profile, reconnect everything."""
            nonlocal active_playwright, browser, context, page, cdp_session
            nonlocal bridge, matcher, _captured_oai_sid, _prev_url, _cf_streak, _email_streak

            _log("  [restart] Closing current browser session...")
            try:
                page.close()
            except Exception:
                pass
            try:
                context.close()
            except Exception:
                pass
            try:
                browser.close()
            except Exception:
                pass
            try:
                active_playwright.stop()
            except Exception:
                pass

            _log("  [restart] Killing and re-launching Chrome (fresh profile)...")
            _kill_cdp_port(CDP_PORT)
            time.sleep(1)
            if not _launch_chrome(CDP_PORT, proxy=proxy):
                raise RuntimeError("Chrome re-launch failed")
            if not _wait_for_cdp(CDP_PORT, timeout=40):
                raise RuntimeError("CDP re-connect timeout")

            _log("  [restart] Connecting to fresh Chrome via CDP...")
            active_playwright = sync_playwright().start()
            browser = active_playwright.chromium.connect_over_cdp(
                f"http://127.0.0.1:{CDP_PORT}"
            )

            proxy_config = None
            if proxy:
                p_val = proxy.strip()
                if p_val.startswith("socks5h://"):
                    p_val = "socks5://" + p_val[len("socks5h://"):]
                proxy_config = {"server": p_val}

            context = browser.new_context(
                viewport={"width": 1680, "height": 1200},
                locale="en-US",
                timezone_id="America/New_York",
                proxy=proxy_config,
                user_agent=b["ua"],
            )
            page = context.new_page()

            # Close other about:blank windows
            for ctx in browser.contexts:
                if ctx == context:
                    continue
                try:
                    for pg in ctx.pages:
                        pg.close()
                except Exception:
                    pass

            _log("  [restart] Applying CDP anti-detection...")
            cdp_session = context.new_cdp_session(page)
            cdp_session.send("Network.setUserAgentOverride", {
                "userAgent": b["ua"],
                "acceptLanguage": b["accept_language"],
                "platform": b["platform"],
            })
            cdp_session.send("Page.addScriptToEvaluateOnNewDocument", {
                "source": _CDP_ANTI_DETECTION_SCRIPT
            })

            try:
                page.bring_to_front()
            except Exception:
                pass

            # Re-attach oai-session-id capture
            _captured_oai_sid.clear()

            def _on_request_restart(request):
                if _captured_oai_sid:
                    return
                try:
                    h = {k.lower(): v for k, v in (request.headers or {}).items()}
                    sid = h.get("oai-session-id", "")
                    if sid:
                        _captured_oai_sid.append(sid)
                        _log(f"  Captured oai-session-id: {sid}")
                except Exception:
                    pass

            page.on("request", _on_request_restart)

            # Re-init page-state matcher
            matcher = None
            if use_matcher and PageStateMatcher is not None:
                try:
                    matcher = PageStateMatcher(
                        states_file=str(_THIS_DIR / "page_states.json")
                    )
                    _log("  [matcher] PageStateMatcher re-initialised")
                except Exception as e:
                    _log(f"  [matcher] Failed to re-initialise: {e}")

            # Re-inject human-simulator bridge
            bridge = None
            if _HUMAN_SIM and PageInteractionBridge is not None:
                try:
                    bridge = PageInteractionBridge(
                        page, verbose=False,
                        error_rates={"typing": 0, "click_hesitation": 0.15,
                                     "misclick": 0, "scroll_drift": 0},
                    )
                    bridge.inject()
                    _log("  [bridge] Human simulator re-injected")
                except Exception as e:
                    _log(f"  [bridge] Failed to re-initialize: {e}")

            # Navigate to chatgpt.com
            _log("  [restart] Visiting chatgpt.com with fresh session...")
            try:
                page.goto(
                    "https://chatgpt.com/",
                    wait_until="domcontentloaded", timeout=30000,
                )
            except Exception:
                page.goto(
                    "https://chatgpt.com/",
                    wait_until="commit", timeout=30000,
                )

            current_url_lower = page.url.lower()
            on_auth = (
                "auth.openai.com" in current_url_lower
                or "chatgpt.com/auth" in current_url_lower
            )

            if on_auth:
                _log(f"  [restart] Landed on auth page: {page.url[:120]} — "
                     f"skipping CF wait")
                page.wait_for_timeout(2000)
            else:
                _log("  [restart] Waiting for 'Log in' button (CF-aware)...")
                _wait_for_element_after_cf(
                    page,
                    target_selectors=[
                        "[data-testid='login-button']",
                        "button:has-text('Log in')",
                    ],
                    timeout=360,
                )
                try:
                    page.wait_for_load_state("networkidle", timeout=10000)
                except Exception:
                    pass
                page.wait_for_timeout(1500)
                _log(f"  [restart] Landed at: {page.url[:120]}")

                # Click "Log in" so the state machine starts from the login modal
                _log("  [restart] Clicking 'Log in' to open login modal...")
                _find_and_click(page, [
                    "[data-testid='login-button']",
                    "button:has-text('Log in')",
                    "a:has-text('Log in')",
                ], label="'Log in' button (restart)", bridge=bridge)
                page.wait_for_timeout(3000)
                # Re-check: did the click redirect us to auth?
                if "auth.openai.com" in page.url.lower() or "chatgpt.com/auth" in page.url.lower():
                    on_auth = True
                    _log(f"  [restart] Redirected to auth after 'Log in' click")

            # Reset tracking state
            _prev_url = page.url
            _cf_streak = 0
            _email_streak = 0

            _log("  [restart] Browser session restarted successfully")
            return on_auth

        if recon_enabled:
            _recon_page_snapshot(page, "AFTER chatgpt.com LOAD", indent=0)

        # ── 6. Click "Log in" (if not on auth page) ──
        if not on_auth_page:
            _log("[Step 6] Clicking 'Log in'...")
            clicked, _ = _find_and_click(page, [
                "[data-testid='login-button']",
                "button:has-text('Log in')",
                "a:has-text('Log in')",
            ], label="'Log in' button", bridge=bridge)
            if not clicked:
                _log("  [!] 'Log in' not found, trying JS fallback...")
                try:
                    page.evaluate("""() => {
                        const els = [...document.querySelectorAll('button, a, [role="button"]')];
                        const btn = els.find(e => /log\\s*in/i.test(e.textContent || ''));
                        if (btn) { btn.click(); return true; }
                        return false;
                    }""")
                except Exception:
                    pass
            page.wait_for_timeout(3000)
            if recon_enabled:
                _recon_page_snapshot(page, "AFTER LOG IN CLICK", indent=0)

            # Verify modal actually opened (CF 403 can block JS needed for modal)
            modal_indicators = [
                "button:has-text('Continue with Google')",
                "button:has-text('Continue with Apple')",
                "button:has-text('Continue with phone')",
                "input[type='email']",
                "[role='dialog']",
            ]
            modal_opened = False
            for indicator in modal_indicators:
                try:
                    if page.locator(indicator).first.is_visible(timeout=2000):
                        _log(f"  Login modal confirmed via: {indicator}")
                        modal_opened = True
                        break
                except Exception:
                    continue

            if not modal_opened:
                _log("  [!] Login modal did not open (CF 403 on CDN?)")
                _save_screenshot(page, "modal_not_opened")
                # B3: Instead of refreshing the page (which compounds CF suspicion),
                # first try a JS-level re-click without reloading. If that fails,
                # fall back to navigating directly to auth.openai.com/login.
                _log("  Retrying via JS click (no page refresh)...")
                page.wait_for_timeout(2000)
                try:
                    page.evaluate("""() => {
                        const els = [...document.querySelectorAll('button, a, [role="button"]')];
                        const btn = els.find(e => /log\\s*in/i.test(e.textContent || ''));
                        if (btn) { btn.click(); return true; }
                        return false;
                    }""")
                except Exception:
                    pass
                page.wait_for_timeout(3000)
                # Check again
                for indicator in modal_indicators:
                    try:
                        if page.locator(indicator).first.is_visible(timeout=2000):
                            _log(f"  Login modal confirmed (JS retry) via: {indicator}")
                            modal_opened = True
                            break
                    except Exception:
                        continue

            if not modal_opened:
                # ── Recovery stage 2: wait + re-click ──────────────────
                # JS hydration may be slow after CF; a short wait + re-click
                # on the correct button often succeeds when the first click
                # was too early.  This matches the user's manual experience.
                _log("  [!] Modal still not open after JS retry — waiting 5s for late hydration...")
                page.wait_for_timeout(5000)
                _log("  Retrying click on [data-testid='login-button'] after wait...")
                try:
                    btn = page.locator("[data-testid='login-button']").first
                    if btn.is_visible(timeout=2000):
                        btn.click(force=True)
                        page.wait_for_timeout(3000)
                        for indicator in modal_indicators:
                            try:
                                if page.locator(indicator).first.is_visible(timeout=2000):
                                    _log(f"  Login modal confirmed (wait+retry) via: {indicator}")
                                    modal_opened = True
                                    break
                            except Exception:
                                continue
                except Exception:
                    pass

            if not modal_opened:
                _log("  [!] Modal still not open — clearing storage and restarting from chatgpt.com...")
                _save_screenshot(page, "modal_still_not_opened")
                # Clear any residual cookies/storage that might cause a
                # "Your session has ended" half-state on the auth page,
                # then restart the whole flow from chatgpt.com.
                try:
                    page.context.clear_cookies()
                    page.evaluate("localStorage.clear(); sessionStorage.clear();")
                except Exception:
                    pass
                try:
                    page.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=30000)
                except Exception:
                    page.goto("https://chatgpt.com/", wait_until="commit", timeout=30000)
                page.wait_for_timeout(5000)
                _log(f"  Landed at: {page.url[:120]}")
                # Retry clicking "Log in" on the fresh page
                _log("  Retrying 'Log in' click on fresh chatgpt.com...")
                clicked2, _ = _find_and_click(page, [
                    "[data-testid='login-button']",
                    "button:has-text('Log in')",
                    "a:has-text('Log in')",
                ], label="'Log in' button (retry)", bridge=bridge)
                page.wait_for_timeout(3000)
                if clicked2:
                    for indicator in modal_indicators:
                        try:
                            if page.locator(indicator).first.is_visible(timeout=2000):
                                _log(f"  Login modal confirmed (retry) via: {indicator}")
                                modal_opened = True
                                break
                        except Exception:
                            continue
                if not modal_opened:
                    _log("  [!] Modal still not open after restart — may be CF block on this IP")

            if not modal_opened:
                _log("  [!] Login modal not open after all recovery attempts")
                # ── Recovery stage 3: restart browser with fresh Chrome profile ──
                # CF WAF 403 can permanently block the current CDP session.
                # A fresh browser profile (new user-data-dir) often bypasses this.
                # This runs automatically even in --interactive mode because
                # this is a known failure pattern with a proven fix.
                if _restart_count < _RESTART_LIMIT:
                    _restart_count += 1
                    _log(f"  [restart] Attempting browser restart {_restart_count}/{_RESTART_LIMIT} "
                         f"(CF WAF suspected — fresh Chrome profile may bypass)...")
                    _save_screenshot(page, f"modal_pre_restart_{_restart_count}")
                    try:
                        on_auth_after = _restart_browser()
                    except Exception as e:
                        _log(f"  [restart] Browser restart failed: {e}")
                        on_auth_after = False

                    if on_auth_after:
                        _log("  [restart] Landed on auth page after restart — proceeding to login flow")
                        modal_opened = True  # skip to Step 7; browser is now on auth page
                    else:
                        # Re-check if login modal opened on chatgpt.com
                        for indicator in modal_indicators:
                            try:
                                if page.locator(indicator).first.is_visible(timeout=2000):
                                    _log(f"  Login modal confirmed (after restart) via: {indicator}")
                                    modal_opened = True
                                    break
                            except Exception:
                                continue
                        if not modal_opened:
                            _log("  [!] Modal still not open after browser restart")

                if not modal_opened:
                    if interactive:
                        _prompt_user("Login modal not opening — browser is open for inspection", interactive=True)
                        # User may have manually opened the modal — check one last time
                        for indicator in modal_indicators:
                            try:
                                if page.locator(indicator).first.is_visible(timeout=2000):
                                    _log(f"  Login modal confirmed (manual) via: {indicator}")
                                    modal_opened = True
                                    break
                            except Exception:
                                continue
                    if not modal_opened:
                        return {
                            "success": False, "stage": "modal_not_opened",
                            "email": email, "password": password,
                            "error_message": "Login modal did not open after all recovery attempts (CF block or JS hydration failure)",
                            "logs": logs,
                        }

        # ── 7. Detect page type and handle ──
        _log("[Step 7] Detecting page type...")
        page_type = _detect_page_type(page)
        _log(f"  Page type: {page_type}")

        # ── 7a. Input email ──
        _log("[Step 7a] Inputting email...")
        email_input_selectors = [
            "#emailInput",
            "input[type='email']",
            "#phoneNumberInput",  # Same input used for email
            "input[type='tel']",
            "input[name='__reservedForPhoneNumberInput_tel']",
            "input[autocomplete='email']",
            "input[autocomplete='username']",
        ]
        email_el = None
        email_sel = None
        for sel in email_input_selectors:
            try:
                el = page.locator(sel).first
                if el.is_visible(timeout=3000):
                    email_el = el
                    email_sel = sel
                    _log(f"  Found email input: {sel}")
                    break
            except Exception:
                continue

        if email_el:
            # Skip bridge typing for email: React controlled inputs in the
            # login modal don't sync state from dispatched keyboard events.
            # Real users often autocomplete or copy-paste emails anyway, so
            # native fill() is actually more human-like than char-by-char.
            email_el.click(force=True)
            email_el.fill(email)
            page.wait_for_timeout(500)
            val = email_el.input_value()
            _log(f"  Input value: '{val}'")
        else:
            _log("  [!] Email input not found — check screenshot")
            _save_screenshot(page, "no_email_input")
            if recon_enabled:
                _recon_page_snapshot(page, "NO EMAIL INPUT FOUND", indent=0)

        # ── 7b. Snapshot mailbox BEFORE clicking Continue ──
        # The Continue button triggers OpenAI to send the OTP email.
        # We must capture before_ids NOW so the OTP email (which will
        # arrive seconds later) appears as "new" and is not skipped.
        otp_before_ids: set[str] | None = None
        if mailbox is not None:
            fake_account = MailboxAccount(email=email, account_id=email)
            otp_before_ids = mailbox.get_current_ids(fake_account)
            _log(f"  Mailbox snapshot: {len(otp_before_ids)} existing emails (before OTP trigger)")

        # ── 7c. Click Continue after email ──
        _log("[Step 7c] Clicking Continue after email...")
        email_submit_trace["active"] = True
        submit_selectors = [
            "button[type='submit']",
            "button:has-text('Continue')",
            "button:has-text('Next')",
            "button[value='validate']",
            "button[value='email']",
        ]
        clicked = False
        for sel in submit_selectors:
            try:
                btn = page.locator(sel).first
                if btn.is_visible(timeout=1000):
                    text = (btn.text_content() or "").strip()[:50]
                    _log(f"  Clicking: '{text}' ({sel})")
                    btn.click(force=True)
                    clicked = True
                    break
            except Exception:
                continue
        if not clicked:
            _log("  [!] Could not find Continue button — trying Enter key...")
            page.keyboard.press("Enter")

        # ── 8. Handle post-email pages (OTP / password / about_you) ──
        # State machine loop to handle whatever page comes next
        MAX_ROUNDS = 10

        # Persist about_you fields so they can be written to the final result
        about_name = ""
        about_birthdate = ""
        about_age = 0

        for rd in range(MAX_ROUNDS):
            _log(f"\n{'─' * 55}")
            _log(f"  Round {rd + 1}/{MAX_ROUNDS} — post-email page handling")

            # Wait for page to navigate away from previous URL (up to 20s).
            # Skip the wait if navigation already happened during the previous
            # round's settle timeout — avoids wasting 20s on a page that has
            # already fully loaded (e.g. about-you after OTP submit).
            current_url = page.url
            if current_url != _prev_url:
                _log(f"  URL changed (no wait needed): {current_url[:150]}")
                _prev_url = current_url
            else:
                _log("  Waiting for page to settle...")
                try:
                    page.wait_for_url(lambda url: url != _prev_url, timeout=20000)
                    _log(f"  Navigated to: {page.url[:150]}")
                    _prev_url = page.url
                except Exception:
                    _log(f"  URL unchanged after 20s: {page.url[:150]}")
                page.wait_for_timeout(1000)  # extra settle time

            # Give extra time for CF challenge / slow loads
            page.wait_for_timeout(2000)

            # Detect page type BEFORE CF wait to see if it's CF challenge
            page_type = _detect_page_type(page)
            _log(f"  Initial page type: {page_type}")

            if rd == 0:
                email_submit_trace["active"] = False
                trace = email_submit_trace["responses"]
                if trace:
                    _log("  Email-submit response metadata: " + json.dumps(trace, sort_keys=True))
                else:
                    _log("  [!] No ChatGPT/OpenAI response observed after email submission")
                if page_type == "login":
                    _log("  [!] Returned to the public login page after email submission")
                    _save_screenshot(page, "email_submit_returned_to_login")

            # If CF challenge on auth.openai.com, solve it first
            if page_type == "cf_challenge":
                _cf_streak += 1
                _log(f"  >>> CF Challenge #{_cf_streak}/{_CF_STREAK_LIMIT} — solving...")
                if _cf_streak > _CF_STREAK_LIMIT:
                    _log(f"  [!] CF death loop: {_cf_streak} consecutive challenges")
                    _save_screenshot(page, f"cf_death_loop_round{rd + 1}")
                    # ── Recovery: restart browser with fresh Chrome profile ──
                    # CF is looping because the current browser session is
                    # flagged.  A fresh profile often bypasses this.
                    if _restart_count < _RESTART_LIMIT:
                        _restart_count += 1
                        _log(f"  [restart] Attempting browser restart {_restart_count}/{_RESTART_LIMIT} "
                             f"(CF death loop — fresh Chrome profile may bypass)...")
                        try:
                            _restart_browser()
                            _cf_streak = 0
                            _prev_url = ""
                            _log("  [restart] Browser restarted — re-entering state machine loop")
                            continue
                        except Exception as e:
                            _log(f"  [restart] Browser restart failed: {e}")
                    # Restart failed or limit reached — abandon this session
                    _log(f"  [!] Browser restart limit ({_RESTART_LIMIT}) exhausted — abandoning session")
                    try:
                        page.context.clear_cookies()
                        page.evaluate("localStorage.clear(); sessionStorage.clear();")
                    except Exception:
                        pass
                    return {
                        "success": False, "stage": "cf_death_loop",
                        "email": email, "password": password,
                        "error_message": f"CF death loop: {_cf_streak} consecutive challenges without progress after {_RESTART_LIMIT} browser restarts",
                        "logs": logs,
                    }
                # Exponential backoff: 0s (1st), 3s (2nd), 6s (3rd)
                if _cf_streak >= 2:
                    backoff = 3 * (2 ** (_cf_streak - 2))
                    _log(f"  CF backoff: waiting {backoff}s before retry...")
                    page.wait_for_timeout(backoff * 1000)
                _save_screenshot(page, f"cf_auth_round{rd + 1}")
                _wait_for_element_after_cf(
                    page,
                    target_selectors=[
                        "input[inputmode='numeric']",
                        "input[autocomplete='one-time-code']",
                        "input[type='email']",
                        "input[name='name']",
                        "#aboutYouName",
                        "button:has-text('Continue')",
                        "button[type='submit']",
                    ],
                    timeout=120,
                    force_cf=True,   # page is already showing CF — skip network detection
                )
                page.wait_for_timeout(3000)
                page_type = _detect_page_type(page)
                if page_type != "cf_challenge":
                    _cf_streak = 0  # CF solved, page moved on

            if recon_enabled:
                _recon_page_snapshot(page, f"ROUND {rd + 1} — page_type={page_type}", indent=0)
            _log(f"  Page type: {page_type}")

            # Reset CF streak when on a non-CF page (progress was made)
            if page_type != "cf_challenge":
                _cf_streak = 0
            # Reset OTP streak when on a non-OTP page (progress was made)
            if page_type != "otp":
                _otp_streak = 0
            # Reset email streak when on a non-email-input page (progress was made)
            if page_type not in ("email_input", "auth_page"):
                _email_streak = 0

            if page_type == "session_ended":
                _log("  >>> Session ended page detected — clearing storage and restarting from chatgpt.com...")
                _save_screenshot(page, f"session_ended_round{rd + 1}")
                try:
                    page.context.clear_cookies()
                    page.evaluate("localStorage.clear(); sessionStorage.clear();")
                except Exception:
                    pass
                try:
                    page.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=30000)
                except Exception:
                    page.goto("https://chatgpt.com/", wait_until="commit", timeout=30000)
                page.wait_for_timeout(3000)
                _log(f"  Restarted at: {page.url[:120]}")
                # If we landed on chatgpt.com home (already logged in), break
                if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                    _log("  Landed on chatgpt.com — may already be logged in")
                    break
                # Otherwise retry the "Log in" click
                _log("  Retrying 'Log in' click...")
                _find_and_click(page, [
                    "[data-testid='login-button']",
                    "button:has-text('Log in')",
                    "a:has-text('Log in')",
                ], label="'Log in' button (session-ended restart)", bridge=bridge)
                page.wait_for_timeout(3000)
                continue

            elif page_type == "account_deactivated":
                _log("  >>> Account DEACTIVATED — this account has been deleted or banned by OpenAI")
                _save_screenshot(page, f"account_deactivated_round{rd + 1}")

                # Persist is-ban marker to the account JSON so the batch
                # runner skips this account on future runs.
                safe_email = email.replace("@", "_at_").replace("+", "")
                output_path = _output_path_override or (_FAILED_OUTPUT_DIR / f"session-{safe_email}.json")
                try:
                    existing: dict = {}
                    if output_path.exists():
                        try:
                            with open(output_path, "r", encoding="utf-8") as _f:
                                existing = json.load(_f)
                        except Exception:
                            pass  # corrupt or empty — start fresh
                    existing["is-ban"] = True
                    existing["success"] = False
                    existing["error_message"] = (
                        existing.get("error_message", "")
                        or "Account has been deleted or deactivated by OpenAI (account_deactivated)"
                    )
                    existing["session_refreshed_at"] = datetime.now(timezone(timedelta(hours=8))).isoformat()
                    with open(output_path, "w", encoding="utf-8") as _f:
                        json.dump(existing, _f, ensure_ascii=False, indent=2)
                    _log(f"  [*] Marked is-ban=true in {output_path}")
                except Exception as _e:
                    _log(f"  [!] Failed to save is-ban to JSON: {_e}")

                return {
                    "success": False, "stage": "account_deactivated",
                    "email": email, "password": password,
                    "error_message": (
                        "Account has been deleted or deactivated by OpenAI "
                        "(account_deactivated)"
                    ),
                    "logs": logs,
                }

            elif page_type == "chatgpt_home":
                _log("  >>> Landed on chatgpt.com — login complete!")
                break

            elif page_type == "mfa_challenge":
                _log("  >>> MFA/TOTP challenge page detected")
                _save_screenshot(page, f"mfa_challenge_round{rd + 1}")

                if totp_secret:
                    _log("  Generating TOTP code from stored secret...")
                    if _handle_totp_challenge_during_login(page, totp_secret):
                        page.wait_for_timeout(3000)
                        if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                            _log("  TOTP accepted — login complete!")
                            break
                        _prev_url = page.url
                        continue
                    _log("  [!] TOTP auto-fill did not advance the page")
                else:
                    _log("  [!] No TOTP secret available — cannot auto-fill MFA challenge")

                _save_screenshot(page, f"mfa_challenge_blocked_round{rd + 1}")
                _prompt_user(
                    "MFA/TOTP challenge — enter code manually in browser, then press Enter",
                    interactive=interactive,
                )
                # Check if user completed it manually
                page.wait_for_timeout(3000)
                if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                    _log("  User completed MFA manually — continuing...")
                    break
                _prev_url = page.url
                continue

            elif page_type == "otp":
                _otp_streak += 1
                _log(f"  >>> Email OTP page detected (streak={_otp_streak})")

                # ── Error page detection ─────────────────────────────────
                # After the 1st OTP attempt, if we're still stuck on the
                # email-verification URL, the page may actually be showing
                # "Oops, an error occurred! Route Error (400 ...)" instead
                # of a legitimate OTP form.  Check via text-based matcher
                # before blindly retrying OTP.
                if _otp_streak >= 2 and matcher is not None:
                    _log("  OTP streak ≥2 — checking matcher for error page...")
                    _save_screenshot(page, f"otp_streak{_otp_streak}_round{rd + 1}")
                    recovery = matcher.identify_and_recover(page)
                    if recovery["matched"]:
                        _log(f"  Matcher identified: {recovery['template_id']} "
                             f"(score={recovery['score']:.2f}, "
                             f"recovered={recovery['recovered']})")
                        if recovery["recovered"]:
                            _log("  Error page recovered — re-entering state loop")
                            _prev_url = ""
                            continue
                    else:
                        _log("  Matcher did not match — proceeding with OTP retry")
                # ── End error page detection ─────────────────────────────

                # ── TOTP challenge handling (re-login with stored secret) ──
                # When re-logging into an account with TOTP enabled, the
                # 2nd OTP-like page is actually the TOTP challenge.  Try
                # the stored secret before falling back to manual input.
                if _otp_streak >= 2 and totp_secret:
                    _log("  OTP streak ≥2 — may be TOTP challenge, trying stored secret...")
                    _save_screenshot(page, f"otp_totp_attempt_round{rd + 1}")
                    if _handle_totp_challenge_during_login(page, totp_secret):
                        page.wait_for_timeout(3000)
                        _prev_url = page.url
                        continue  # re-enter state machine — should advance past OTP
                    _log("  TOTP attempt did not succeed — falling through to normal OTP flow")

                if mailbox is None:
                    # ── TOTP fallback: try stored secret even on 1st OTP page ──
                    if totp_secret:
                        _log("  No mailbox — trying TOTP from stored secret...")
                        if _handle_totp_challenge_during_login(page, totp_secret):
                            page.wait_for_timeout(3000)
                            if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                                _log("  TOTP accepted — continuing...")
                                break
                            _prev_url = page.url
                            continue
                    _log("  [!] No mailbox configured — cannot get OTP")
                    _prompt_user("OTP required but no mailbox configured", interactive=interactive)
                    # User may have entered OTP manually — try fetching session anyway
                    page.wait_for_timeout(3000)
                    if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                        _log("  User completed OTP manually — continuing...")
                        break

                fake_account = MailboxAccount(email=email, account_id=email)

                _log(f"  Polling OTP for {email} (timeout=120s)...")
                otp = mailbox.wait_for_code(
                    fake_account,
                    timeout=120,
                    before_ids=otp_before_ids,
                )
                if not otp:
                    # ── TOTP fallback ──
                    if totp_secret:
                        _log("  Email OTP timeout — trying TOTP from stored secret...")
                        _save_screenshot(page, f"otp_timeout_totp_fallback_round{rd + 1}")
                        if _handle_totp_challenge_during_login(page, totp_secret):
                            page.wait_for_timeout(3000)
                            if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                                _log("  TOTP accepted — continuing...")
                                break
                            _prev_url = page.url
                            continue
                    # OTP timeout — terminal.  Interactive: pause for manual
                    # entry.  Batch: fail immediately, let on_failure handle it.
                    if interactive:
                        _log("  [!] OTP timeout — pausing for manual intervention")
                        _save_screenshot(page, "otp_timeout")
                        _prompt_user("OTP not received — enter it manually in browser, "
                                     "then press Enter", interactive=True)
                        if "chatgpt.com" in page.url.lower() and "auth" not in page.url.lower():
                            _log("  User completed OTP manually — continuing...")
                            break
                    else:
                        _log("  [!] OTP timeout — marking as failure")
                    return {
                        "success": False, "stage": "otp_timeout",
                        "email": email, "password": password,
                        "error_message": "OTP not received within timeout",
                    }

                _log(f"  Got OTP: ...{otp[-2:]}")

                # Fill OTP
                otp_input_selectors = [
                    "input[autocomplete='one-time-code']",
                    "input[inputmode='numeric']",
                    "input[name*='code' i]",
                    "input[data-testid*='code']",
                ]
                # Try split inputs first (6 separate fields)
                try:
                    digit_inputs = [
                        el for el in page.locator("input[inputmode='numeric']").all()
                        if el.is_visible(timeout=300)
                    ]
                    if len(digit_inputs) >= 6:
                        _log(f"  Filling {len(digit_inputs)} split OTP inputs...")
                        for i, digit in enumerate(otp):
                            try:
                                digit_inputs[i].click(force=True)
                                digit_inputs[i].type(digit, delay=random.randint(30, 80))
                            except Exception:
                                pass
                        page.wait_for_timeout(500)
                        # Click submit
                        for s in ["button[type='submit']", "button:has-text('Continue')",
                                   "button:has-text('Verify')"]:
                            try:
                                btn = page.locator(s).first
                                if btn.is_visible(timeout=1000):
                                    btn.click(force=True)
                                    _log(f"  OTP submitted via {s}")
                                    break
                            except Exception:
                                continue
                    else:
                        raise RuntimeError("not enough split inputs")
                except Exception:
                    # Single input
                    for sel in otp_input_selectors:
                        try:
                            otp_el = page.locator(sel).first
                            if otp_el.is_visible(timeout=2000):
                                _log(f"  Filling single OTP input: {sel}")
                                otp_el.click(force=True)
                                otp_el.fill(otp)
                                page.wait_for_timeout(500)
                                for s in ["button[type='submit']", "button:has-text('Continue')",
                                           "button:has-text('Verify')"]:
                                    try:
                                        btn = page.locator(s).first
                                        if btn.is_visible(timeout=1000):
                                            btn.click(force=True)
                                            _log(f"  OTP submitted via {s}")
                                            break
                                    except Exception:
                                        continue
                                break
                        except Exception:
                            continue

                page.wait_for_timeout(3000)

            elif page_type == "password":
                _log("  >>> UNEXPECTED: Password page detected — this is the sign-up route, not the log-in route")
                _save_screenshot(page, f"unexpected_password_round{rd + 1}")
                _log("  [!] OpenAI redirected to create-account/password instead of OTP.")

                # ── Recovery: restart browser with fresh Chrome profile ──
                # The password page means OpenAI routed this session to the
                # sign-up flow instead of log-in.  A fresh browser session
                # often gets the correct OTP flow.  Runs automatically even
                # in --interactive mode (same as modal-not-opening recovery).
                if _restart_count < _RESTART_LIMIT:
                    _restart_count += 1
                    _log(f"  [restart] Attempting browser restart {_restart_count}/{_RESTART_LIMIT} "
                         f"to get OTP flow instead of password...")
                    try:
                        _restart_browser()
                        _prev_url = ""  # skip the 20s URL wait at loop top
                        _log("  [restart] Browser restarted — re-entering state machine loop")
                        continue
                    except Exception as e:
                        _log(f"  [restart] Browser restart failed: {e}")

                # Restart failed or limit reached — abandon this session
                _log("  [!] Abandoning session — batch retry will use a fresh email + browser.")
                return {
                    "success": False, "stage": "unexpected_password_page",
                    "email": email, "password": password,
                    "error_message": (
                        "Landed on create-account/password — unexpected sign-up route. "
                        "OpenAI did not offer the email-OTP log-in flow for this session."
                    ),
                    "logs": logs,
                }

            elif page_type == "about_you":
                _log("  >>> About You page detected")
                # Derive a human name from the email local part so it looks
                # related (e.g. john.smith@... → "John Smith") instead of a
                # completely random name.
                user_info = generate_random_user_info()
                name = _email_derive_name(email)
                # Compute age from birthdate for the new "How old are you?" page
                # that asks for age (number) instead of birthdate.
                from datetime import date as _date
                birth_parts = user_info["birthdate"].split("-")
                birth_date = _date(int(birth_parts[0]), int(birth_parts[1]), int(birth_parts[2]))
                today = _date.today()
                age = today.year - birth_date.year - (
                    (today.month, today.day) < (birth_date.month, birth_date.day)
                )
                _log(f"  Name (from email): {name}, age={age}, birthdate={user_info['birthdate']}")
                about_name = name
                about_birthdate = user_info["birthdate"]
                about_age = age

                # Name input — try modern selectors first, then legacy
                name_selectors = [
                    "input[name='name']",
                    "#aboutYouName",
                    "input[placeholder*='Full name' i]",
                    "input[placeholder*='name' i]",
                    "input[autocomplete='name']",
                ]
                for ns in name_selectors:
                    try:
                        name_el = page.locator(ns).first
                        if name_el.is_visible(timeout=2000):
                            _type_human(name_el, name, bridge=None, selector=None)
                            _log(f"  Name filled: {name}")
                            break
                    except Exception:
                        continue

                page.wait_for_timeout(500)

                # Age / birthdate — handle THREE variants:
                #   A) Age UI:     input[name='age'] type=number  (just the age)
                #   B) Birthday UI (single):  Single input expecting DD/MM/YYYY
                #   C) Birthday UI (split):   Separate day/month/year selects/inputs
                #
                #   Detection order: scan page for birthday indicators first;
                #   if none found, assume the common Age variant.

                # ── Prepare DD/MM/YYYY format for birthday variants ──
                birth_day_str   = birth_parts[2]               # "17"
                birth_month_str = birth_parts[1]               # "05"
                birth_year_str  = birth_parts[0]               # "1986"
                dd_mm_yyyy      = f"{birth_day_str}/{birth_month_str}/{birth_year_str}"

                # ── Detect birthday variant ──
                birthday_mode = False
                # Check for birthday-specific DOM elements.
                # NOTE: ChatGPT uses a React Aria segmented date field for
                # birthday — NOT a plain <input>.  The visible parts are
                # contenteditable <div role="spinbutton"> elements with
                # data-type="month" / "day" / "year".  The companion
                # <input name="birthday"> is always type="hidden" so we
                # rely on the spinbutton segments for detection.
                birthday_indicators = [
                    # React Aria segmented date field (ChatGPT birthday UI)
                    "[data-type='month'][role='spinbutton']",
                    "[data-type='day'][role='spinbutton']",
                    # Traditional <input>-based birthday fields
                    "input[placeholder*='DD/MM' i]",
                    "input[placeholder*='dd/mm' i]",
                    "input[placeholder*='Birthday' i]",
                    "input[name*='birth' i]",
                    "select[name*='month' i]",
                    "select[name*='day' i]",
                    "input[name*='day' i]",
                    "input[name*='month' i]",
                    "input[name*='year' i]",
                ]
                for ind in birthday_indicators:
                    try:
                        if page.locator(ind).first.is_visible(timeout=400):
                            birthday_mode = True
                            _log(f"  Birthday variant detected via selector: {ind}")
                            break
                    except Exception:
                        continue

                # Also check page body text for "Birthday" (but not when "Age" is present)
                if not birthday_mode:
                    try:
                        body_text = (page.locator("body").text_content() or "")
                        if "birthday" in body_text.lower() and "how old" not in body_text.lower():
                            birthday_mode = True
                            _log("  Birthday variant detected from page text")
                    except Exception:
                        pass

                age_filled = False

                if birthday_mode:
                    # ── BIRTHDAY PATH ──
                    # Try React Aria segmented date field first (ChatGPT's
                    # native birthday UI), then separate selects/inputs,
                    # then a single DD/MM/YYYY input.
                    # See email-reg/docs/birthday.md for reference DOM.

                    # --- React Aria segmented date field (div spinbuttons) ---
                    if not age_filled:
                        try:
                            month_seg = page.locator(
                                "[data-type='month'][role='spinbutton']"
                            ).first
                            if month_seg.is_visible(timeout=800):
                                _log("  React Aria segmented date field detected")
                                day_seg = page.locator(
                                    "[data-type='day'][role='spinbutton']"
                                ).first
                                year_seg = page.locator(
                                    "[data-type='year'][role='spinbutton']"
                                ).first

                                # Fill month segment (2 digits → auto-advance)
                                month_seg.click(force=True)
                                page.wait_for_timeout(150)
                                month_seg.type(birth_month_str,
                                               delay=random.randint(30, 80))
                                _log(f"  Month segment: {birth_month_str}")

                                # Day segment
                                page.wait_for_timeout(200)
                                day_seg.click(force=True)
                                page.wait_for_timeout(100)
                                day_seg.type(birth_day_str,
                                             delay=random.randint(30, 80))
                                _log(f"  Day segment: {birth_day_str}")

                                # Year segment
                                page.wait_for_timeout(200)
                                year_seg.click(force=True)
                                page.wait_for_timeout(100)
                                year_seg.type(birth_year_str,
                                              delay=random.randint(30, 80))
                                _log(f"  Year segment: {birth_year_str}")

                                age_filled = True
                                _log(f"  Birthday filled (React Aria): "
                                     f"{dd_mm_yyyy}")
                        except Exception as e:
                            _log(f"  [!] React Aria segment fill failed: {e}")

                    # --- Separate day/month/year selects/inputs ---
                    day_selectors = [
                        "select[name*='day' i]",
                        "input[name*='day' i]",
                        "input[placeholder*='day' i]",
                    ]
                    month_selectors = [
                        "select[name*='month' i]",
                        "input[name*='month' i]",
                        "input[placeholder*='month' i]",
                    ]
                    year_selectors = [
                        "select[name*='year' i]",
                        "input[name*='year' i]",
                        "input[placeholder*='year' i]",
                    ]

                    day_el = month_el = year_el = None
                    for ds in day_selectors:
                        try:
                            d = page.locator(ds).first
                            if d.is_visible(timeout=800):
                                day_el = d
                                break
                        except Exception:
                            continue
                    for ms in month_selectors:
                        try:
                            m = page.locator(ms).first
                            if m.is_visible(timeout=800):
                                month_el = m
                                break
                        except Exception:
                            continue
                    for ys in year_selectors:
                        try:
                            y = page.locator(ys).first
                            if y.is_visible(timeout=800):
                                year_el = y
                                break
                        except Exception:
                            continue

                    if month_el:
                        # Variant C: separate day/month/year fields
                        _log(f"  Filling split birthday: day={birth_day_str}, month={birth_month_str}, year={birth_year_str}")
                        try:
                            tag = (month_el.evaluate("el => el.tagName.toLowerCase()") or "")
                            if tag == "select":
                                # <select> — try zero-padded value first, then bare int
                                try:
                                    month_el.select_option(birth_month_str)
                                except Exception:
                                    month_el.select_option(str(int(birth_month_str)))
                            else:
                                month_el.click(force=True)
                                month_el.fill(birth_month_str)
                            _log(f"  Month filled: {birth_month_str}")
                        except Exception as e:
                            _log(f"  [!] Month fill failed: {e}")

                        if day_el:
                            try:
                                tag = (day_el.evaluate("el => el.tagName.toLowerCase()") or "")
                                if tag == "select":
                                    try:
                                        day_el.select_option(birth_day_str)
                                    except Exception:
                                        day_el.select_option(str(int(birth_day_str)))
                                else:
                                    day_el.click(force=True)
                                    day_el.fill(birth_day_str)
                                _log(f"  Day filled: {birth_day_str}")
                            except Exception as e:
                                _log(f"  [!] Day fill failed: {e}")

                        if year_el:
                            try:
                                tag = (year_el.evaluate("el => el.tagName.toLowerCase()") or "")
                                if tag == "select":
                                    year_el.select_option(birth_year_str)
                                else:
                                    year_el.click(force=True)
                                    year_el.fill(birth_year_str)
                                _log(f"  Year filled: {birth_year_str}")
                            except Exception as e:
                                _log(f"  [!] Year fill failed: {e}")

                        age_filled = True
                    else:
                        # Variant B: single masked birthday input (__/__/____).
                        # The input has built-in / separators — we type only the
                        # 8 digits; the mask auto-inserts the slashes as you type.
                        birth_single_selectors = [
                            "input[placeholder*='DD/MM' i]",
                            "input[placeholder*='dd/mm' i]",
                            "input[placeholder*='Birthday' i]",
                            "input[placeholder*='birth' i]",
                            "input[name*='birth' i]",
                            "input[type='date']",
                        ]
                        digits_only = birth_day_str + birth_month_str + birth_year_str  # "DDMMYYYY"
                        for bs in birth_single_selectors:
                            try:
                                birth_el = page.locator(bs).first
                                if birth_el.is_visible(timeout=2000):
                                    # type=date expects YYYY-MM-DD set via value attribute
                                    input_type = (birth_el.get_attribute("type") or "").lower()
                                    if input_type == "date":
                                        birth_el.click(force=True)
                                        birth_el.fill(user_info["birthdate"])
                                        _log(f"  Birthdate filled (type=date): {user_info['birthdate']}")
                                    else:
                                        # Masked text input: click → clear → type digits only.
                                        # The built-in / separators advance the cursor automatically.
                                        birth_el.click(force=True)
                                        page.wait_for_timeout(100)
                                        birth_el.click(force=True, click_count=3)
                                        birth_el.press("Backspace")
                                        page.wait_for_timeout(100)
                                        birth_el.type(digits_only, delay=random.randint(50, 150))
                                        _log(f"  Birthday digits typed: {digits_only} → shows as {dd_mm_yyyy}")
                                    age_filled = True
                                    break
                            except Exception:
                                continue

                if not age_filled:
                    # ── AGE PATH (default / fallback) ──
                    # Try the number-input Age variant (most common UI).
                    age_selectors = [
                        "input[name='age']",
                        "input[type='number']",
                    ]
                    for ags in age_selectors:
                        try:
                            age_el = page.locator(ags).first
                            if age_el.is_visible(timeout=2000):
                                age_el.click(force=True)
                                age_el.fill(str(age))
                                _log(f"  Age filled: {age}")
                                age_filled = True
                                break
                        except Exception:
                            continue

                if not age_filled:
                    # Last-resort fallback: old-style single birthdate UI
                    birth_selectors = [
                        "input[name*='birth' i]",
                        "select[name*='month' i]",
                        "input[placeholder*='birth' i]",
                        "input[type='date']",
                    ]
                    for bs in birth_selectors:
                        try:
                            birth_el = page.locator(bs).first
                            if birth_el.is_visible(timeout=2000):
                                birth_el.click(force=True)
                                birth_el.fill(user_info["birthdate"])
                                _log(f"  Birthdate filled (legacy): {user_info['birthdate']}")
                                age_filled = True
                                break
                        except Exception:
                            continue

                if not age_filled:
                    _log("  [!] Could not fill age or birthdate — no matching field found")
                    _save_screenshot(page, "about_you_no_field")
                    # If birthday mode was detected but no selectors matched,
                    # pause for manual DOM inspection (only when --interactive)
                    if birthday_mode:
                        _log("  [BIRTHDAY] Detected but input not found — pausing for inspection")
                        _save_screenshot(page, "birthday_input_not_found")
                        _prompt_user("Birthday field detected but input not found — inspect DOM and press Enter", interactive=interactive)

                    # ── Page-state matcher fallback ─────────────────────
                    # The form fields may have disappeared because the page
                    # is showing "Oops, an error occurred!" (400) with a
                    # "Try again" button.  Only runs when normal field
                    # detection has already failed — zero overhead on the
                    # happy path.
                    if matcher is not None:
                        _log("  Trying page-state matcher to identify error page...")
                        recovery = matcher.identify_and_recover(page)
                        if recovery["matched"]:
                            _log(f"  Matcher identified: {recovery['template_id']} "
                                 f"(score={recovery['score']:.2f}, "
                                 f"recovered={recovery['recovered']}, "
                                 f"resume_strategy={recovery['resume_strategy']})")
                            if recovery["recovered"]:
                                _log("  Error page recovery clicked — re-entering state loop")
                                _prev_url = ""
                                continue

                    # ── Welcome / getting-started modal fallback ─────────
                    # The "about_you" detection may be a false match — the
                    # page could actually be showing the "Tips for getting
                    # started" onboarding modal (which appears AFTER the
                    # about-you form is submitted).  Try dismissing it.
                    if not age_filled:
                        _log("  Fields not found — trying to dismiss any welcome/getting-started modal...")
                        if _dismiss_welcome_modal(page, bridge=bridge):
                            _log("  Modal dismissed — re-entering state loop")
                            _prev_url = ""
                            continue

                page.wait_for_timeout(500)

                # Submit about_you
                for s in ["button[type='submit']", "button:has-text('Finish creating account')",
                           "button:has-text('Continue')", "button:has-text('Next')"]:
                    try:
                        btn = page.locator(s).first
                        if btn.is_visible(timeout=1000):
                            text = (btn.text_content() or "").strip()[:50]
                            _log(f"  About You submitted via '{text}' ({s})")
                            btn.click(force=True)
                            break
                    except Exception:
                        continue

                page.wait_for_timeout(3000)

                # ── Birthday confirmation dialog ──────────────────────────
                # When age is submitted (instead of birthday), ChatGPT shows
                # a confirmation dialog: "You're setting your birthday to
                # <date>. This is just for our records..." with OK / Cancel.
                # Click OK to dismiss it so navigation can proceed.
                _save_screenshot(page, f"about_you_post_submit_round{rd + 1}")
                try:
                    body = page.locator("body").inner_text(timeout=2000)
                    if ("you're setting your birthday" in body.lower()
                            or "setting your birthday to" in body.lower()):
                        _log("  Birthday confirmation dialog detected — clicking OK")
                        _save_screenshot(page, f"birthday_confirm_round{rd + 1}")
                        for ok_sel in [
                            "div[role='dialog'] button[type='submit']",
                            "[role='dialog'] button[type='submit']",
                            "button:has-text('OK')",
                            "button[type='submit']:has-text('OK')",
                            "[role='dialog'] button:has-text('OK')",
                        ]:
                            try:
                                ok_btn = page.locator(ok_sel).first
                                if ok_btn.is_visible(timeout=1000):
                                    ok_btn.click(force=True)
                                    _log(f"  Clicked OK via {ok_sel}")
                                    page.wait_for_timeout(2000)
                                    break
                            except Exception:
                                continue
                except Exception:
                    pass

            elif page_type == "getting_started":
                _log("  >>> Getting Started onboarding modal — clicking 'Okay, let's go'...")
                _save_screenshot(page, f"getting_started_round{rd + 1}")
                clicked_gs = False
                for gs_sel in [
                    "[data-testid='getting-started-button']",
                    "button:has-text('Okay, let's go')",
                    "button:has-text(\"Let's go\")",
                    "div[role='dialog'] button:has-text('go')",
                ]:
                    try:
                        btn = page.locator(gs_sel).first
                        if btn.is_visible(timeout=2000):
                            text = (btn.text_content() or "").strip()[:50]
                            _log(f"  Clicking: '{text}' ({gs_sel})")
                            btn.click(force=True)
                            page.wait_for_timeout(3000)
                            clicked_gs = True
                            break
                    except Exception:
                        continue

                if not clicked_gs:
                    # Fallback: try Escape key to dismiss the dialog
                    _log("  Trying Escape key to dismiss...")
                    try:
                        page.keyboard.press("Escape")
                        page.wait_for_timeout(2000)
                    except Exception:
                        pass

                _prev_url = page.url
                continue

            elif page_type == "tour_welcome":
                _log("  >>> Tour welcome overlay detected — clicking 'Skip Tour'...")
                _save_screenshot(page, f"tour_welcome_round{rd + 1}")
                if _dismiss_tour_overlay(page, bridge=bridge):
                    _log("  Tour dismissed via Skip Tour button")
                else:
                    _log("  [!] Skip Tour button not found — trying Escape key...")
                    try:
                        page.keyboard.press("Escape")
                        page.wait_for_timeout(2000)
                    except Exception:
                        pass
                    # Last resort: click "Next" button to advance the tour
                    # (may close after the last step)
                    _log("  Trying 'Next' button as fallback...")
                    try:
                        next_btn = page.locator("button:has-text('Next')").first
                        if next_btn.is_visible(timeout=1000):
                            next_btn.click(force=True)
                            page.wait_for_timeout(2000)
                            _log("  Clicked 'Next' — checking if tour closed...")
                            # Try Skip Tour again after advancing
                            _dismiss_tour_overlay(page, bridge=bridge)
                    except Exception:
                        pass
                page.wait_for_timeout(2000)
                _prev_url = page.url
                continue

            elif page_type == "cf_challenge":
                # This handler fires when the early CF block didn't catch the
                # challenge, or when CF returned after the early block solved it.
                _cf_streak += 1
                _log(f"  >>> CF Challenge #{_cf_streak}/{_CF_STREAK_LIMIT} — solving...")
                if _cf_streak > _CF_STREAK_LIMIT:
                    _log(f"  [!] CF death loop: {_cf_streak} consecutive challenges")
                    _save_screenshot(page, f"cf_death_loop_round{rd + 1}")
                    # ── Recovery: restart browser with fresh Chrome profile ──
                    if _restart_count < _RESTART_LIMIT:
                        _restart_count += 1
                        _log(f"  [restart] Attempting browser restart {_restart_count}/{_RESTART_LIMIT} "
                             f"(CF death loop — fresh Chrome profile may bypass)...")
                        try:
                            _restart_browser()
                            _cf_streak = 0
                            _prev_url = ""
                            _log("  [restart] Browser restarted — re-entering state machine loop")
                            continue
                        except Exception as e:
                            _log(f"  [restart] Browser restart failed: {e}")
                    # Restart failed or limit reached — abandon this session
                    _log(f"  [!] Browser restart limit ({_RESTART_LIMIT}) exhausted — abandoning session")
                    try:
                        page.context.clear_cookies()
                        page.evaluate("localStorage.clear(); sessionStorage.clear();")
                    except Exception:
                        pass
                    return {
                        "success": False, "stage": "cf_death_loop",
                        "email": email, "password": password,
                        "error_message": f"CF death loop: {_cf_streak} consecutive challenges without progress after {_RESTART_LIMIT} browser restarts",
                        "logs": logs,
                    }
                # Exponential backoff: 0s (1st), 3s (2nd), 6s (3rd)
                if _cf_streak >= 2:
                    backoff = 3 * (2 ** (_cf_streak - 2))
                    _log(f"  CF backoff: waiting {backoff}s before retry...")
                    page.wait_for_timeout(backoff * 1000)
                _wait_for_element_after_cf(
                    page,
                    target_selectors=[
                        "input[inputmode='numeric']",
                        "button:has-text('Continue')",
                    ],
                    timeout=120,
                    force_cf=True,   # page is already showing CF — skip network detection
                )
                page.wait_for_timeout(2000)
                page_type = _detect_page_type(page)
                if page_type != "cf_challenge":
                    _cf_streak = 0  # CF solved, page moved on

            elif page_type == "login":
                # Landed on chatgpt.com homepage with "Log in" button visible.
                # This can happen after a browser restart.  Click "Log in"
                # and let the next round handle the resulting modal/auth page.
                _log("  >>> On chatgpt.com homepage ('Log in' visible) — clicking 'Log in'...")
                clicked, _ = _find_and_click(page, [
                    "[data-testid='login-button']",
                    "button:has-text('Log in')",
                    "a:has-text('Log in')",
                ], label="'Log in' button (state machine)", bridge=bridge)
                if clicked:
                    page.wait_for_timeout(3000)
                    _prev_url = page.url
                else:
                    _log("  [!] Could not click 'Log in' — will retry next round")
                continue

            elif page_type in ("auth_page", "email_input"):
                _email_streak += 1

                # ── Stuck detection ─────────────────────────────────────
                # When we've been on email_input/auth_page for multiple
                # consecutive rounds without advancing to OTP/password,
                # restart the browser with a fresh Chrome profile instead
                # of wasting 20 s per round for all 10 rounds.
                if _email_streak >= 2:
                    _log(f"  [!] Email input stuck ({_email_streak} consecutive rounds "
                         f"on {page_type}) — attempting browser restart...")
                    _save_screenshot(page, f"email_stuck_streak{_email_streak}_round{rd + 1}")
                    if _restart_count < _RESTART_LIMIT:
                        _restart_count += 1
                        _log(f"  [restart] Attempting browser restart "
                             f"{_restart_count}/{_RESTART_LIMIT} ...")
                        try:
                            _restart_browser()
                            _email_streak = 0
                            _prev_url = ""
                            otp_before_ids = None
                            _log("  [restart] Browser restarted — "
                                 "re-entering state machine loop")
                            continue
                        except Exception as _e:
                            _log(f"  [restart] Browser restart failed: {_e}")
                            import traceback
                            traceback.print_exc()
                    if _restart_count >= _RESTART_LIMIT:
                        _log(f"  [!] Browser restart limit ({_RESTART_LIMIT}) "
                             f"reached — abandoning session")
                        _save_screenshot(page, f"email_stuck_exhausted_round{rd + 1}")
                        return {
                            "success": False, "stage": "email_stuck_exhausted",
                            "email": email, "password": password,
                            "error_message": (
                                f"Email input stuck after {_RESTART_LIMIT} "
                                f"browser restarts — account may be blocked "
                                f"or email rejected by OpenAI"
                            ),
                            "logs": logs,
                        }

                # ── Login modal on chatgpt.com (email input visible) ────
                # This happens when the initial email submission failed
                # silently (server rejected, React state didn't sync, etc.)
                # and we're still on chatgpt.com with the login dialog open.
                # Re-fill the email and click Continue to retry.
                if (page_type == "email_input"
                        and "chatgpt.com" in page.url.lower()
                        and "auth" not in page.url.lower()):
                    _log("  >>> Email input on chatgpt.com login modal — re-filling and submitting...")
                    _save_screenshot(page, f"email_retry_round{rd + 1}")
                    # Re-find email input
                    for sel in [
                        "input[type='email']",
                        "#emailInput",
                        "#phoneNumberInput",
                    ]:
                        try:
                            el = page.locator(sel).first
                            if el.is_visible(timeout=2000):
                                el.click(force=True)
                                page.wait_for_timeout(100)
                                el.click(force=True, click_count=3)
                                el.press("Backspace")
                                page.wait_for_timeout(100)
                                el.fill(email)
                                page.wait_for_timeout(300)
                                val = el.input_value()
                                _log(f"  Re-filled email: '{val}'")
                                break
                        except Exception:
                            continue

                    # Re-snapshot mailbox before clicking Continue
                    if mailbox is not None:
                        fake_account = MailboxAccount(email=email, account_id=email)
                        otp_before_ids = mailbox.get_current_ids(fake_account)
                        _log(f"  Mailbox re-snapshot: {len(otp_before_ids)} existing emails")

                    # Click Continue
                    clicked_submit = False
                    for sel in [
                        "button[type='submit']",
                        "button:has-text('Continue')",
                        "button:has-text('Next')",
                    ]:
                        try:
                            btn = page.locator(sel).first
                            if btn.is_visible(timeout=1000):
                                text = (btn.text_content() or "").strip()[:50]
                                _log(f"  Clicking: '{text}' ({sel})")
                                btn.click(force=True)
                                clicked_submit = True
                                break
                        except Exception:
                            continue
                    if not clicked_submit:
                        _log("  [!] Continue button not found — trying Enter key...")
                        page.keyboard.press("Enter")
                    page.wait_for_timeout(2000)
                    _prev_url = page.url
                    continue

                # ── Detect "stuck after email submit" ──────────────────
                # If we're on email_input AND the URL already contains
                # ?email=, it means OpenAI didn't accept the email (the
                # page didn't advance to OTP/password).  The browser
                # session is likely tainted — restart with a fresh Chrome
                # profile so we get clean cookies + localStorage.
                if page_type == "email_input" and "?email=" in page.url.lower():
                    if _restart_count < _RESTART_LIMIT:
                        _restart_count += 1
                        _log(f"  >>> Email submission stuck (URL={page.url[:120]}) — "
                             f"restarting browser session ({_restart_count}/{_RESTART_LIMIT})...")
                        _save_screenshot(page, f"email_stuck_round{rd + 1}_restart{_restart_count}")
                        try:
                            _restart_browser()
                        except Exception as e:
                            _log(f"  [!] Browser restart failed: {e}")
                            import traceback
                            traceback.print_exc()
                            return {
                                "success": False, "stage": "browser_restart_failed",
                                "email": email, "password": password,
                                "error_message": f"Browser restart failed: {e}",
                                "logs": logs,
                            }
                        # Reset OTP snapshot — we'll re-snapshot when we
                        # re-submit the email in the next round.
                        otp_before_ids = None
                        # After restart we're back on chatgpt.com (or auth
                        # page).  Continue the state machine — it will
                        # detect and handle whatever page we landed on.
                        continue
                    else:
                        _log(f"  [!] Browser restart limit ({_RESTART_LIMIT}) reached — "
                             f"abandoning session")
                        _save_screenshot(page, f"email_stuck_exhausted_round{rd + 1}")
                        return {
                            "success": False, "stage": "email_stuck_exhausted",
                            "email": email, "password": password,
                            "error_message": (
                                f"Email input stuck after {_RESTART_LIMIT} browser "
                                f"restarts — email may be blocked or already registered"
                            ),
                            "logs": logs,
                        }

                # auth.openai.com page — may be transitioning or showing consent/redirect
                _log(f"  >>> On auth.openai.com ({page_type}) — waiting for redirect or content...")
                # Try clicking any visible Continue/Allow/Authorize button.
                # IMPORTANT: exclude social-login buttons (Google, Microsoft,
                # Apple) — they appear on the same auth page and would
                # derail the email flow.
                consent_selectors = [
                    "button[type='submit']:not(:has-text('Google')):not(:has-text('Microsoft')):not(:has-text('Apple'))",
                    "button:has-text('Continue'):not(:has-text('Google')):not(:has-text('Microsoft')):not(:has-text('Apple'))",
                    "button:has-text('Allow')",
                    "button:has-text('Authorize')",
                    "button[type='submit']",
                ]
                clicked_any = False
                for s in consent_selectors:
                    try:
                        btn = page.locator(s).first
                        if btn.is_visible(timeout=2000):
                            text = (btn.text_content() or "").strip()[:60]
                            # Double-check: skip if the matched button is a
                            # social-login button (belt-and-suspenders with
                            # the :not() selectors above).
                            if any(kw in text.lower() for kw in
                                   ("google", "microsoft", "apple", "phone",
                                    "qr code", "single sign-on", "sso")):
                                _log(f"  Skipping social/alt login btn: '{text}' ({s})")
                                continue
                            _log(f"  Clicking consent btn: '{text}' ({s})")
                            btn.click(force=True)
                            page.wait_for_timeout(2000)
                            clicked_any = True
                            break
                    except Exception:
                        continue
                if not clicked_any:
                    # ── Page-state matcher fallback ─────────────────────
                    # auth/error?error=undefined and similar error pages
                    # show no consent buttons.  Try the text-based matcher
                    # before falling back to a blind redirect wait.
                    recovered_by_matcher = False
                    if matcher is not None:
                        _log("  Trying page-state matcher to identify auth page...")
                        recovery = matcher.identify_and_recover(page)
                        if recovery["matched"]:
                            _log(f"  Matcher identified: {recovery['template_id']} "
                                 f"(score={recovery['score']:.2f}, "
                                 f"recovered={recovery['recovered']}, "
                                 f"resume_strategy={recovery['resume_strategy']})")
                            if recovery["recovered"]:
                                _log("  Matcher recovered — re-entering state loop")
                                recovered_by_matcher = True
                                continue  # back to state machine

                    if not recovered_by_matcher:
                        # Maybe the page is still loading — wait for redirect
                        _log("  No consent buttons found — waiting for navigation...")
                        try:
                            page.wait_for_url("**/chatgpt.com/**", timeout=15000)
                            _log(f"  Redirected to chatgpt.com: {page.url[:150]}")
                        except Exception:
                            _log(f"  Still on: {page.url[:150]}")
                        page.wait_for_timeout(2000)

            else:
                # Unknown page — try text-based matcher fallback first, then prompt user
                _log(f"  >>> Unknown page type '{page_type}'")
                _save_screenshot(page, f"unknown_round{rd + 1}")

                # Layer 1: text-based page-state identification + recovery
                recovered_by_matcher = False
                if matcher is not None:
                    _log("  Trying page-state matcher to identify page state...")
                    recovery = matcher.identify_and_recover(page)
                    if recovery["matched"]:
                        _log(f"  Matcher identified: {recovery['template_id']} "
                             f"(score={recovery['score']:.2f}, "
                             f"recovered={recovery['recovered']}, "
                             f"resume_strategy={recovery['resume_strategy']})")
                        if recovery["recovered"]:
                            _log("  Matcher recovered — re-entering state loop")
                            recovered_by_matcher = True
                            # Re-detect page type after recovery
                            page.wait_for_timeout(2000)
                            page_type = _detect_page_type(page)
                            _log(f"  Page type after matcher recovery: {page_type}")
                            if page_type in ("chatgpt_home", "otp", "password", "about_you"):
                                continue  # back to state machine
                    else:
                        _log("  Matcher could not identify page state")

                if recovered_by_matcher:
                    continue

                # Layer 2: interactive prompt (existing fallback)
                _prompt_user(f"Unknown page type '{page_type}' at {page.url[:120]}", interactive=interactive)
                # Re-check after user intervention
                current = page.url.lower()
                if "chatgpt.com" in current and "auth" not in current:
                    _log("  Landed on chatgpt.com after manual intervention!")
                    break
                # Try clicking any Continue/Next as fallback
                for s in ["button:has-text('Continue')", "button:has-text('Next')",
                           "button[type='submit']"]:
                    try:
                        btn = page.locator(s).first
                        if btn.is_visible(timeout=1000):
                            btn.click(force=True)
                            page.wait_for_timeout(3000)
                            _log(f"  Fallback click on '{s}'")
                            break
                    except Exception:
                        continue

        else:
            _log(f"  [!] Max rounds ({MAX_ROUNDS}) reached without reaching chatgpt.com")
            _save_screenshot(page, "max_rounds_reached")
            _prompt_user("Max rounds reached — browser is open for manual inspection", interactive=interactive)

        # ── 9. Handle "You're all set" welcome modal ──
        # After first-time registration, chatgpt.com shows a full-screen
        # onboarding modal that must be dismissed.
        #
        # Three-layer defence against timing races (the modal is an async
        # React component that may render after we land on the page):
        #   1. Fast scan — check known selectors immediately.
        #   2. Slow scan — wait 3 s for late render, check again.
        #   3. Blind click — try dismissing anyway ("click first, no scanning"
        #      per the discussion in docs/plans/page-state-image-matcher-plan.md
        #      and the text-based fallback module in page_state_matcher.py).
        _log("[Step 9] Checking for 'You're all set' welcome modal...")
        welcome_selectors = [
            "[aria-label=\"You're all set\"]",
            "[aria-label*='all set' i]",
            "div[role='dialog']:has-text('all set')",
        ]

        dismissed = False
        for scan_pass in ("fast", "slow"):
            for modal_sel in welcome_selectors:
                try:
                    modal = page.locator(modal_sel).first
                    if modal.is_visible(timeout=2000):
                        _log(f"  Welcome modal detected ({scan_pass} scan): {modal_sel}")
                        _save_screenshot(page, "welcome_modal", level="debug")
                        dismissed = _dismiss_welcome_modal(page, bridge=bridge)
                        break
                except Exception:
                    continue
            if dismissed:
                break
            if scan_pass == "fast":
                _log("  First scan negative — waiting 3s for late render...")
                page.wait_for_timeout(3000)

        # Layer 3: blind attempt (handles the case where the modal exists
        # but none of our selectors match it).
        if not dismissed:
            _log("  Scans negative — trying blind click to dismiss any modal...")
            dismissed = _dismiss_welcome_modal(page, bridge=bridge)

        # Layer 4: tour overlay dismissal (new "Ask anything" / "Skip Tour"
        # onboarding tour that appears on chatgpt.com/c/... after registration.
        # Not a role="dialog" — targets the "Skip Tour" button directly.)
        if not dismissed:
            _log("  Trying tour overlay dismissal ('Skip Tour')...")
            dismissed = _dismiss_tour_overlay(page, bridge=bridge)

        if not dismissed:
            _log("  No welcome modal or tour overlay found (or already dismissed).")

        # ── 10. Final snapshot + ensure we're on chatgpt.com ──
        if recon_enabled:
            _recon_page_snapshot(page, "BEFORE SESSION FETCH", indent=0)

        # Ensure we're on chatgpt.com
        if "chatgpt.com" not in page.url.lower():
            _log("[Step 10] Navigating to chatgpt.com for session fetch...")
            try:
                page.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=15000)
            except Exception:
                pass
            page.wait_for_timeout(3000)

        # ── 10b. Check for tour overlay after navigation ────────────────
        # The "Ask anything" / "Skip Tour" onboarding tour can render
        # asynchronously after landing on chatgpt.com.  Dismiss it now
        # so Step 11 (login-state verification) sees a clean page.
        _log("[Step 10b] Checking for tour overlay after navigation...")
        _dismiss_tour_overlay(page, bridge=bridge)

        if recon_enabled:
            _recon_page_snapshot(page, "ON chatgpt.com BEFORE SESSION", indent=0)

        # Verify we appear logged in before fetching session
        _log("[Step 11] Verifying login state...")
        login_state_failed = False
        try:
            body_text = page.locator("body").inner_text(timeout=2000)
            has_log_in_btn = _login_entry_visible(page, body_text)
        except Exception:
            has_log_in_btn = False
        if has_log_in_btn:
            login_state_failed = True
            _log("  [!] Login entry still visible — not logged in!")
            _save_screenshot(page, "not_logged_in")
            _prompt_user("Not logged in — 'Log in' button still visible on chatgpt.com", interactive=interactive)

        # ── 11. Fetch /api/auth/session ──
        _log("[Step 12] Fetching /api/auth/session...")
        session_data = {}
        session_meta = {}
        try:
            session_result = page.evaluate("""() => {
                return fetch('/api/auth/session', { credentials: 'include' })
                    .then(async r => {
                        const text = await r.text();
                        let payload = {};
                        try { payload = JSON.parse(text); } catch (e) { payload = {error: 'invalid_json'}; }
                        return {
                            payload,
                            meta: {
                                status: r.status,
                                content_type: r.headers.get('content-type') || '',
                                body_length: text.length,
                                keys: payload && typeof payload === 'object' ? Object.keys(payload).sort() : [],
                            },
                        };
                    })
                    .catch(e => ({payload: {error: e.message}, meta: {status: 0, keys: []}}));
            }""")

            if isinstance(session_result, dict) and "payload" in session_result:
                session_data = session_result.get("payload") or {}
                session_meta = session_result.get("meta") or {}
            else:
                session_data = session_result or {}
            _log(f"  /api/auth/session metadata: {json.dumps(session_meta, ensure_ascii=False, sort_keys=True)}")

            if session_data.get("accessToken"):
                at = session_data["accessToken"]
                _log(f"  accessToken:  {at[:50]}...{at[-20:]}")
            else:
                _log("  [!] No accessToken in session response")
                _save_screenshot(page, "session_no_access_token")
                _prompt_user("No accessToken — browser is open for manual inspection", interactive=interactive)

            if session_data.get("sessionToken"):
                st = session_data["sessionToken"]
                _log(f"  sessionToken: {st[:50]}...{st[-10:]}")

            if session_data.get("user"):
                u = session_data["user"]
                _log(f"  user.id:      {u.get('id', 'N/A')}")
                _log(f"  user.email:   {u.get('email', 'N/A')}")
                _log(f"  user.name:    {u.get('name', 'N/A')}")

        except Exception as e:
            _log(f"  [!] Session fetch failed: {e}")
            _save_screenshot(page, "session_fetch_failed")
            _prompt_user("Session fetch failed — browser is open for manual inspection", interactive=interactive)

        # ── 11. Extract claims from JWT (same level as chatgpt_login.py) ──
        account_id = ""
        jwt_sub = ""
        jwt_iat = ""
        jwt_exp = ""
        jwt_auth_time = ""
        jwt_plan_type = ""
        jwt_chatgpt_user_id = ""
        jwt_user_id = ""
        jwt_profile_email = ""
        at = session_data.get("accessToken", "")
        if at:
            try:
                segs = at.split(".")
                if len(segs) >= 2:
                    raw = segs[1]
                    raw += "=" * (4 - len(raw) % 4) if len(raw) % 4 else ""
                    jwt_payload = json.loads(base64.b64decode(raw))
                    auth_section = jwt_payload.get("https://api.openai.com/auth", {})
                    account_id = str(
                        auth_section.get("chatgpt_account_id")
                        or auth_section.get("user_id")
                        or ""
                    ).strip()
                    _log(f"  JWT account_id: {account_id}")

                    # Extract JWT time claims (iat/exp/auth_time)
                    def _ts_to_iso(ts):
                        if not ts:
                            return ""
                        return datetime.fromtimestamp(ts, tz=timezone.utc).isoformat()

                    jwt_sub = str(jwt_payload.get("sub", ""))
                    jwt_iat = _ts_to_iso(jwt_payload.get("iat"))
                    jwt_exp = _ts_to_iso(jwt_payload.get("exp"))
                    jwt_auth_time = _ts_to_iso(jwt_payload.get("auth_time"))
                    jwt_plan_type = str(auth_section.get("chatgpt_plan_type", ""))
                    jwt_chatgpt_user_id = str(auth_section.get("chatgpt_user_id", ""))
                    jwt_user_id = str(auth_section.get("user_id", ""))
                    profile_section = jwt_payload.get("https://api.openai.com/profile", {})
                    jwt_profile_email = str(profile_section.get("email", ""))
                    _log(f"  JWT sub={jwt_sub}, iat={jwt_iat}, exp={jwt_exp}, auth_time={jwt_auth_time}")
                    _log(f"  JWT plan_type={jwt_plan_type}, user_id={jwt_user_id}, profile_email={jwt_profile_email}")
            except Exception as e:
                _log(f"  JWT parse warning: {e}")

        # ── 12. Get cookies ──
        try:
            cookies = page.context.cookies()
            oai_did = ""
            session_cookie = ""
            for c in cookies:
                if c["name"] == "oai-did":
                    oai_did = c["value"]
                if c["name"] == "__Secure-next-auth.session-token":
                    session_cookie = c["value"]
            _log(f"  oai_did: {oai_did[:30]}..." if oai_did else "  oai_did: N/A")
            _log(f"  session_cookie: {session_cookie[:30]}..." if session_cookie else "  session_cookie: N/A")
        except Exception:
            oai_did = ""

        # ── 13. Build result ──
        expires = str(session_data.get("expires") or "")
        user_info = session_data.get("user") or {}

        result = {
            "success": bool(session_data.get("accessToken")),
            "stage": (
                "session_fetched"
                if session_data.get("accessToken")
                else "not_logged_in" if login_state_failed else "no_access_token"
            ),
            "email": email,
            "password": password,
            "account_id": account_id,
            "access_token": session_data.get("accessToken", ""),
            "session_token": session_data.get("sessionToken", ""),
            "id_token": "",
            "expires": expires,
            "user": user_info,
            "name": about_name,
            "birthdate": about_birthdate,
            "age": about_age,
            "fingerprint": fingerprint.to_dict() if fingerprint else {},
            "oai_device_id": oai_did,
            "source": "email_login_playwright",
            "created_at": (
                (existing_data or {}).get("created_at")
                or datetime.now(timezone(timedelta(hours=8))).isoformat()
            ),
            "session_refreshed_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
            "jwt_sub": jwt_sub,
            "jwt_iat": jwt_iat,
            "jwt_exp": jwt_exp,
            "jwt_auth_time": jwt_auth_time,
            "jwt_plan_type": jwt_plan_type,
            "jwt_chatgpt_user_id": jwt_chatgpt_user_id,
            "jwt_user_id": jwt_user_id,
            "jwt_profile_email": jwt_profile_email,
            "error_message": "",
            "rum_view_tags": session_data.get("rumViewTags", {}),
        }

        if keep_logs:
            result["logs"] = logs

        # ── Merge existing data when re-logging into an existing account ──
        # Preserve TOTP secret and original fingerprint from the input JSON
        # so they are not lost when the file is re-saved.
        if existing_data:
            # Preserve TOTP (already set up — won't be re-fetched)
            if existing_data.get("totp"):
                result["totp"] = existing_data["totp"]
                _log("  Preserved existing TOTP secret from input JSON")
            # Preserve original fingerprint if the restored fingerprint
            # didn't capture it (belt and suspenders)
            if existing_data.get("fingerprint") and not result.get("fingerprint"):
                result["fingerprint"] = existing_data["fingerprint"]

        # ── 14. Save to output ──
        safe_email = email.replace("@", "_at_").replace("+", "")
        # Use the provided output path if available (e.g. re-saving to the
        # same file for --adjust-settings), otherwise route by success:
        #   success  → json2server/session-{email}.json
        #   fail     → json2server/failed/session-{email}.json
        if _output_path_override:
            output_path = _output_path_override
        elif result.get("success"):
            output_path = _OUTPUT_DIR / f"session-{safe_email}.json"
        else:
            output_path = _FAILED_OUTPUT_DIR / f"session-{safe_email}.json"
        with open(output_path, "w", encoding="utf-8") as f:
            json.dump(result, f, ensure_ascii=False, indent=2)
        _log(f"\n[*] Result saved to {output_path}")

        # ── 15. Setup TOTP (optional) ──
        if setup_totp and result["success"]:
            _log("[Step 15] Setting up TOTP authenticator...")
            # First attempt: skip the proactive welcome-modal check —
            # Step 9 already dismissed the "You're all set" modal, and
            # it normally only appears once.  We fall back to dismissing
            # it on retry if the first attempt fails.
            totp_data = _setup_totp_on_page(page, email, mailbox=mailbox,
                                            skip_welcome_check=True)
            if totp_data:
                result["totp"] = totp_data
                result["stage"] = "totp_setup_complete"
                # Re-save JSON with TOTP data
                with open(output_path, "w", encoding="utf-8") as f:
                    json.dump(result, f, ensure_ascii=False, indent=2)
                _log(f"[*] TOTP secret saved, JSON updated: {output_path}")
            else:
                # TOTP may have failed because the welcome modal appeared
                # (rendered late, after Step 9 already checked).  Dismiss
                # it and retry once before giving up.
                _log("  [!] First TOTP attempt failed — dismissing any modal and retrying...")
                _dismiss_welcome_modal(page, bridge=bridge)
                totp_data = _setup_totp_on_page(page, email, mailbox=mailbox,
                                                skip_welcome_check=True)
                if totp_data:
                    result["totp"] = totp_data
                    result["stage"] = "totp_setup_complete"
                    with open(output_path, "w", encoding="utf-8") as f:
                        json.dump(result, f, ensure_ascii=False, indent=2)
                    _log(f"[*] TOTP retry succeeded! JSON updated: {output_path}")
                else:
                    _log("[!] TOTP setup did not complete (may already be enabled, or failed)")

        # ── 16. Send a chat message (fire and forget — settings run while
        #    the AI reply generates in the background).
        chat_sent = False
        if send_chat_message and result["success"]:
            _log("[Step 16] Sending chat message to warm up account...")
            prompts = _load_warming_prompts()
            if prompts:
                chosen = random.choice(prompts)
                _log(f"  Topic: {chosen.get('topic', 'N/A')}")
                _log(f"  Prompt: {chosen['prompt'][:80]}...")
                _save_screenshot(page, "16-before-chat", level="debug")
                chat_sent = _send_chat_message(page, chosen["prompt"], timeout=60,
                                               bridge=bridge, wait_for_reply=False)
                if chat_sent:
                    _log("  Chat message sent — will check reply after settings")
                    _save_screenshot(page, "16-chat-sent", level="debug")
                else:
                    _log("  [!] Chat message failed to send — continuing anyway")
            else:
                _log("  [!] No warming prompts found — skipping chat step")

        # ── 17. Adjust privacy settings (default on; --no-adjust-settings to skip) ──
        #    This runs while the AI reply is generating, overlapping the
        #    chat wait time with settings work.
        if adjust_settings and result["success"]:
            _log("[Step 17] Adjusting privacy settings...")
            try:
                settings_result = _adjust_privacy_settings(page, email, interactive=interactive)
                result["settings_adjusted"] = settings_result
                if settings_result["memory_disabled"] and settings_result["improve_model_disabled"]:
                    result["stage"] = "settings_adjusted"
                else:
                    result["stage"] = "settings_partial"
                # Re-save JSON with settings adjustment result
                with open(output_path, "w", encoding="utf-8") as f:
                    json.dump(result, f, ensure_ascii=False, indent=2)
                _log(f"[*] Settings adjustment result saved: {output_path}")
            except Exception as e:
                _log(f"  [!] Settings adjustment failed: {e}")
                import traceback
                traceback.print_exc()
                _save_screenshot(page, "settings-adjust-error")
                result["settings_adjusted"] = {
                    "attempted_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
                    "memory_disabled": False,
                    "improve_model_disabled": False,
                    "error": str(e),
                }

        # ── After settings: check if the chat reply completed ──
        if chat_sent:
            reply_ok = _wait_for_chat_reply(page, timeout=60)
            if reply_ok:
                _log("  Chat reply completed — usage trace created")
                _save_screenshot(page, "16-chat-done", level="debug")
            else:
                _log("  [!] Chat reply did not complete (timeout or error) — continuing anyway")

        return result

    except Exception as e:
        import traceback
        _log(f"\n[!] Unhandled error: {e}")
        traceback.print_exc()
        _save_screenshot(page, f"error_{datetime.now().strftime('%H%M%S')}")
        return {
            "success": False, "stage": "exception",
            "email": email, "password": password,
            "error_message": str(e), "logs": logs,
        }

    finally:
        # ── Cleanup ──
        _log("\n[*] Cleanup...")
        try:
            if cdp_session:
                cdp_session.detach()
        except Exception:
            pass
        try:
            if browser:
                browser.close()
        except Exception:
            pass
        try:
            if active_playwright:
                active_playwright.stop()
        except Exception:
            pass

        _log("[*] Done.")
        print(f"\n{'═' * 55}")
        print("  Browser closed. Check email-reg/output/ for results.")
        print(f"{'═' * 55}")


# ═══════════════════════════════════════════════════════════════════════════
# CLI
# ═══════════════════════════════════════════════════════════════════════════

def main():
    parser = argparse.ArgumentParser(
        description="ChatGPT Email Registration via Log-In — Playwright + Real Chrome CDP"
    )
    parser.add_argument("--email", default=None, help="Email to log in with (default: auto-generate via zoho)")
    parser.add_argument("--password", default=None, help="Password (default: auto-generate)")
    parser.add_argument("--proxy", default=None, help="Proxy URL (default: read from zhuce6 config)")
    parser.add_argument("--headless", action="store_true", help="Run Chrome headless")
    parser.add_argument("--password-length", type=int, default=12,
                        help="Generated password length (default: 12)")
    parser.add_argument("--recon", action="store_true", default=False,
                        help="Enable heavy recon/DOM-scanning logging (disabled by default "
                             "to avoid CF WAF triggers)")
    parser.add_argument("--keep-logs", action="store_true", default=False,
                        help="Include verbose logs in output JSON (omitted by default)")
    parser.add_argument("--no-totp", action="store_false", dest="setup_totp",
                        help="Skip TOTP authenticator setup (default: setup TOTP)")
    parser.add_argument("--fingerprint-json", default=None,
                        help="Path to a previous session JSON to restore fingerprint from "
                             "(bypasses fresh Fingerprint generation)")
    parser.add_argument("--count", type=int, default=1,
                        help="Number of accounts to register (default: 1). "
                             "Set >1 for batch mode with auto-generated emails.")
    parser.add_argument("--delay", type=float, default=2.0,
                        help="Seconds to wait between registrations (default: 2.0).")
    parser.add_argument("--on-failure", choices=["skip", "stop", "retry"], default="skip",
                        help="Failure handling in batch mode: skip (continue), stop (abort), "
                             "retry (new email + fingerprint, up to --retry-max). Default: skip.")
    parser.add_argument("--retry-max", type=int, default=10,
                        help="Max attempts per slot when --on-failure=retry (default: 2).")
    parser.add_argument("--interactive", action="store_true", default=False,
                        help="Enable interactive prompts on unexpected pages (default: off). "
                             "When off, errors auto-continue — useful for batch mode.")
    parser.add_argument("--no-matcher", action="store_true", default=False,
                        help="Disable text-based page-state matcher fallback "
                             "(default: matcher enabled).")
    parser.add_argument("--no-chat", action="store_false", dest="send_chat_message",
                        help="Skip sending a chat message after registration "
                             "(default: send a random warming prompt).")
    parser.add_argument("--debug", action="store_true", default=False,
                        help="Enable debug screenshots at every milestone / normal-flow "
                             "checkpoint (default: only error/failure screenshots).")
    parser.add_argument("--no-adjust-settings", action="store_false", dest="adjust_settings",
                        default=True,
                        help="Skip the privacy settings adjustment step (disable Memory "
                             "and disable 'Improve model for everyone'). By default, "
                             "settings adjustment runs after registration/login.")
    parser.add_argument("--input-dir", default=None,
                        help="Directory containing session-*.json files to re-login and "
                             "adjust settings (default: email-reg/output/). "
                             "When specified without --email, batch-processes all "
                             "session-*.json files in the directory.")
    parser.add_argument("--skip-adjusted", action="store_true", default=False,
                        help="When used with --input-dir, skip accounts whose JSON already "
                             "has settings_adjusted.memory_disabled=true AND "
                             "settings_adjusted.improve_model_disabled=true. "
                             "These accounts are counted as 'skipped' in the summary.")
    parser.add_argument("--skip-refreshed-hours", type=int, default=24,
                        help="When used with --input-dir, skip accounts whose "
                             "session_refreshed_at is within N hours. "
                             "This avoids re-processing accounts that were already "
                             "refreshed in a prior (interrupted) batch run. "
                             "Default: 24 (one day). Set to 0 to disable and "
                             "re-process everything.")
    args = parser.parse_args()

    # Resolve proxy (CLI arg > zhuce6 config)
    proxy = args.proxy or _load_proxy_from_config()

    # ── Batch mode preflight checks ──
    if args.count > 1 and args.email:
        print("[!] --email cannot be used with --count > 1 (each registration needs a unique email)")
        raise SystemExit(1)
    if args.count > 1 and args.fingerprint_json:
        print("[*] --fingerprint-json ignored in batch mode (each account gets a unique fingerprint)")

    # ── Init mailbox (needed for auto-generated emails) ──
    mailbox: ZohoMailbox | None = None
    if not args.email or args.count > 1:
        mailbox = ZohoMailbox(proxy=proxy)

    # ═════════════════════════════════════════════════════════════════════
    # ── Re-login mode: process existing session JSONs ──
    # Triggered when --input-dir or --fingerprint-json is provided without
    # an explicit --email.  Re-logs into each account and adjusts settings.
    # ═════════════════════════════════════════════════════════════════════
    if (args.input_dir is not None or args.fingerprint_json) and not args.email:
        # ── Single-file mode: --fingerprint-json points to the account ──
        input_dir = Path(args.input_dir) if args.input_dir else _OUTPUT_DIR
        if args.fingerprint_json:
            fp_path = Path(args.fingerprint_json)
            if not fp_path.exists():
                print(f"[!] File not found: {fp_path}")
                raise SystemExit(1)
            json_files = [fp_path]
        else:
            json_files = sorted(
                f for f in input_dir.glob("session-*.json")
                if not f.name.startswith("batch-summary")
            )
        if not json_files:
            print(f"[!] No session-*.json files found")
            raise SystemExit(1)

        print(f"\n{'═' * 55}")
        print(f"  ADJUST SETTINGS — {'Single' if len(json_files) == 1 else 'Batch'} Mode")
        print(f"{'═' * 55}")
        if args.fingerprint_json:
            print(f"  Input file: {args.fingerprint_json}")
        else:
            print(f"  Input dir:  {input_dir}")
        print(f"  Accounts:   {len(json_files)}")
        print(f"  Proxy:      {proxy or 'none'}")
        print(f"{'═' * 55}\n")

        batch_results: list[dict] = []
        batch_start = datetime.now(timezone(timedelta(hours=8)))

        for idx, json_path in enumerate(json_files):
            slot = idx + 1
            print(f"\n{'█' * 55}")
            print(f"█  {slot}/{len(json_files)}  —  {json_path.name}")
            print(f"{'█' * 55}")

            try:
                with open(json_path, "r", encoding="utf-8") as f:
                    existing_data = json.load(f)
            except Exception as e:
                print(f"  [!] Failed to read {json_path.name}: {e}")
                batch_results.append({
                    "file": str(json_path), "success": False,
                    "error": f"Read error: {e}",
                })
                continue

            # ── Skip banned/deactivated accounts (default, always on) ──
            if existing_data.get("is-ban") is True:
                print(f"  [!] Skipping {json_path.name}: account is banned/deactivated (is-ban=true)")
                batch_results.append({
                    "file": str(json_path),
                    "email": existing_data.get("email", ""),
                    "success": False,
                    "skipped": True,
                    "reason": "is_ban",
                })
                continue

            # ── Skip recently-refreshed accounts ──
            if args.skip_refreshed_hours > 0:
                refreshed_at_str = (existing_data.get("session_refreshed_at") or "").strip()
                if refreshed_at_str:
                    try:
                        # Handle timezone offset formats: +08:00, +0800, Z
                        cleaned = refreshed_at_str
                        if cleaned.endswith("Z"):
                            cleaned = cleaned[:-1] + "+00:00"
                        if re.match(r".*[+-]\d{4}$", cleaned) and ":" not in cleaned[-5:]:
                            cleaned = cleaned[:-2] + ":" + cleaned[-2:]
                        refreshed_at = datetime.fromisoformat(cleaned)
                        # Ensure timezone-aware comparison
                        if refreshed_at.tzinfo is None:
                            refreshed_at = refreshed_at.replace(tzinfo=timezone(timedelta(hours=8)))
                        age_seconds = (datetime.now(timezone(timedelta(hours=8))) - refreshed_at).total_seconds()
                        if age_seconds < args.skip_refreshed_hours * 3600:
                            hours_ago = age_seconds / 3600
                            print(f"  Skipping {json_path.name}: refreshed {hours_ago:.1f}h ago "
                                  f"(within {args.skip_refreshed_hours}h threshold)")
                            batch_results.append({
                                "file": str(json_path),
                                "email": existing_data.get("email", ""),
                                "success": True,
                                "skipped": True,
                                "reason": "recently_refreshed",
                                "session_refreshed_at": refreshed_at_str,
                            })
                            continue
                        else:
                            hours_ago = age_seconds / 3600
                            print(f"  Last refreshed {hours_ago:.1f}h ago — re-processing")
                    except (ValueError, TypeError) as e:
                        print(f"  [!] Could not parse session_refreshed_at '{refreshed_at_str[:30]}': {e}")

            email = (existing_data.get("email") or "").strip()
            password = (existing_data.get("password") or "").strip()
            totp_secret = (existing_data.get("totp") or {}).get("secret", "").strip() or None

            if not email or not password:
                print(f"  [!] Skipping {json_path.name}: missing email or password")
                batch_results.append({
                    "file": str(json_path), "success": False,
                    "error": "Missing email or password in JSON",
                })
                continue

            # Restore fingerprint from existing data
            fingerprint = None
            fp_dict = existing_data.get("fingerprint")
            if fp_dict:
                try:
                    fingerprint = Fingerprint.from_dict(fp_dict)
                    print(f"  Fingerprint restored from existing JSON")
                except Exception as e:
                    print(f"  [!] Fingerprint restore failed: {e} — generating fresh")
                    fingerprint = Fingerprint(seed=email)
            else:
                fingerprint = Fingerprint(seed=email)

            # ── Check if settings already adjusted ──
            sa_existing = existing_data.get("settings_adjusted") or {}
            already_adjusted = (
                sa_existing.get("memory_disabled") is True
                and sa_existing.get("improve_model_disabled") is True
            )

            # ── --skip-adjusted: skip accounts that are already fully adjusted ──
            if already_adjusted and args.skip_adjusted:
                print(f"  Settings already adjusted "
                      f"({sa_existing.get('attempted_at', 'unknown')[:19]}) — "
                      f"skipping entirely (--skip-adjusted)")
                batch_results.append({
                    "file": str(json_path),
                    "email": email,
                    "success": True,
                    "skipped": True,
                    "reason": "already_adjusted",
                })
                continue

            _do_adjust = args.adjust_settings and not already_adjusted
            if already_adjusted and args.adjust_settings:
                print(f"  Settings already adjusted ({sa_existing.get('attempted_at', 'unknown')[:19]}) — "
                      f"skipping adjustment, re-login only")

            # ── Re-login + adjust settings ──
            result = email_login(
                email=email,
                password=password,
                fingerprint=fingerprint,
                proxy=proxy,
                mailbox=mailbox,
                headless=args.headless,
                recon_enabled=args.recon,
                keep_logs=args.keep_logs,
                setup_totp=not bool(totp_secret),  # 没有 TOTP 的账号顺势开一下
                interactive=args.interactive,
                use_matcher=not args.no_matcher,
                send_chat_message=False,      # skip — not needed
                debug=args.debug,
                on_failure="skip",
                adjust_settings=_do_adjust,
                totp_secret=totp_secret,
                existing_data=existing_data,  # preserve totp/fingerprint
                _output_path_override=json_path,  # save back to same file
            )

            status = "OK" if result.get("success") else "FAIL"
            sa = result.get("settings_adjusted", {})
            mem_ok = sa.get("memory_disabled", False)
            imp_ok = sa.get("improve_model_disabled", False)
            print(f"  [{status}] {email}  |  memory={mem_ok}  improve_model={imp_ok}")

            batch_results.append({
                "file": str(json_path),
                "email": email,
                "success": result.get("success", False),
                "settings_adjusted": sa,
            })

            # Inter-batch delay
            if slot < len(json_files):
                delay = args.delay if args.delay > 0 else 3.0
                print(f"  Sleeping {delay:.1f}s before next account...")
                time.sleep(delay)

        # ── Batch summary ──
        batch_end = datetime.now(timezone(timedelta(hours=8)))
        ok_count = sum(1 for r in batch_results if r.get("success") and not r.get("skipped"))
        skipped_count = sum(1 for r in batch_results if r.get("skipped"))
        fail_count = sum(1 for r in batch_results if not r.get("success") and not r.get("skipped"))

        # Break down skipped by reason
        skipped_ban = sum(1 for r in batch_results if r.get("reason") == "is_ban")
        skipped_refreshed = sum(1 for r in batch_results if r.get("reason") == "recently_refreshed")
        skipped_adjusted = sum(1 for r in batch_results if r.get("reason") == "already_adjusted")
        skipped_other = skipped_count - skipped_ban - skipped_refreshed - skipped_adjusted

        print(f"\n{'═' * 55}")
        print(f"  RE-LOGIN — Batch Complete")
        print(f"{'═' * 55}")
        print(f"  Total:   {len(json_files)}")
        print(f"  Success: {ok_count}")
        print(f"  Skipped: {skipped_count}")
        if skipped_ban:
            print(f"    - banned/deactivated:  {skipped_ban}")
        if skipped_refreshed:
            print(f"    - recently refreshed:  {skipped_refreshed}")
        if skipped_adjusted:
            print(f"    - already adjusted:    {skipped_adjusted}")
        if skipped_other:
            print(f"    - other:               {skipped_other}")
        print(f"  Failed:  {fail_count}")
        print(f"  Time:    {batch_start.strftime('%H:%M:%S')} → {batch_end.strftime('%H:%M:%S')}")

        for r in batch_results:
            reason = f" ({r.get('reason', '')})" if r.get("skipped") and r.get("reason") else ""
            if r.get("skipped"):
                s = "SKIP"
            elif r.get("success"):
                s = "OK"
            else:
                s = "FAIL"
            print(f"  [{s}] {r.get('email', Path(r.get('file', '')).name)}{reason}")

        # Save batch summary
        summary_path = _OUTPUT_DIR / f"re-login-summary-{batch_start.strftime('%Y%m%d_%H%M%S')}.json"
        summary = {
            "mode": "re_login",
            "batch_start": batch_start.isoformat(),
            "batch_end": batch_end.isoformat(),
            "total": len(json_files),
            "success": ok_count,
            "skipped": skipped_count,
            "skipped_breakdown": {
                "banned_deactivated": skipped_ban,
                "recently_refreshed": skipped_refreshed,
                "already_adjusted": skipped_adjusted,
                "other": skipped_other,
            },
            "failure": fail_count,
            "input_dir": str(input_dir),
            "results": batch_results,
        }
        with open(summary_path, "w", encoding="utf-8") as f:
            json.dump(summary, f, ensure_ascii=False, indent=2)
        print(f"  Summary: {summary_path}")

        return {"batch_summary": summary}

    # ── Run registrations ──
    total = args.count
    results: list[dict] = []
    batch_start = datetime.now(timezone(timedelta(hours=8)))

    for i in range(total):
        slot = i + 1  # 1-based for display

        # --- resolve email ---
        if args.email:
            email = args.email.strip()
        else:
            account = mailbox.get_email(persist=False)
            email = account.email

        # --- resolve password ---
        if args.password:
            password = args.password
        else:
            password = "".join(
                secrets.choice("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
                for _ in range(args.password_length)
            )

        # --- resolve fingerprint ---
        if args.fingerprint_json and total == 1:
            fp_path = Path(args.fingerprint_json)
            fingerprint = None
            if fp_path.exists():
                try:
                    with open(fp_path, "r", encoding="utf-8") as f:
                        saved = json.load(f)
                    fp_dict = saved.get("fingerprint")
                    if fp_dict:
                        fingerprint = Fingerprint.from_dict(fp_dict)
                except Exception as e:
                    print(f"[!] Failed to restore fingerprint: {e}")
            if fingerprint is None:
                fingerprint = Fingerprint(seed=email)
        else:
            fingerprint = Fingerprint(seed=email)

        # --- progress header ---
        if total > 1:
            print(f"\n{'█' * 55}")
            print(f"█  Batch {slot}/{total}  —  {email}")
            print(f"{'█' * 55}")

        # --- run login ---
        # Settings adjustment is on by default.  TOTP setup and chat warming
        # run normally unless explicitly disabled with --no-totp / --no-chat.
        _do_totp = args.setup_totp
        _do_chat = args.send_chat_message

        # Extract TOTP secret from fingerprint JSON if available (single-account
        # re-login mode where the user passes --fingerprint-json).
        _totp_secret: str | None = None
        _existing_data: dict | None = None
        if args.fingerprint_json:
            fp_path = Path(args.fingerprint_json)
            if fp_path.exists():
                try:
                    with open(fp_path, "r", encoding="utf-8") as _f:
                        _existing_data = json.load(_f)
                    _totp_secret = (_existing_data.get("totp") or {}).get("secret", "").strip() or None
                    if _totp_secret:
                        print(f"  TOTP secret loaded from {fp_path.name}")
                except Exception:
                    pass

        result = email_login(
            email=email,
            password=password,
            fingerprint=fingerprint,
            proxy=proxy,
            mailbox=mailbox,
            headless=args.headless,
            recon_enabled=args.recon,
            keep_logs=args.keep_logs,
            setup_totp=_do_totp,
            interactive=args.interactive,
            use_matcher=not args.no_matcher,
            send_chat_message=_do_chat,
            debug=args.debug,
            on_failure=args.on_failure,
            adjust_settings=args.adjust_settings,
            totp_secret=_totp_secret,
            existing_data=_existing_data,
        )

        # --- persist on success ---
        if result["success"] and mailbox is not None:
            mailbox.persist_email(email)
        elif not result["success"] and mailbox is not None and total == 1:
            print(f"[*] Registration failed — email NOT persisted (can be reused): {email}")

        # --- handle failure ---
        if not result["success"]:
            if args.on_failure == "stop":
                results.append(result)
                print(f"\n[!] Batch stopped at {slot}/{total} due to failure (--on-failure=stop)")
                break
            elif args.on_failure == "retry":
                retry_ok = False
                for retry_n in range(1, args.retry_max):
                    print(f"\n[*] Retry {retry_n}/{args.retry_max - 1} for slot {slot}...")
                    retry_account = mailbox.get_email(persist=False)
                    retry_email = retry_account.email
                    retry_password = "".join(
                        secrets.choice("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
                        for _ in range(args.password_length)
                    )
                    retry_fp = Fingerprint(seed=retry_email)
                    retry_result = email_login(
                        email=retry_email,
                        password=retry_password,
                        fingerprint=retry_fp,
                        proxy=proxy,
                        mailbox=mailbox,
                        headless=args.headless,
                        recon_enabled=args.recon,
                        keep_logs=args.keep_logs,
                        setup_totp=_do_totp,
                        interactive=args.interactive,
                        use_matcher=not args.no_matcher,
                        send_chat_message=_do_chat,
                        debug=args.debug,
                        on_failure=args.on_failure,
                        adjust_settings=args.adjust_settings,
                        totp_secret=_totp_secret,
                        existing_data=_existing_data,
                    )
                    if retry_result["success"]:
                        mailbox.persist_email(retry_email)
                        result = retry_result  # replace with successful retry
                        retry_ok = True
                        break
                if not retry_ok:
                    print(f"  [!] Slot {slot} exhausted retries ({args.retry_max} attempts)")
            # on-failure=skip: just record and continue

        results.append(result)

        # --- progress line ---
        status = "OK" if result["success"] else "FAIL"
        acct = result.get("account_id", "") or ""
        stage = result.get("stage", "")
        if total > 1:
            print(f"  [{status}] {email}  |  account_id={acct[:20]}  stage={stage}")

        # --- inter-batch delay ---
        if slot < total:
            print(f"  Sleeping {args.delay:.1f}s before next registration...")
            time.sleep(args.delay)

    # ── Batch summary ──
    batch_end = datetime.now(timezone(timedelta(hours=8)))
    success_count = sum(1 for r in results if r.get("success"))
    fail_count = len(results) - success_count

    print(f"\n{'═' * 55}")
    print(f"  BATCH COMPLETE" if total > 1 else f"  RESULT")
    print(f"{'═' * 55}")
    if total > 1:
        print(f"  Total:   {total}")
        print(f"  Success: {success_count}")
        print(f"  Failed:  {fail_count}")
        print(f"  Time:    {batch_start.strftime('%H:%M:%S')} → {batch_end.strftime('%H:%M:%S')}")
    print(f"  Output:  {_OUTPUT_DIR}")

    # Save batch summary JSON (only for multi-registration)
    if total > 1:
        batch_summary_path = _OUTPUT_DIR / f"batch-summary-{batch_start.strftime('%Y%m%d_%H%M%S')}.json"
        summary = {
            "batch_start": batch_start.isoformat(),
            "batch_end": batch_end.isoformat(),
            "total": total,
            "success": success_count,
            "failure": fail_count,
            "args": {
                "count": args.count,
                "delay": args.delay,
                "on_failure": args.on_failure,
                "retry_max": args.retry_max,
                "proxy": proxy,
                "headless": args.headless,
                "setup_totp": args.setup_totp,
                "send_chat_message": args.send_chat_message,
            },
            "results": [
                {
                    "email": r.get("email", ""),
                    "success": r.get("success", False),
                    "account_id": r.get("account_id", ""),
                    "stage": r.get("stage", ""),
                    "error_message": r.get("error_message", ""),
                }
                for r in results
            ],
        }
        with open(batch_summary_path, "w", encoding="utf-8") as f:
            json.dump(summary, f, ensure_ascii=False, indent=2)
        print(f"  Summary: {batch_summary_path}")

    # Print individual result lines
    for i, r in enumerate(results):
        status = "OK" if r.get("success") else "FAIL"
        email = r.get("email", "")
        acct = r.get("account_id", "") or "N/A"
        token = str(r.get("access_token", ""))
        print(f"  [{status}] {email}  account_id={acct}")
        if token:
            print(f"         access_token={token[:40]}...")
        if r.get("error_message"):
            print(f"         error={r.get('error_message')}")

    return results[0] if len(results) == 1 else {"batch_summary": summary}


if __name__ == "__main__":
    main()

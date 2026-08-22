"""
Deterministic fingerprint generation for browser and Codex CLI identity.

Core principle: same account → same fingerprint → consistent digital identity.

- Browser fingerprint: used for all OpenAI auth/registration HTTP requests.
- Codex fingerprint: embedded in CPA JSON output as the "headers" field.

OS is selected deterministically from seed + configurable weights,
then browser and codex profiles are drawn from the SAME OS ecosystem
— no more Windows-browser + Mac-codex mismatches.

Chrome version is locked to 145 across all profiles.

Usage:
    from fingerprint import Fingerprint

    fp = Fingerprint(seed="+447308540243")
    fp.browser   # dict with ua, sec_ch_ua, accept_language, etc.
    fp.codex     # dict with codex_cli_rs ua, originator, version, etc.

    # Generate complete HTTP headers for API call:
    headers = fp.api_headers(referer_path="/log-in")
"""

import hashlib
import json
import secrets
from pathlib import Path


# ═════════════════════════════════════════════════════════════════════════════
# Ecosystem Profiles
#
# Each entry binds browser + codex to the SAME operating system.
# Chrome version locked to 145 (matching curl_cffi impersonate="chrome145" TLS).
# Differentiated by accept_language, chrome build string, and codex terminal.
# ═════════════════════════════════════════════════════════════════════════════

_ECOSYSTEM_PROFILES = [
    # ── Windows ───────────────────────────────────────────────────────────
    {
        "os": "windows",
        "browser": {
            "ua": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"Windows\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en-US,en;q=0.9",
            "platform": "Win32",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Windows NT 10.0; x64) vscode/1.101.0",
    },
    {
        "os": "windows",
        "browser": {
            "ua": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"Windows\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en,en-US;q=0.9,zh-CN;q=0.8,zh;q=0.7",
            "platform": "Win32",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Windows NT 10.0; x64) cmd.exe",
    },
    {
        "os": "windows",
        "browser": {
            "ua": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"Windows\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en-US,en;q=0.9,fr;q=0.8",
            "platform": "Win32",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Windows NT 10.0; x64) vscode/1.100.0",
    },
    {
        "os": "windows",
        "browser": {
            "ua": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"Windows\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en-GB,en;q=0.9,en-US;q=0.8",
            "platform": "Win32",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Windows NT 10.0; x64) Windows_Terminal/1.20.0",
    },

    # ── Mac (ARM64) ───────────────────────────────────────────────────────
    {
        "os": "mac",
        "browser": {
            "ua": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"macOS\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en-US,en;q=0.9",
            "platform": "MacIntel",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Mac OS 15.4.1; arm64) vscode/1.101.0",
    },
    {
        "os": "mac",
        "browser": {
            "ua": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"macOS\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en,en-US;q=0.9,zh-CN;q=0.8,zh;q=0.7",
            "platform": "MacIntel",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Mac OS 15.4.1; arm64) iTerm.app/3.6.9",
    },
    {
        "os": "mac",
        "browser": {
            "ua": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"macOS\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en-US,en;q=0.9,de;q=0.8",
            "platform": "MacIntel",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Mac OS 15.3.2; arm64) Apple_Terminal/455.1",
    },
    {
        "os": "mac",
        "browser": {
            "ua": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.7604.0 Safari/537.36",
            "sec_ch_ua": "\"Chromium\";v=\"145\", \"Google Chrome\";v=\"145\", \"Not?A_Brand\";v=\"99\"",
            "sec_ch_ua_platform": "\"macOS\"",
            "sec_ch_ua_mobile": "?0",
            "accept_language": "en-GB,en;q=0.9,en-US;q=0.8",
            "platform": "MacIntel",
            "chrome_version": "145.0.7604.0",
            "chrome_major": 145,
        },
        "codex_ua": "codex_cli_rs/0.134.0 (Mac OS 15.5.0; arm64) vscode/1.101.0",
    },
]

# Default OS weights when config.json doesn't specify fingerprint.os_weights.
# Sum doesn't need to be 1.0 — weights are normalised at selection time.
_DEFAULT_OS_WEIGHTS = {"windows": 100, "mac": 0}


# ═════════════════════════════════════════════════════════════════════════════
# OS weight loading
# ═════════════════════════════════════════════════════════════════════════════

def _load_os_weights():
    """Load OS weights from config.json. Returns {os_name: weight} dict."""
    config_path = Path(__file__).parent / "config.json"
    if not config_path.exists():
        return dict(_DEFAULT_OS_WEIGHTS)
    try:
        with open(config_path, "r", encoding="utf-8") as f:
            cfg = json.load(f)
        weights = cfg.get("fingerprint", {}).get("os_weights", {})
        if weights and isinstance(weights, dict):
            return {k.lower(): float(v) for k, v in weights.items()}
    except Exception:
        pass
    return dict(_DEFAULT_OS_WEIGHTS)


def _weighted_select_os(seed, weights):
    """Deterministically select an OS based on seed and configured weights.

    Uses SHA256 to generate a deterministic float in [0, 1) from the seed.
    The same (seed, weights) always produces the same OS.
    """
    digest = hashlib.sha256(f"{seed}:os_select".encode()).digest()
    rand_val = int.from_bytes(digest[:8], "big") / (2 ** 64)

    total = sum(weights.values())
    if total <= 0:
        return "windows"

    cumulative = 0.0
    for os_name, weight in weights.items():
        cumulative += weight / total
        if rand_val < cumulative:
            return os_name

    # Floating-point edge case: return last OS
    return list(weights.keys())[-1]


# ═════════════════════════════════════════════════════════════════════════════
# Deterministic selection
# ═════════════════════════════════════════════════════════════════════════════


def _pick_deterministic(seed, pool):
    """Pick one item from *pool* using deterministic SHA256 scoring.

    Algorithm matches the Go reference: for each item, compute
    SHA256(seed + "\\x00" + index + "\\x00" + serialized_item),
    take the first 8 bytes as a uint64 score.  Pick the item with
    the highest score.

    Same seed + same pool = always same result.
    """
    best_idx = 0
    best_score = 0
    for i, item in enumerate(pool):
        if isinstance(item, dict):
            serialized = json.dumps(item, sort_keys=True, separators=(",", ":"))
        else:
            serialized = str(item)
        payload = f"{seed}\x00{i}\x00{serialized}".encode()
        digest = hashlib.sha256(payload).digest()
        score = int.from_bytes(digest[:8], "big")
        if i == 0 or score > best_score:
            best_score = score
            best_idx = i
    return pool[best_idx]


def _codex_ua_to_version(ua):
    """Extract version string from a codex_cli_rs User-Agent.

    "codex_cli_rs/0.118.0 (Mac OS 15.4.1; arm64) vscode/1.100.0" → "0.118.0"
    """
    prefix = "codex_cli_rs/"
    ua = ua.strip()
    if not ua.startswith(prefix):
        return ""
    rest = ua[len(prefix):]
    idx = len(rest)
    for i, ch in enumerate(rest):
        if ch in (" ", "\t", "("):
            idx = i
            break
    return rest[:idx].strip()


# ═════════════════════════════════════════════════════════════════════════════
# Public API
# ═════════════════════════════════════════════════════════════════════════════


class Fingerprint:
    """Consistent browser + codex fingerprint for a single account.

    OS is selected deterministically from seed + configured weights,
    then browser and codex profiles are drawn from the same OS ecosystem
    — they can never disagree on OS.
    """

    def __init__(self, seed=None, oai_device_id=None, oai_session_id=None, os_weights=None):
        """
        Parameters:
            seed: deterministic seed (e.g. phone number). If None, a random
                  seed is generated — but consistency is lost across runs.
            oai_device_id: OpenAI-assigned device-id from sentinel extraction.
                           If None, a deterministic fallback is generated from seed.
            os_weights: optional dict overriding config.json os_weights
                        (e.g. {"windows": 70, "mac": 30}).
        """
        self.seed = seed or secrets.token_urlsafe(16)
        self.oai_device_id = oai_device_id or self._generate_device_id()
        self._os_weights = os_weights or _load_os_weights()
        self._oai_session_id = oai_session_id or ""
        self._ecosystem = None
        self._browser = None
        self._codex = None
        self._selected_os = None

    def _generate_device_id(self):
        """Deterministic fallback: SHA256(seed + ':oai_device_id') → UUIDv4 format.

        Used when no real oai-device-id was captured from sentinel extraction.
        Same seed → same device-id, ensuring stability across runs.
        """
        digest = hashlib.sha256(f"{self.seed}:oai_device_id".encode()).digest()
        hex_str = digest.hex()[:32]
        return f"{hex_str[:8]}-{hex_str[8:12]}-{hex_str[12:16]}-{hex_str[16:20]}-{hex_str[20:32]}"

    # ── lazy properties (computed once, never changes) ──

    @property
    def selected_os(self):
        """The OS chosen for this account (deterministic from seed + weights)."""
        if self._selected_os is None:
            _ = self.ecosystem  # trigger OS selection
        return self._selected_os

    @property
    def ecosystem(self):
        """One-shot: pick OS by weight, then pick a profile within that OS.

        Browser and codex are guaranteed to share the same OS.
        """
        if self._ecosystem is None:
            self._selected_os = _weighted_select_os(self.seed, self._os_weights)
            os_pool = [p for p in _ECOSYSTEM_PROFILES if p["os"] == self._selected_os]
            if not os_pool:
                # Should never happen with valid weights, but fall back to windows
                self._selected_os = "windows"
                os_pool = [p for p in _ECOSYSTEM_PROFILES if p["os"] == "windows"]
            self._ecosystem = _pick_deterministic(self.seed + ":ecosystem", os_pool)
        return self._ecosystem

    @property
    def browser(self):
        if self._browser is None:
            self._browser = self.ecosystem["browser"]
        return self._browser

    @property
    def codex(self):
        if self._codex is None:
            ua = self.ecosystem["codex_ua"]
            self._codex = {
                "user_agent": ua,
                "originator": "codex_cli_rs",
                "version": _codex_ua_to_version(ua),
                "x_codex_beta_features": "multi_agent",
            }
        return self._codex

    # ── serialisation ──

    def to_dict(self):
        return {
            "seed_hash": hashlib.sha256(self.seed.encode()).hexdigest()[:16],
            "oai_device_id": self.oai_device_id,
            "oai_session_id": self._oai_session_id,
            "selected_os": self.selected_os,
            "browser": dict(self.browser),
            "codex": dict(self.codex),
        }

    @classmethod
    def from_dict(cls, d):
        """Restore from a stored fingerprint dict (uses stored values directly,
        bypassing pool selection — so re-runs use the exact same fingerprint)."""
        fp = cls.__new__(cls)
        fp.seed = d.get("seed_hash", "")
        fp.oai_device_id = d.get("oai_device_id", "")
        fp._oai_session_id = d.get("oai_session_id", "")
        fp._selected_os = d.get("selected_os", "")
        fp._browser = dict(d.get("browser", {}))
        fp._codex = dict(d.get("codex", {}))
        fp._ecosystem = None  # not needed when _browser/_codex are restored
        return fp

    # ── header builders (browser requests) ──

    def api_headers(self, referer_path, auth_base=None, extra=None):
        """Return headers dict for an OpenAI auth API JSON request.

        Includes User-Agent, sec-ch-ua, Accept-Language, Origin, Referer,
        Content-Type, and sec-fetch-* computed per-request.
        """
        b = self.browser
        origin = auth_base or "https://auth.openai.com"
        h = {
            "User-Agent": b["ua"],
            "Accept": "application/json",
            "Content-Type": "application/json",
            "sec-ch-ua": b["sec_ch_ua"],
            "sec-ch-ua-platform": b["sec_ch_ua_platform"],
            "sec-ch-ua-mobile": b["sec_ch_ua_mobile"],
            "Accept-Language": b["accept_language"],
            "Origin": origin,
            "Referer": f"{origin}{referer_path}",
            "sec-fetch-dest": "empty",
            "sec-fetch-mode": "cors",
            "sec-fetch-site": "same-origin",
        }
        if extra:
            h.update(extra)
        return h

    def browser_headers(self):
        """Return headers dict for a browser navigation request (HTML page)."""
        b = self.browser
        return {
            "User-Agent": b["ua"],
            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
            "sec-ch-ua": b["sec_ch_ua"],
            "sec-ch-ua-platform": b["sec_ch_ua_platform"],
            "sec-ch-ua-mobile": b["sec_ch_ua_mobile"],
            "Accept-Language": b["accept_language"],
            "Upgrade-Insecure-Requests": "1",
            "sec-fetch-dest": "document",
            "sec-fetch-mode": "navigate",
            "sec-fetch-site": "none",
            "sec-fetch-user": "?1",
            "Cache-Control": "max-age=0",
            "Priority": "u=0, i",
        }

    # ── codex headers (for CPA JSON) ──

    def codex_cpa_headers(self):
        """Return the 'headers' dict to embed in CPA JSON output."""
        c = self.codex
        return {
            "Originator": c["originator"],
            "User-Agent": c["user_agent"],
            "Version": c["version"],
            "X-Codex-Beta-Features": c["x_codex_beta_features"],
        }

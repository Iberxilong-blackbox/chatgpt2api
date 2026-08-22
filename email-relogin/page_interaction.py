"""
PageInteractionBridge — Python wrapper for human-simulator JS library.

Injects human_sim_bundle.js into a Playwright page, then provides
human-like click, typing, and delay generation via page.evaluate().

Usage:
    bridge = PageInteractionBridge(page, verbose=True)
    bridge.inject()
    bridge.simulate_click('button:has-text("Log in")')
    bridge.simulate_typing('input[type="password"]', 'mypassword')

Parameter control:
    bridge = PageInteractionBridge(page, verbose=True,
        error_rates={"typing": 0, "misclick": 0},   # disable errors
        initial_profile={"type": "careful", "speed": 1.0, "accuracy": 1.0})

Supported selectors:
    Standard CSS selectors, plus Playwright's :has-text('X') pseudo-selector.
    The bridge converts :has-text() to a JS textContent filter internally.
"""

import json
from pathlib import Path


_BUNDLE_PATH = Path(__file__).parent / "human_sim_bundle.js"

# Default error rates from the JS library
DEFAULT_ERROR_RATES = {
    "typing": 0.08,            # 8% chars intentionally mistyped, then corrected
    "click_hesitation": 0.15,  # 15% clicks hover near target before clicking
    "misclick": 0.03,          # 3% clicks hit a nearby element first
    "scroll_drift": 0.12,      # 12% chance of unintentional scroll after action
    "backspace_correction": 0.05,
}

class PageInteractionBridge:
    """Bridges Python Playwright code to the human-simulator JS library.

    All JS interaction methods return False on failure so callers can
    fall back to native Playwright methods.

    Parameters:
        page: Playwright Page object
        verbose: Enable debug logging
        error_rates: Dict overriding default error rates.
            Set a key to 0 to disable that behavior:
            - typing: 0 → no intentional typos
            - click_hesitation: 0 → no hover-hesitation before clicks
            - misclick: 0 → no misclicks
            - scroll_drift: 0 → no unintentional scrolling
        initial_profile: Dict presetting the behavior personality.
            {type: "careful", speed: 1.0, accuracy: 1.0, pause_frequency: 0.2}
            accuracy >= 0.95 disables the error-typing branch entirely.
    """

    def __init__(self, page, verbose=False, error_rates=None, initial_profile=None):
        self._page = page
        self._verbose = verbose
        self._injected = False
        self._error_rates = dict(DEFAULT_ERROR_RATES)
        if error_rates:
            self._error_rates.update(error_rates)
        self._initial_profile = initial_profile

    # ── Injection ─────────────────────────────────────────────────

    def _read_bundle(self):
        """Read the JS bundle file. Returns '' on failure."""
        try:
            return _BUNDLE_PATH.read_text(encoding="utf-8")
        except Exception as e:
            if self._verbose:
                print(f"    [bridge] Failed to read bundle: {e}")
            return ""

    def inject(self) -> bool:
        """Inject the human-simulator bundle and initialize a global instance.

        Must be called after page navigation, before any simulate_* calls.
        Safe to call multiple times — re-injects if __hs__ is missing.
        Returns True on success.
        """
        bundle_js = self._read_bundle()
        if not bundle_js:
            return False

        try:
            # Step 1: inject the IIFE (defines window.HumanSimulator if not present)
            self._page.evaluate(f"""
                if (!window.HumanSimulator) {{
                    {bundle_js}
                }}
            """)
            # Step 2: initialize simulator instance (re-init if missing or re-injection)
            error_rates_json = json.dumps(self._error_rates)
            profile_json = json.dumps(self._initial_profile) if self._initial_profile else "null"
            init_result = self._page.evaluate(f"""
                async () => {{
                    try {{
                        if (window.__hs__) {{
                            return {{ ok: true, profile: window.__hs__.humanBehavior.behaviorPatterns.type, cached: true }};
                        }}
                        const opts = {{
                            verbose: {str(self._verbose).lower()},
                            persistProfile: false,
                            errorRates: {error_rates_json},
                            initialProfile: {profile_json}
                        }};
                        window.__hs__ = await HumanSimulator.createHumanSimulator(opts);
                        return {{ ok: true, profile: window.__hs__.humanBehavior.behaviorPatterns.type }};
                    }} catch (e) {{
                        return {{ ok: false, error: e.message }};
                    }}
                }}
            """)
            if init_result and init_result.get("ok"):
                self._injected = True
                if self._verbose and not init_result.get("cached"):
                    print(f"    [bridge] HumanSimulator injected (profile: {init_result.get('profile', '?')})")
                return True
            else:
                err = init_result.get("error", "unknown") if init_result else "no result"
                if self._verbose:
                    print(f"    [bridge] Init failed: {err}")
                return False
        except Exception as e:
            if self._verbose:
                print(f"    [bridge] Injection error: {e}")
            return False

    def _ensure_ready(self) -> bool:
        """Check if bridge is ready; auto re-inject if page navigated away."""
        if not self._injected:
            return False
        try:
            alive = self._page.evaluate("() => !!(window.__hs__ && window.__hs__.eventSimulator)")
            if not alive:
                if self._verbose:
                    print(f"    [bridge] __hs__ lost (page navigated), re-injecting...")
                return self.inject()
            return True
        except Exception:
            return self.inject()

    @property
    def is_ready(self) -> bool:
        """True if the simulator is injected and ready to use."""
        return self._ensure_ready()

    # ── Click ──────────────────────────────────────────────────────

    def simulate_click(self, selector: str) -> bool:
        """Simulate a human click on the element matching `selector`.

        Includes: bezier mouse trajectory, possible hesitation, possible
        misclick, full mousedown/mouseup/click event sequence.

        Supports standard CSS + Playwright's :has-text('X') pseudo-selector.

        Returns True on success, False if element not found or error.
        """
        if not self._ensure_ready():
            return False

        try:
            result = self._page.evaluate("""
                async (sel) => {
                    const hs = window.__hs__;
                    if (!hs) return { ok: false, error: '__hs__ not found' };
                    // Inline :has-text() adapter (no global _hsFind to avoid detection)
                    const el = (function(s) {
                        if (!s || typeof s !== 'string') return null;
                        const m = s.match(/:has-text\\(\\s*['\"]([^'\"]*)['\"]\\s*\\)/i);
                        if (m) {
                            const base = s.replace(/:has-text\\(\\s*['\"][^'\"]*['\"]\\s*\\)/gi, '').trim();
                            const needle = m[1].toLowerCase();
                            const els = [...document.querySelectorAll(base || '*')];
                            return els.find(function(e) { return (e.textContent || '').toLowerCase().includes(needle); }) || null;
                        }
                        return document.querySelector(s);
                    })(sel);
                    if (!el) return { ok: false, error: 'element not found: ' + sel };
                    const success = await hs.eventSimulator.simulateHumanClick(el);
                    return { ok: success };
                }
            """, selector)
            ok = result and result.get("ok", False)
            if self._verbose and not ok:
                err = result.get("error", "?") if result else "no result"
                print(f"    [bridge] Click failed: {err}")
            return ok
        except Exception as e:
            if self._verbose:
                print(f"    [bridge] Click error: {e}")
            return False

    # ── Typing ─────────────────────────────────────────────────────

    def simulate_typing(self, selector: str, text: str,
                        previous_message: str = "") -> bool:
        """Simulate human typing of `text` into the element matching `selector`.

        Includes: cognitive delay (reading/thinking/preparation),
        possible typing errors with correction, per-character delays
        with punctuation awareness.

        Supports standard CSS + Playwright's :has-text('X') pseudo-selector.

        Returns True on success.
        """
        if not self._ensure_ready():
            return False

        if not text:
            return True

        try:
            result = self._page.evaluate("""
                async (params) => {
                    const hs = window.__hs__;
                    if (!hs) return { ok: false, error: '__hs__ not found' };
                    // Inline :has-text() adapter (no global _hsFind to avoid detection)
                    const el = (function(s) {
                        if (!s || typeof s !== 'string') return null;
                        const m = s.match(/:has-text\\(\\s*['\"]([^'\"]*)['\"]\\s*\\)/i);
                        if (m) {
                            const base = s.replace(/:has-text\\(\\s*['\"][^'\"]*['\"]\\s*\\)/gi, '').trim();
                            const needle = m[1].toLowerCase();
                            const els = [...document.querySelectorAll(base || '*')];
                            return els.find(function(e) { return (e.textContent || '').toLowerCase().includes(needle); }) || null;
                        }
                        return document.querySelector(s);
                    })(params.selector);
                    if (!el) return { ok: false, error: 'element not found: ' + params.selector };
                    const result = await hs.eventSimulator.simulateHumanTyping(
                        el, params.text, params.previousMessage || ''
                    );
                    return { ok: result.success || false };
                }
            """, {
                "selector": selector,
                "text": text,
                "previousMessage": previous_message,
            })
            ok = result and result.get("ok", False)
            if self._verbose and not ok:
                err = result.get("error", "?") if result else "no result"
                print(f"    [bridge] Typing failed: {err}")
            return ok
        except Exception as e:
            if self._verbose:
                print(f"    [bridge] Typing error: {e}")
            return False

    # ── Delay ──────────────────────────────────────────────────────

    def get_delay_ms(self, min_ms: float = 100, max_ms: float = 300,
                     context: str = "normal") -> float:
        """Get a human-like delay value from the JS simulator (milliseconds).

        Use this for time.sleep() between actions. Falls back to
        Python-side human_delay if bridge is not available.
        """
        if not self._ensure_ready():
            from human_delay import generate_human_delay
            return generate_human_delay(min_ms, max_ms, context)

        try:
            return self._page.evaluate("""
                (params) => {
                    const hs = window.__hs__;
                    if (!hs) return -1;
                    return hs.humanBehavior.generateHumanDelay(
                        params.min, params.max, params.context
                    );
                }
            """, {"min": min_ms, "max": max_ms, "context": context})
        except Exception:
            from human_delay import generate_human_delay
            return generate_human_delay(min_ms, max_ms, context)

    def human_sleep(self, min_s: float, max_s: float,
                    context: str = "normal", label: str = None) -> None:
        """Drop-in replacement for _human_delay().

        Sleeps for a human-like duration in seconds.
        """
        import time
        delay_ms = self.get_delay_ms(min_s * 1000, max_s * 1000, context)
        delay_s = delay_ms / 1000.0
        if label and self._verbose:
            print(f"[*] {label} ({delay_s:.1f}s)")
        time.sleep(delay_s)

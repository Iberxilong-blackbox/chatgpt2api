"""
Page State Text Matcher — fallback page-state recognition via visible-text
keyword matching.

Extracts the full visible text of the current page via Playwright's
``page.inner_text('body')`` and compares it against a library of keyword
sets.  When a match is found, the corresponding recovery action is executed
via Playwright DOM interaction.

This is a **fallback module** — it runs only on abnormal paths (unknown page
type, TOTP setup failure, etc.).  Normal flow is unaffected.

Usage::

    from page_state_matcher import PageStateMatcher

    matcher = PageStateMatcher(states_file="page_states.json")

    # One-shot: identify + recover
    result = matcher.identify_and_recover(page)
    if result["matched"] and result["recovered"]:
        print(f"Recovered via {result['template_id']} "
              f"(score={result['score']:.2f})")

    # Two-step (useful when you need to inspect before acting):
    best = matcher.identify(page)       # → template_id | None
    if best:
        matcher.recover(page, best)

Requires: none (pure stdlib + Playwright).
"""

import json
from pathlib import Path
from typing import Any


# ── Matching algorithm ───────────────────────────────────────────────────────

def compute_match_score(page_text: str, match_texts: list[str]) -> float:
    """Return the fraction of *match_texts* keywords found in *page_text*.

    Each keyword is tested independently via case-insensitive substring
    search.  Keyword order is ignored — text from different DOM regions
    (header, dialog, sidebar) naturally appears in any order.

    Returns a score in [0.0, 1.0]:  ``matched / len(match_texts)``.
    """
    if not match_texts:
        return 0.0
    page_lower = page_text.lower()
    matched = sum(1 for t in match_texts if t.lower() in page_lower)
    return matched / len(match_texts)


# ── Recovery actions ─────────────────────────────────────────────────────────

def _action_click_continue_in_dialog(page, config: dict) -> bool:
    """Click the Continue (or equivalent) button inside a dialog."""
    selectors = config.get("selectors", [
        "div[role='dialog'] button:has-text('Continue')",
        "[role='dialog'] button:has-text('Continue')",
        "div[role='dialog'] button:has-text('Next')",
        "[role='dialog'] button:has-text('Next')",
        "div[role='dialog'] button:has-text('Get started')",
        "div[role='dialog'] button:has-text(\"Let's go\")",
        "div[role='dialog'] button",
    ])

    _short = 500  # ms — element is either in DOM or not; don't burn 30s default

    for sel in selectors:
        try:
            btn = page.locator(sel).first
            # Filter out Cancel/Back/Close when using the wildcard selector
            if sel in ("div[role='dialog'] button", "[role='dialog'] button"):
                try:
                    text = (btn.text_content(timeout=_short) or "").strip().lower()
                    if not text or any(skip in text for skip in
                                       ("cancel", "back", "close", "skip",
                                        "maybe later", "dismiss")):
                        continue
                except Exception:
                    continue

            text = (btn.text_content(timeout=_short) or "").strip()[:60]
            print(f"  [matcher] Clicking '{text}' ({sel})")
            btn.click(force=True, timeout=3000)
            page.wait_for_timeout(2000)
            return True
        except Exception:
            continue

    # Fallbacks: Escape key, then click at (10,10)
    try:
        page.keyboard.press("Escape")
        page.wait_for_timeout(1000)
    except Exception:
        pass
    try:
        page.mouse.click(10, 10)
        page.wait_for_timeout(500)
    except Exception:
        pass

    return False


def _action_click_button(page, config: dict) -> bool:
    """Click a button identified by *config.selectors*."""
    selectors = config.get("selectors", [])
    _short = 500  # ms — element is either in DOM or not; don't burn 30s default
    for sel in selectors:
        try:
            btn = page.locator(sel).first
            text = (btn.text_content(timeout=_short) or "").strip()[:60]
            print(f"  [matcher] Clicking '{text}' ({sel})")
            btn.click(force=True, timeout=3000)
            page.wait_for_timeout(2000)
            return True
        except Exception:
            continue
    return False


def _action_wait_and_retry(page, config: dict) -> bool:
    """Wait for *wait_seconds* then return (caller retries)."""
    wait_s = config.get("wait_seconds", 60)
    print(f"  [matcher] Waiting {wait_s}s before retry...")
    page.wait_for_timeout(int(wait_s * 1000))
    return True


def _action_refresh_and_retry(page, config: dict) -> bool:
    """Refresh the page and return (caller retries)."""
    print("  [matcher] Refreshing page...")
    try:
        page.reload(wait_until="domcontentloaded", timeout=15000)
    except Exception:
        try:
            page.reload(wait_until="commit", timeout=15000)
        except Exception:
            pass
    page.wait_for_timeout(3000)
    return True


def _action_abort(page, config: dict) -> bool:
    """Abort the flow — raise an exception with the configured message."""
    msg = config.get("reason", "Aborted by PageStateMatcher — unrecoverable page state")
    raise RuntimeError(msg)


def _action_navigate_to(page, config: dict) -> bool:
    """Navigate to a specific URL."""
    url = config.get("url", "")
    if not url:
        return False
    print(f"  [matcher] Navigating to: {url}")
    try:
        page.goto(url, wait_until="domcontentloaded", timeout=15000)
    except Exception:
        page.goto(url, wait_until="commit", timeout=15000)
    page.wait_for_timeout(3000)
    return True


# Map action names → implementations
_ACTION_REGISTRY: dict[str, Any] = {
    "click_continue_in_dialog": _action_click_continue_in_dialog,
    "click_button": _action_click_button,
    "wait_and_retry": _action_wait_and_retry,
    "refresh_and_retry": _action_refresh_and_retry,
    "abort": _action_abort,
    "navigate_to": _action_navigate_to,
}


# ── PageStateMatcher ─────────────────────────────────────────────────────────

class PageStateMatcher:
    """Load a keyword-template library and match page visible text against it."""

    def __init__(self, states_file: str = "page_states.json"):
        # Resolve path relative to this script's directory if not absolute
        self._states_path = Path(states_file)
        if not self._states_path.is_absolute():
            self._states_path = Path(__file__).parent / states_file

        self._threshold: float = 0.6
        self._templates: list[dict] = []
        self._load_states()

    # ── Public API ────────────────────────────────────────────────────────

    def identify(self, page) -> str | None:
        """Extract visible page text and return the best-matching template ID,
        or ``None`` if no template exceeds the confidence threshold."""
        if not self._templates:
            print("  [matcher] No templates loaded — skipping identification")
            return None

        # Extract visible text from the page
        try:
            page_text = page.inner_text("body")
        except Exception as e:
            print(f"  [matcher] inner_text() failed: {e}")
            return None

        if not page_text or not page_text.strip():
            print("  [matcher] Page text is empty — cannot identify")
            return None

        best_id: str | None = None
        best_score = 0.0

        for tpl in self._templates:
            tpl_id = tpl.get("id", "?")
            match_texts = tpl.get("match_texts", [])
            if not match_texts:
                print(f"  [matcher] Template '{tpl_id}' has no match_texts — skipping")
                continue

            score = compute_match_score(page_text, match_texts)
            print(f"  [matcher] Template '{tpl_id}' score={score:.2f} "
                  f"({sum(1 for t in match_texts if t.lower() in page_text.lower())}"
                  f"/{len(match_texts)})")

            if score > best_score:
                best_score = score
                best_id = tpl_id

        if best_id is not None and best_score > self._threshold:
            print(f"  [matcher] Matched '{best_id}' (score={best_score:.2f} "
                  f"> threshold={self._threshold})")
            return best_id
        else:
            if best_id is not None:
                print(f"  [matcher] Best match '{best_id}' below threshold "
                      f"(score={best_score:.2f} <= {self._threshold}) — returning None")
            else:
                print(f"  [matcher] No match found")
            return None

    def recover(self, page, template_id: str, **context) -> dict:
        """Execute the recovery action mapped to *template_id*.

        Returns a dict::

            {"recovered": bool, "resume_strategy": str}
        """
        tpl_meta = self._get_template(template_id)
        if tpl_meta is None:
            print(f"  [matcher] Unknown template: {template_id}")
            return {"recovered": False, "resume_strategy": "retry_current"}

        action_name = tpl_meta.get("action", "")
        action_config = tpl_meta.get("action_config", {})
        resume_strategy = tpl_meta.get("resume_strategy", "retry_current")
        action_fn = _ACTION_REGISTRY.get(action_name)

        if action_fn is None:
            print(f"  [matcher] Unknown action: {action_name}")
            return {"recovered": False, "resume_strategy": resume_strategy}

        print(f"  [matcher] Executing action '{action_name}' "
              f"for template '{template_id}'")
        try:
            ok = action_fn(page, action_config)
            if ok:
                print(f"  [matcher] Action '{action_name}' succeeded")
            else:
                print(f"  [matcher] Action '{action_name}' did not complete "
                      f"(all selectors exhausted)")
            return {"recovered": ok, "resume_strategy": resume_strategy}
        except Exception as e:
            print(f"  [matcher] Action '{action_name}' raised: {e}")
            import traceback
            traceback.print_exc()
            return {"recovered": False, "resume_strategy": resume_strategy}

    def identify_and_recover(self, page, **context) -> dict:
        """Identify the current page state and, if matched, execute the
        mapped recovery action.

        Returns a dict::

            {
                "matched": bool,           # template matched above threshold?
                "template_id": str | None, # which template
                "score": float,            # match score (0.0–1.0)
                "recovered": bool,         # action succeeded?
                "resume_strategy": str | None,  # retry_current / continue / abort
            }
        """
        tpl_id = self.identify(page)
        if tpl_id is None:
            return {
                "matched": False,
                "template_id": None,
                "score": 0.0,
                "recovered": False,
                "resume_strategy": None,
            }

        recovery = self.recover(page, tpl_id, **context)

        # Recompute score for the return value
        tpl_meta = self._get_template(tpl_id)
        match_texts = tpl_meta.get("match_texts", []) if tpl_meta else []
        try:
            page_text = page.inner_text("body")
        except Exception:
            page_text = ""
        score = compute_match_score(page_text, match_texts)

        return {
            "matched": True,
            "template_id": tpl_id,
            "score": score,
            "recovered": recovery["recovered"],
            "resume_strategy": recovery["resume_strategy"],
        }

    # ── Internal ──────────────────────────────────────────────────────────

    def _load_states(self):
        if not self._states_path.exists():
            print(f"  [matcher] States file not found: {self._states_path}")
            return
        try:
            with open(self._states_path, "r", encoding="utf-8") as f:
                data = json.load(f)
            self._templates = data.get("templates", [])
            self._threshold = data.get("threshold", 0.6)
            print(f"  [matcher] Loaded {len(self._templates)} template(s) "
                  f"from {self._states_path} (threshold={self._threshold})")
        except Exception as e:
            print(f"  [matcher] Failed to load states file: {e}")

    def _get_template(self, template_id: str) -> dict | None:
        for tpl in self._templates:
            if tpl.get("id") == template_id:
                return tpl
        return None

"""Turnstile detection, triggering, token waiting, and token injection.

This module is deliberately independent from Tkinter, TabPool, registration
flows, and any browser fingerprint changes. It only needs a page object with
the small DrissionPage surface used below.
"""

from __future__ import annotations

import time
from dataclasses import dataclass
from enum import Enum
from typing import Any, Callable


LogFn = Callable[[str], None]
CancelFn = Callable[[], bool]
SleepFn = Callable[[float], None]


class TurnstileStatus(str, Enum):
    """Terminal state returned by :meth:`TurnstileController.wait_for_token`."""

    SOLVED = "solved"
    NOT_PRESENT = "not_present"
    TIMEOUT = "timeout"
    CANCELLED = "cancelled"
    ERROR = "error"


@dataclass(frozen=True)
class TurnstilePolicy:
    """Timing and token rules for one controller instance."""

    timeout: float = 45.0
    poll_interval: float = 1.0
    trigger_interval: float = 1.0
    element_timeout: float = 0.3
    token_min_length: int = 80
    reset_on_start: bool = True

    def __post_init__(self) -> None:
        if self.timeout <= 0:
            raise ValueError("timeout must be greater than zero")
        if self.poll_interval <= 0:
            raise ValueError("poll_interval must be greater than zero")
        if self.trigger_interval < 0:
            raise ValueError("trigger_interval cannot be negative")
        if self.element_timeout < 0:
            raise ValueError("element_timeout cannot be negative")
        if self.token_min_length <= 0:
            raise ValueError("token_min_length must be greater than zero")


@dataclass(frozen=True)
class TurnstileSnapshot:
    """A point-in-time view of the widget and its response token."""

    present: bool
    token: str = ""

    @property
    def token_length(self) -> int:
        return len(self.token)


@dataclass(frozen=True)
class TurnstileResult:
    """Terminal result without exposing token contents in log messages."""

    status: TurnstileStatus
    token: str = ""
    elapsed: float = 0.0
    attempts: int = 0
    present: bool = False
    error: str = ""

    @property
    def ok(self) -> bool:
        return self.status is TurnstileStatus.SOLVED

    @property
    def token_length(self) -> int:
        return len(self.token)


_STATE_JS = r"""
try {
  const input = document.querySelector('input[name="cf-turnstile-response"]');
  const tokenFromInput = String((input && input.value) || '').trim();
  const present = !!input || !!document.querySelector(
    'iframe[src*="turnstile"], iframe[src*="challenges.cloudflare.com"], '
    + 'div.cf-turnstile, [data-sitekey], script[src*="turnstile"]'
  );
  let token = tokenFromInput;
  if (!token && window.turnstile && typeof window.turnstile.getResponse === 'function') {
    token = String(window.turnstile.getResponse() || '').trim();
  }
  return {present: present, token: token};
} catch (e) {
  return {present: false, token: ''};
}
"""

_RESET_JS = r"""
try {
  if (window.turnstile && typeof window.turnstile.reset === 'function') {
    window.turnstile.reset();
  }
  return true;
} catch (e) {
  return false;
}
"""

_INJECT_JS = r"""
const token = String(arguments[0] || '').trim();
const input = document.querySelector('input[name="cf-turnstile-response"]');
if (!input || !token) return 0;
const setter = Object.getOwnPropertyDescriptor(
  window.HTMLInputElement.prototype,
  'value'
)?.set;
if (setter) setter.call(input, token); else input.value = token;
input.dispatchEvent(new Event('input', {bubbles: true}));
input.dispatchEvent(new Event('change', {bubbles: true}));
return String(input.value || '').trim().length;
"""

_FALLBACK_TRIGGER_JS = r"""
const nodes = Array.from(document.querySelectorAll('div,span,iframe')).filter((node) => {
  const text = (node.className || '') + ' ' + (node.id || '') + ' '
    + (node.getAttribute?.('src') || '');
  return String(text).toLowerCase().includes('turnstile');
});
const target = nodes.find((node) => typeof node.click === 'function');
if (!target) return false;
target.click();
return true;
"""


class TurnstileController:
    """Control one Turnstile widget on an already-created browser page.

    ``page`` is intentionally supplied by the caller. This keeps browser
    lifecycle, tabs, profiles, and application-specific retries outside the
    reusable module.
    """

    def __init__(
        self,
        page: Any,
        *,
        policy: TurnstilePolicy | None = None,
        log: LogFn | None = None,
        cancel: CancelFn | None = None,
        sleep: SleepFn | None = None,
    ) -> None:
        if page is None:
            raise ValueError("page is required")
        self.page = page
        self.policy = policy or TurnstilePolicy()
        self.log = log or (lambda _message: None)
        self.cancel = cancel
        self.sleep = sleep or time.sleep

    def inspect(self) -> TurnstileSnapshot:
        """Read widget presence and the current response token."""

        raw = self.page.run_js(_STATE_JS)
        if not isinstance(raw, dict):
            return TurnstileSnapshot(present=False)
        token = str(raw.get("token") or "").strip()
        return TurnstileSnapshot(present=bool(raw.get("present")), token=token)

    def reset(self) -> bool:
        """Best-effort reset of the page's Turnstile instance."""

        try:
            return bool(self.page.run_js(_RESET_JS))
        except Exception as exc:  # page implementations differ here
            self.log(f"turnstile reset skipped: {exc}")
            return False

    def inject_token(self, token: str) -> int:
        """Write a token into the page and return the resulting length."""

        value = str(token or "").strip()
        if not value:
            return 0
        result = self.page.run_js(_INJECT_JS, value)
        try:
            return int(result or 0)
        except (TypeError, ValueError):
            return 0

    def trigger(self) -> bool:
        """Try the Turnstile shadow checkbox, then a DOM fallback click."""

        try:
            challenge_input = self._find_element("@name=cf-turnstile-response")
            if challenge_input is not None:
                wrapper = challenge_input.parent()
                iframe = wrapper.shadow_root.ele("tag:iframe")
                if iframe is not None:
                    body = iframe.ele("tag:body")
                    body_shadow = body.shadow_root
                    checkbox = body_shadow.ele("tag:input")
                    if checkbox is not None:
                        checkbox.click()
                        self.log("turnstile shadow checkbox clicked")
                        return True
        except Exception as exc:
            self.log(f"turnstile shadow click skipped: {exc}")

        try:
            clicked = bool(self.page.run_js(_FALLBACK_TRIGGER_JS))
            if clicked:
                self.log("turnstile container clicked")
            return clicked
        except Exception as exc:
            self.log(f"turnstile fallback click skipped: {exc}")
            return False

    def wait_for_token(
        self,
        *,
        timeout: float | None = None,
        require_present: bool = True,
    ) -> TurnstileResult:
        """Wait until a usable token is available.

        When ``require_present`` is false, a page without a Turnstile widget is
        returned as ``NOT_PRESENT`` after the first inspection. When it is true
        (the default), late-rendering widgets are allowed to appear until the
        timeout expires.
        """

        limit = float(timeout if timeout is not None else self.policy.timeout)
        if limit <= 0:
            raise ValueError("timeout must be greater than zero")

        started = time.monotonic()
        deadline = started + limit
        attempts = 0
        seen_present = False
        last_trigger = float("-inf")
        last_error = ""

        if self.policy.reset_on_start:
            self.reset()

        while True:
            if self._is_cancelled():
                return self._result(
                    TurnstileStatus.CANCELLED,
                    started=started,
                    attempts=attempts,
                    present=seen_present,
                )

            now = time.monotonic()
            if now >= deadline:
                break
            attempts += 1

            try:
                snapshot = self.inspect()
                seen_present = seen_present or snapshot.present
                if len(snapshot.token) >= self.policy.token_min_length:
                    self.log(f"turnstile solved token_length={len(snapshot.token)}")
                    return self._result(
                        TurnstileStatus.SOLVED,
                        token=snapshot.token,
                        started=started,
                        attempts=attempts,
                        present=seen_present,
                    )
            except Exception as exc:
                if not last_error:
                    self.log(f"turnstile inspection failed: {exc}")
                last_error = str(exc)
                snapshot = None

            if snapshot is not None and not snapshot.present and not require_present:
                return self._result(
                    TurnstileStatus.NOT_PRESENT,
                    started=started,
                    attempts=attempts,
                    present=seen_present,
                )

            now = time.monotonic()
            if (
                snapshot is not None
                and snapshot.present
                and now - last_trigger >= self.policy.trigger_interval
            ):
                self.trigger()
                last_trigger = now

            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            self.sleep(min(self.policy.poll_interval, remaining))

        status = TurnstileStatus.ERROR if last_error and not seen_present else (
            TurnstileStatus.NOT_PRESENT if not seen_present else TurnstileStatus.TIMEOUT
        )
        return self._result(
            status,
            started=started,
            attempts=attempts,
            present=seen_present,
            error=last_error,
        )

    def _find_element(self, locator: str) -> Any:
        try:
            return self.page.ele(locator, timeout=self.policy.element_timeout)
        except TypeError:
            return self.page.ele(locator)

    def _is_cancelled(self) -> bool:
        return bool(self.cancel and self.cancel())

    @staticmethod
    def _result(
        status: TurnstileStatus,
        *,
        started: float,
        token: str = "",
        attempts: int = 0,
        present: bool = False,
        error: str = "",
    ) -> TurnstileResult:
        return TurnstileResult(
            status=status,
            token=token,
            elapsed=max(0.0, time.monotonic() - started),
            attempts=attempts,
            present=present,
            error=error,
        )

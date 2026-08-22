"""Reusable Cloudflare Turnstile page controller.

The package intentionally does not own a browser lifecycle. Pass it a page
object that provides DrissionPage's ``run_js()`` and ``ele()`` methods.
"""

from .browser import add_turnstile_extension, turnstile_extension_path
from .controller import (
    TurnstileController,
    TurnstilePolicy,
    TurnstileResult,
    TurnstileSnapshot,
    TurnstileStatus,
)

__all__ = [
    "TurnstileController",
    "TurnstilePolicy",
    "TurnstileResult",
    "TurnstileSnapshot",
    "TurnstileStatus",
    "add_turnstile_extension",
    "turnstile_extension_path",
]

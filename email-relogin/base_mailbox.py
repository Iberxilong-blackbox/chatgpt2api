"""Mailbox abstractions for zhuce6."""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
import re
from typing import Any

OTP_PATTERN = re.compile(r"\b(\d{6})\b")

# Keywords that indicate an OTP code nearby — prefer codes found close to these.
_OTP_CONTEXT_RE = re.compile(
    r"(?:verification|verif|verify|otp|one.time|security)\s*(?:code|password|pin|number)",
    re.IGNORECASE,
)
# Generic "code is" pattern
_CODE_IS_RE = re.compile(r"(?:code|code\s*is|code\s*:)\s*(\d{6})", re.IGNORECASE)


@dataclass
class MailboxAccount:
    email: str
    account_id: str = ""
    extra: dict[str, Any] = field(default_factory=dict)


class BaseMailbox(ABC):
    @abstractmethod
    def get_email(self) -> MailboxAccount:
        """Create or reserve an email inbox."""

    @abstractmethod
    def wait_for_code(
        self,
        account: MailboxAccount,
        keyword: str = "",
        timeout: int = 120,
        before_ids: set[str] | None = None,
    ) -> str:
        """Poll for a 6-digit code."""

    @abstractmethod
    def get_current_ids(self, account: MailboxAccount) -> set[str]:
        """Return the current known message ids."""


# Pre-compiled HTML tag stripper (used by _extract_code to avoid matching
# CSS colour codes like #202123 inside style attributes).
_HTML_TAG_RE = re.compile(r"<[^>]+>")


def _extract_code(content: str, *, sender: str = "", keyword: str = "") -> str:
    """Extract a 6-digit verification code from email content.

    Strips HTML tags first so that CSS colour codes (e.g. ``#202123``)
    inside ``style`` attributes are not mistaken for OTP digits.
    """
    # Strip HTML so CSS colour codes like #202123 are removed
    plain_text = _HTML_TAG_RE.sub(" ", content)
    normalized_content = plain_text.lower()
    normalized_sender = sender.lower()
    if keyword:
        target = keyword.lower()
        if target not in normalized_content and target not in normalized_sender:
            return ""
    elif not any(
        kw in normalized_content or kw in normalized_sender
        for kw in ("openai", "chatgpt")
    ):
        return ""

    # 1. Preferred: "code is XXXXXX" / "code: XXXXXX" near OTP context
    m = _CODE_IS_RE.search(plain_text)
    if m:
        code = m.group(1)
        start = max(0, m.start() - 200)
        end = min(len(normalized_content), m.end() + 20)
        surrounding = normalized_content[start:end]
        if _OTP_CONTEXT_RE.search(surrounding) or any(
            w in surrounding for w in ("verification", "verify", "otp", "one-time")
        ):
            return code
        if keyword:
            return code

    # 2. Fallback: first 6-digit number in the plain text
    match = OTP_PATTERN.search(plain_text)
    return match.group(1) if match else ""


def create_mailbox(provider: str, proxy: str | None = None) -> BaseMailbox:
    """Factory: return a mailbox instance for *provider*.

    Currently only ``"zoho"`` is supported.  Other providers (mailtm,
    cfmail, tempmaillol, guerrillamail, etc.) have been moved to
    ``archive/core/`` — restore them from there if needed.
    """
    provider_key = provider.strip().lower()
    if provider_key == "zoho":
        import importlib.util
        from pathlib import Path as _Path

        _zoho_path = _Path(__file__).resolve().parent / "mailbox.py"
        _spec = importlib.util.spec_from_file_location("zoho_mailbox", _zoho_path)
        _mod = importlib.util.module_from_spec(_spec)
        _spec.loader.exec_module(_mod)
        return _mod.ZohoMailbox(proxy=proxy)
    raise ValueError(
        f"Unsupported mailbox provider: {provider!r}. "
        f"Only 'zoho' is currently active. "
        f"Other providers (mailtm, cfmail, tempmaillol, etc.) "
        f"have been archived to archive/core/."
    )

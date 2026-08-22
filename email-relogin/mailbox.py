"""Zoho Mail IMAP catch-all mailbox — POC implementation of BaseMailbox."""

from __future__ import annotations

import email as email_parser
import email.message
import imaplib
import json
import logging
import os
import re
import secrets
import threading
import time
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any

try:
    from faker import Faker as _Faker
    from faker.exceptions import UniquenessException as _UniquenessException
except ImportError:  # pragma: no cover
    _Faker = None
    _UniquenessException = None

# Reuse the colocated mailbox abstractions and OTP extractor.
from base_mailbox import BaseMailbox, MailboxAccount, _extract_code

logger = logging.getLogger("zoho_mailbox")

_THIS_DIR = Path(__file__).resolve().parent

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_MONTH_ABBRS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun",
                "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]


def _imap_date(dt: datetime) -> str:
    """Return IMAP-compatible date string (DD-Mon-YYYY in English)."""
    return f"{dt.day:02d}-{_MONTH_ABBRS[dt.month - 1]}-{dt.year}"


def _load_jsonc_config(filepath: str = "config.jsonc") -> dict[str, Any]:
    """Lightweight JSONC loader — strips // and /* */ comments."""
    with open(filepath, "r", encoding="utf-8") as fh:
        content = fh.read()
    content = re.sub(r"//.*", "", content)
    content = re.sub(r"/\*.*?\*/", "", content, flags=re.S)
    return json.loads(content)


def _decode_email_body(msg: email.message.Message) -> str:
    """Extract plain-text body from an email.Message, falling back to HTML."""
    body = ""
    if msg.is_multipart():
        for part in msg.walk():
            ct = part.get_content_type()
            cd = str(part.get("Content-Disposition") or "")
            if ct == "text/plain" and "attachment" not in cd:
                payload = part.get_payload(decode=True)
                if payload:
                    body = payload.decode(errors="ignore")
                break

    if not body.strip():
        body = _decode_payload(msg)

    # If still empty, try HTML parts
    if not body.strip() and msg.is_multipart():
        for part in msg.walk():
            if part.get_content_type() == "text/html":
                payload = part.get_payload(decode=True)
                if payload:
                    html = payload.decode(errors="ignore")
                    body = re.sub(r"<[^>]+>", " ", html)
                    body = re.sub(r"\s+", " ", body)
                    break

    return body


def _decode_payload(msg: email.message.Message) -> str:
    payload = msg.get_payload(decode=True)
    if payload:
        return payload.decode(errors="ignore")
    return str(msg.get_payload() or "")


# ---------------------------------------------------------------------------
# ZohoMailbox
# ---------------------------------------------------------------------------

class ZohoMailbox(BaseMailbox):
    """IMAP-based Zoho Mail catch-all mailbox.

    Connects to a single Zoho admin inbox via IMAP SSL.  Incoming mail for
    *any* alias under the catch-all domain lands in this inbox, so
    ``wait_for_code`` matches by ``To`` header.

    Connection is lazy and shared (lock-protected) across threads.
    """

    # Class-level alias tracking with persistent deduplication.
    # _used_aliases is the in-memory mirror of email_history.txt.
    # The file lives next to this module so all processes / restarts
    # share the same dedup source of truth.
    _used_aliases: set[str] = set()
    _used_aliases_lock = threading.Lock()
    _history_loaded = False
    _history_path = _THIS_DIR / "email_history.txt"
    _faker: Any = None
    _faker_lock = threading.Lock()

    # Folders EXCLUDED from verification code search (outgoing / deleted / system).
    # Everything else on the server will be searched.
    _EXCLUDED_FOLDERS: frozenset[str] = frozenset({
        "Sent", "Drafts", "Trash", "Archive", "Templates",
        "[Gmail]/Sent Mail", "[Gmail]/Drafts", "[Gmail]/Trash",
    })

    def __init__(
        self,
        proxy: str | None = None,
        *,
        imap_server: str | None = None,
        imap_port: int | None = None,
        admin_email: str | None = None,
        admin_password: str | None = None,
        catch_all_domain: str | None = None,
        config_path: str | None = None,
        auto_cleanup: bool = True,
    ) -> None:
        _ = proxy  # IMAP does not use HTTP proxy

        # Resolve config — env vars > constructor args > config.jsonc
        config: dict[str, Any] = {}
        _cfg_path = config_path or str(_THIS_DIR / "config.jsonc")
        if os.path.exists(_cfg_path):
            config = _load_jsonc_config(_cfg_path)

        self._imap_server = (
            imap_server
            or os.getenv("ZHUCE6_ZOHO_IMAP_SERVER")
            or str(config.get("imap_server", "imappro.zoho.com"))
        )
        self._imap_port = (
            imap_port
            or int(os.getenv("ZHUCE6_ZOHO_IMAP_PORT", "0"))
            or int(config.get("imap_port", 993))
        )
        self._admin_email = (
            admin_email
            or os.getenv("ZHUCE6_ZOHO_EMAIL")
            or str(config.get("email_address", ""))
        )
        self._admin_password = (
            admin_password
            or os.getenv("ZHUCE6_ZOHO_PASSWORD")
            or str(config.get("password", ""))
        )
        self._domain = (
            catch_all_domain
            or os.getenv("ZHUCE6_ZOHO_DOMAIN")
            or self._admin_email.split("@")[-1]
            if "@" in self._admin_email
            else ""
        )

        if not self._admin_email or not self._admin_password:
            raise RuntimeError(
                "ZohoMailbox requires admin_email and admin_password. "
                "Set via constructor, ZHUCE6_ZOHO_EMAIL/ZHUCE6_ZOHO_PASSWORD env vars, "
                "or config.jsonc."
            )

        self._conn: imaplib.IMAP4_SSL | None = None
        self._conn_lock = threading.Lock()
        self._last_cleanup_errors: dict[str, str] = {}

        # Discover all available folders (populated by _resolve_folders)
        self._active_folders: list[str] = ["INBOX"]
        self._resolve_folders()

        # Cleanup old emails on startup (best-effort, all folders).
        if auto_cleanup:
            self.cleanup_old_emails(days=1)

    def _resolve_folders(self) -> None:
        """Discover ALL available IMAP folders, excluding outgoing/trash/system ones.

        Zoho auto-classifies incoming mail into categories like INBOX, Spam,
        Notification, Forums, Updates, Promotions, etc.  We search EVERYTHING
        except Sent/Drafts/Trash/Archive/Templates so no verification email is
        missed regardless of which category Zoho assigns it to.
        """
        try:
            def _do(conn):
                status, data = conn.list()
                if status != "OK":
                    return
                all_folders: list[str] = []
                for line in data:
                    if isinstance(line, bytes):
                        line_str = line.decode(errors="ignore")
                        # IMAP LIST response: * LIST (...) "/" "FolderName"
                        parts = line_str.rsplit('"', 2)
                        if len(parts) >= 2:
                            folder = parts[-2]
                            if folder not in self._EXCLUDED_FOLDERS:
                                all_folders.append(folder)
                # Ensure INBOX is first (most likely location)
                if "INBOX" in all_folders:
                    all_folders.remove("INBOX")
                    all_folders.insert(0, "INBOX")
                self._active_folders = all_folders
                if not self._active_folders:
                    self._active_folders = ["INBOX"]
                logger.info("active search folders: %s", self._active_folders)
            self._with_retry(_do)
        except Exception:
            self._active_folders = ["INBOX"]

    # ---- connection management -------------------------------------------

    def _ensure_connection(self) -> imaplib.IMAP4_SSL:
        """Return a healthy IMAP connection (lazy init, auto-reconnect)."""
        with self._conn_lock:
            if self._conn is not None:
                try:
                    # Quick no-op to test if connection is alive
                    self._conn.noop()
                    return self._conn
                except Exception:
                    self._conn = None
            self._conn = imaplib.IMAP4_SSL(self._imap_server, self._imap_port)
            self._conn.login(self._admin_email, self._admin_password)
            return self._conn

    def _with_retry(self, fn, *args, **kwargs):
        """Call fn(conn, *args, **kwargs) with one retry on connection error."""
        try:
            conn = self._ensure_connection()
            return fn(conn, *args, **kwargs)
        except (imaplib.IMAP4.abort, imaplib.IMAP4.error, ConnectionError, OSError):
            with self._conn_lock:
                self._conn = None
            conn = self._ensure_connection()
            return fn(conn, *args, **kwargs)

    # ---- cleanup ---------------------------------------------------------

    def cleanup_old_emails(self, days: int = 1, folders: list[str] | None = None) -> dict[str, int]:
        """Delete emails older than `days`.

        When *folders* is given, only those folder names are targeted;
        otherwise, ALL active folders are cleaned.

        Returns a dict mapping folder name → number of emails deleted.
        A count of -1 means the folder errored out (best-effort).
        """
        results: dict[str, int] = {}
        self._last_cleanup_errors = {}
        target_folders = folders or self._active_folders
        try:
            def _do(conn):
                cutoff = _imap_date(datetime.now() - timedelta(days=days))
                for folder in target_folders:
                    try:
                        select_status, select_response = conn.select(folder, readonly=False)
                        if select_status != "OK":
                            raise RuntimeError(f"SELECT failed: {select_response!r}")
                        status, response = conn.uid("SEARCH", None, f"BEFORE {cutoff}")
                        if status != "OK":
                            raise RuntimeError(f"SEARCH BEFORE {cutoff} failed: {response!r}")
                        if not response[0]:
                            results[folder] = 0
                            continue
                        old_uids = response[0].decode().split()
                        if not old_uids:
                            results[folder] = 0
                            continue
                        count = len(old_uids)
                        for uid_batch in _chunk(old_uids, 50):
                            uid_str = ",".join(uid_batch)
                            store_status, store_response = conn.uid("STORE", uid_str, "+FLAGS", "(\\Deleted)")
                            if store_status != "OK":
                                raise RuntimeError(f"STORE deleted flag failed for {uid_str}: {store_response!r}")
                        expunge_status, expunge_response = conn.expunge()
                        if expunge_status != "OK":
                            raise RuntimeError(f"EXPUNGE failed: {expunge_response!r}")
                        results[folder] = count
                        if count > 0:
                            logger.info("cleanup: deleted %d emails from %s", count, folder)
                    except Exception as exc:
                        logger.debug("cleanup: folder %s error: %s", folder, exc)
                        results[folder] = -1
                        self._last_cleanup_errors[folder] = str(exc)
            self._with_retry(_do)
        except Exception as exc:
            logger.debug("cleanup: overall error: %s", exc)
            self._last_cleanup_errors["<overall>"] = str(exc)
        return results

    def scan_folder_counts(self, days: int = 0) -> dict[str, dict[str, Any]]:
        """Read-only folder scan: total messages and messages older than cutoff."""
        results: dict[str, dict[str, Any]] = {}

        def _do(conn):
            cutoff = _imap_date(datetime.now() - timedelta(days=days))
            for folder in self._active_folders:
                try:
                    select_status, select_response = conn.select(folder, readonly=True)
                    if select_status != "OK":
                        raise RuntimeError(f"SELECT failed: {select_response!r}")

                    all_status, all_response = conn.uid("SEARCH", None, "ALL")
                    if all_status != "OK":
                        raise RuntimeError(f"SEARCH ALL failed: {all_response!r}")
                    total = len(all_response[0].decode().split()) if all_response and all_response[0] else 0

                    old_status, old_response = conn.uid("SEARCH", None, f"BEFORE {cutoff}")
                    if old_status != "OK":
                        raise RuntimeError(f"SEARCH BEFORE {cutoff} failed: {old_response!r}")
                    old = len(old_response[0].decode().split()) if old_response and old_response[0] else 0

                    results[folder] = {"total": total, "old": old, "cutoff": cutoff}
                except Exception as exc:
                    results[folder] = {"total": -1, "old": -1, "cutoff": cutoff, "error": str(exc)}

        self._with_retry(_do)
        return results

    # ---- BaseMailbox interface -------------------------------------------

    @classmethod
    def _get_faker(cls) -> Any:
        """Lazy-init a shared Faker instance (thread-safe)."""
        if cls._faker is None:
            with cls._faker_lock:
                if cls._faker is None and _Faker is not None:
                    cls._faker = _Faker()
        return cls._faker

    @classmethod
    def _load_history(cls) -> None:
        """Load previously-generated emails from persistent history file.

        Called once per process lifetime (idempotent).  Merges the on-disk
        list into ``_used_aliases`` so dedup survives restarts.
        """
        if cls._history_loaded:
            return
        cls._history_loaded = True
        try:
            if cls._history_path.exists():
                with open(cls._history_path, "r", encoding="utf-8") as fh:
                    for line in fh:
                        addr = line.strip()
                        if addr:
                            cls._used_aliases.add(addr)
                logger.info("Loaded %d email aliases from history", len(cls._used_aliases))
        except Exception as exc:
            logger.warning("Failed to load email history: %s", exc)

    @classmethod
    def _persist_email(cls, email_addr: str) -> None:
        """Append a newly-generated email to the history file immediately.

        The atomic append + flush guarantees that even if the process
        crashes later, this email address will never be reused.
        """
        try:
            cls._history_path.parent.mkdir(parents=True, exist_ok=True)
            with open(cls._history_path, "a", encoding="utf-8") as fh:
                fh.write(email_addr + "\n")
                fh.flush()
                os.fsync(fh.fileno())
        except Exception as exc:
            logger.warning("Failed to persist email to history: %s", exc)

    def get_email(self, persist: bool = True) -> MailboxAccount:
        """Generate a human-like random alias under the catch-all domain.

        Uses Faker to produce realistic local-parts (e.g. ``john.smith``,
        ``lisa.jones``) and falls back to token_hex when Faker is unavailable
        or has exhausted its unique pool.

        Deduplication is backed by ``email_history.txt`` so no two emails
        are ever reused — even across process restarts or reboots.

        When *persist* is False the email is only tracked in memory for this
        process lifetime; the caller is responsible for calling
        ``persist_email()`` once the registration succeeds.  This avoids
        burning an email address on a failed attempt.
        """
        with self._used_aliases_lock:
            self._load_history()

            faker = self._get_faker()
            if faker is not None:
                for _ in range(500):
                    try:
                        fake_email = faker.unique.free_email()
                    except (_UniquenessException if _UniquenessException is not None else Exception):  # type: ignore[misc]
                        logger.info("Faker unique pool exhausted, switching to hex fallback")
                        break
                    local_part = fake_email.split("@")[0]
                    email_addr = f"{local_part}@{self._domain}"
                    if email_addr not in self._used_aliases:
                        self._used_aliases.add(email_addr)
                        if persist:
                            self._persist_email(email_addr)
                        return MailboxAccount(
                            email=email_addr,
                            account_id=email_addr,
                            extra={"provider": "zoho", "domain": self._domain},
                        )

            # Fallback: random hex aliases (Faker unavailable or exhausted)
            for _ in range(100):
                alias = f"zhuce6_{secrets.token_hex(6)}"
                email_addr = f"{alias}@{self._domain}"
                if email_addr not in self._used_aliases:
                    self._used_aliases.add(email_addr)
                    if persist:
                        self._persist_email(email_addr)
                    return MailboxAccount(
                        email=email_addr,
                        account_id=email_addr,
                        extra={"provider": "zoho", "domain": self._domain},
                    )
            # Final fallback: extremely unlikely collision loop exhausted
            alias = f"zhuce6_{secrets.token_hex(8)}"
            email_addr = f"{alias}@{self._domain}"
            self._used_aliases.add(email_addr)
            if persist:
                self._persist_email(email_addr)
            return MailboxAccount(
                email=email_addr,
                account_id=email_addr,
                extra={"provider": "zoho", "domain": self._domain},
            )

    def persist_email(self, email_addr: str) -> None:
        """Public hook: persist a previously-generated email to the history file.

        Call this AFTER a registration succeeds so the email address is
        permanently recorded and never reused.  Safe to call multiple times
        (the write is idempotent — subsequent lines in the file are harmless).
        """
        self._persist_email(email_addr)

    @staticmethod
    def _uid_with_folder(folder: str, uid: str) -> str:
        """Namespace a UID by folder to avoid collisions across mailboxes."""
        return f"{folder}:{uid}"

    def get_current_ids(self, account: MailboxAccount) -> set[str]:
        """Return all current email UIDs across all active folders (namespaced)."""
        _ = account

        def _do(conn):
            all_ids: set[str] = set()
            for folder in self._active_folders:
                try:
                    conn.select(folder, readonly=True)
                    status, response = conn.uid("SEARCH", None, "ALL")
                    if status == "OK" and response[0]:
                        for uid in response[0].decode().split():
                            all_ids.add(self._uid_with_folder(folder, uid))
                except Exception:
                    pass  # folder may not exist or be unselectable
            return all_ids

        try:
            return self._with_retry(_do)
        except Exception:
            return set()

    def scan_existing_codes(
        self,
        keyword: str = "",
        max_emails: int = 50,
    ) -> list[dict[str, str]]:
        """Scan existing emails across all active folders for verification codes.

        Returns a list of dicts: folder, sender, subject, date, code, to.
        Useful for diagnosing where verification emails land.
        """
        results: list[dict[str, str]] = []

        def _do(conn):
            for folder in self._active_folders:
                try:
                    conn.select(folder, readonly=True)
                    status, response = conn.uid("SEARCH", None, "ALL")
                    if status != "OK" or not response[0]:
                        continue
                    uids = response[0].decode().split()
                    # Search most recent emails first
                    for uid in sorted(uids, key=int, reverse=True)[:max_emails]:
                        fetch_status, fetch_data = conn.uid("FETCH", uid, "(RFC822)")
                        if fetch_status != "OK":
                            continue
                        raw_email = _extract_raw_email(fetch_data)
                        if raw_email is None:
                            continue
                        msg = email_parser.message_from_bytes(raw_email)
                        sender = str(msg.get("From") or "")
                        subject = str(msg.get("Subject") or "")
                        date = str(msg.get("Date") or "")
                        to_addr = str(msg.get("To") or "")
                        body = _decode_email_body(msg)
                        content = f"{subject}\n{body}"
                        code = _extract_code(content, sender=sender, keyword=keyword)
                        if code:
                            results.append({
                                "folder": folder,
                                "sender": sender,
                                "subject": subject,
                                "date": date,
                                "code": code,
                                "to": to_addr,
                            })
                except Exception:
                    continue

        self._with_retry(_do)
        return results

    def wait_for_code(
        self,
        account: MailboxAccount,
        keyword: str = "",
        timeout: int = 120,
        before_ids: set[str] | None = None,
    ) -> str:
        """Poll IMAP inbox until a verification code arrives for *account.email*."""
        target_email = account.email.lower()
        seen_uids = set(before_ids or set())
        deadline = time.time() + timeout

        while time.time() < deadline:
            try:
                code = self._poll_once(target_email, seen_uids, keyword)
                if code:
                    return code
            except Exception:
                pass
            time.sleep(3)

        return ""

    def _poll_once(
        self, target_email: str, seen_uids: set[str], keyword: str
    ) -> str | None:
        """One polling iteration — check all active folders for new mail."""

        def _do(conn):
            for folder in self._active_folders:
                try:
                    code = self._poll_folder(conn, folder, target_email, seen_uids, keyword)
                    if code:
                        return code
                except Exception:
                    continue
            return None

        return self._with_retry(_do)

    def _poll_folder(
        self,
        conn: imaplib.IMAP4_SSL,
        folder: str,
        target_email: str,
        seen_uids: set[str],
        keyword: str,
    ) -> str | None:
        try:
            conn.select(folder, readonly=True)
        except Exception:
            return None

        status, response = conn.uid("SEARCH", None, "ALL")
        if status != "OK":
            return None

        current_uids = set(response[0].decode().split()) if response[0] else set()

        new_count = len(current_uids - {u.split(":", 1)[1] for u in seen_uids if u.startswith(f"{folder}:")})
        if new_count:
            logger.debug("folder=%s total=%d new=%d target=%s",
                         folder, len(current_uids), new_count, target_email)

        for uid in sorted(current_uids, key=int):
            namespaced = self._uid_with_folder(folder, uid)
            if namespaced in seen_uids:
                continue
            seen_uids.add(namespaced)

            fetch_status, fetch_data = conn.uid("FETCH", uid, "(RFC822)")
            if fetch_status != "OK":
                continue

            raw_email = _extract_raw_email(fetch_data)
            if raw_email is None:
                continue

            msg = email_parser.message_from_bytes(raw_email)

            to_hdr = str(msg.get("To") or "").lower()
            sender = str(msg.get("From") or "")
            subject = str(msg.get("Subject") or "")

            if target_email not in to_hdr:
                logger.debug("folder=%s SKIP To mismatch: To=%s target=%s subject=%s",
                             folder, to_hdr[:100], target_email, subject[:80])
                continue

            logger.info("folder=%s MATCHED To: sender=%s subject=%s",
                        folder, sender, subject[:80])

            body = _decode_email_body(msg)
            content = f"{subject}\n{body}"

            code = _extract_code(content, sender=sender, keyword=keyword)
            if code:
                logger.info("folder=%s CODE FOUND: %s", folder, code)
                # Diagnostic: save matched email to disk for inspection
                _diagnostic_dump(subject, sender, body, code, target_email, folder)
                return code
            else:
                logger.debug("folder=%s To matched but no code: sender=%s subject=%s body_preview=%s",
                             folder, sender, subject[:80], body[:120])

        return None


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _extract_raw_email(fetch_data: list[Any]) -> bytes | None:
    """Extract raw RFC822 bytes from an imaplib FETCH response."""
    if not fetch_data:
        return None
    for item in fetch_data:
        if isinstance(item, tuple) and len(item) >= 2:
            payload = item[1]
            if isinstance(payload, bytes):
                return payload
    return None


def _chunk(items: list[str], size: int) -> list[list[str]]:
    return [items[i:i + size] for i in range(0, len(items), size)]


def _diagnostic_dump(
    subject: str,
    sender: str,
    body: str,
    code: str,
    target: str,
    folder: str,
) -> None:
    """Write matched email to disk so we can inspect the raw content."""
    import time as _time
    from pathlib import Path as _Path

    dump_dir = _Path(__file__).resolve().parent.parent / "debug_output"
    dump_dir.mkdir(parents=True, exist_ok=True)
    ts = _time.strftime("%Y%m%d_%H%M%S")
    slug = target.split("@")[0][:20]
    fname = f"otp_match_{ts}_{slug}.txt"
    fpath = dump_dir / fname
    with open(fpath, "w", encoding="utf-8") as f:
        f.write(f"Target:  {target}\n")
        f.write(f"Folder:  {folder}\n")
        f.write(f"Sender:  {sender}\n")
        f.write(f"Subject: {subject}\n")
        f.write(f"Code extracted: {code}\n")
        f.write(f"{'=' * 60}\n")
        f.write(body)
    print(f"[DIAG] OTP email dumped to: {fpath}", flush=True)


# ---------------------------------------------------------------------------
# CLI entry point (python zoho-mail/mailbox.py --cleanup)
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(
        description="Zoho Mail mailbox cleanup — delete old verification emails",
    )
    parser.add_argument(
        "--cleanup",
        action="store_true",
        help="Delete emails older than --days (default: 1) across ALL folders",
    )
    parser.add_argument(
        "--days",
        type=int,
        default=1,
        help="Retention period in days (default: 1)",
    )
    parser.add_argument(
        "--scan",
        action="store_true",
        help="Scan and show email counts per folder without deleting",
    )
    parser.add_argument(
        "--interval",
        type=int,
        default=0,
        help="Run cleanup every N seconds (0 = once, then exit)",
    )
    parser.add_argument(
        "--notification-only",
        action="store_true",
        help="Only clean the Notification folder",
    )
    args = parser.parse_args()

    if not args.cleanup and not args.scan:
        parser.print_help()
        print("\nExamples:")
        print("  python zoho-mail/mailbox.py --cleanup                                # delete >1 day old emails")
        print("  python zoho-mail/mailbox.py --cleanup --days 3                       # delete >3 days old emails")
        print("  python zoho-mail/mailbox.py --scan                                   # show folder stats, no delete")
        print("  python zoho-mail/mailbox.py --cleanup --interval 3600                # cleanup every hour")
        print("  python zoho-mail/mailbox.py --cleanup --days 0 --notification-only   # delete Notification except today")
        raise SystemExit(0)

    mb = ZohoMailbox(auto_cleanup=False)
    print(f"Server:   {mb._imap_server}:{mb._imap_port}")
    print(f"Account:  {mb._admin_email}")
    print(f"Domain:   {mb._domain}")
    print(f"Folders:  {mb._active_folders}")
    print()

    if args.scan:
        print(f"Scanning email counts per folder (read-only, old = BEFORE {_imap_date(datetime.now() - timedelta(days=args.days))})...")
        scan_results = mb.scan_folder_counts(days=args.days)
        for folder, info in scan_results.items():
            if "error" in info:
                print(f"  {folder:30s}  ERROR: {info['error']}")
            else:
                print(f"  {folder:30s}  total={info['total']:5d}  old={info['old']:5d}")
        print("\n(No emails deleted — scan only)")

    target_folders = ["Notification"] if args.notification_only else None
    folder_label = "Notification" if args.notification_only else "all folders"

    if args.cleanup:
        if args.interval > 0:
            import time as _time
            print(f"Periodic cleanup every {args.interval}s ({folder_label}, Ctrl+C to stop)...")
            while True:
                results = mb.cleanup_old_emails(days=args.days, folders=target_folders)
                total = sum(max(0, c) for c in results.values())
                errors = sum(1 for c in results.values() if c < 0)
                ts = _time.strftime("%Y-%m-%d %H:%M:%S")
                print(f"[{ts}] Deleted {total} emails across {len(results)} folders"
                      + (f" ({errors} errors)" if errors else ""))
                for folder, count in results.items():
                    if count < 0:
                        print(f"        {folder}: ERROR: {mb._last_cleanup_errors.get(folder, 'unknown error')}")
                    elif count > 0:
                        print(f"        {folder}: deleted {count}")
                    else:
                        print(f"        {folder}: deleted 0")
                _time.sleep(args.interval)
        else:
            print(f"Deleting emails older than {args.days} day(s) from {folder_label}...")
            results = mb.cleanup_old_emails(days=args.days, folders=target_folders)
            total = sum(max(0, c) for c in results.values())
            errors = sum(1 for c in results.values() if c < 0)
            print(f"Done. Deleted {total} emails across {len(results)} folders"
                  + (f" ({errors} errors)" if errors else ""))
            for folder, count in results.items():
                if count < 0:
                    print(f"  {folder}: ERROR: {mb._last_cleanup_errors.get(folder, 'unknown error')}")
                elif count > 0:
                    print(f"  {folder}: deleted {count}")
                else:
                    print(f"  {folder}: deleted 0")

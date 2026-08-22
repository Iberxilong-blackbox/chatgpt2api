"""Optional browser integration helpers for the bundled extension."""

from __future__ import annotations

from pathlib import Path
from typing import Any


_EXTENSION_DIR = Path(__file__).resolve().parent / "extension"


def turnstile_extension_path() -> str:
    """Return the absolute path of the bundled unpacked extension."""

    return str(_EXTENSION_DIR)


def add_turnstile_extension(options: Any, extension_dir: str | Path | None = None) -> Any:
    """Add the bundled extension to a DrissionPage options object.

    The object is returned to support fluent setup in callers. Browser launch
    and browser shutdown remain the caller's responsibility.
    """

    path = Path(extension_dir) if extension_dir else _EXTENSION_DIR
    path = path.expanduser().resolve()
    if not path.is_dir():
        raise FileNotFoundError(f"Turnstile extension directory not found: {path}")
    options.add_extension(str(path))
    return options

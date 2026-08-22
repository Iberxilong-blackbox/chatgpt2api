"""
Sound Alarm — reusable notification-sound module.

Plays a sound effect to get the user's attention when a script pauses
for interactive input (unexpected page, manual action needed, etc.).

Backends (auto-detected, first available wins):
  1. **winsound** (Windows built-in, .wav only)          — zero dependencies
  2. **playsound** (cross-platform, .wav + .mp3)          — pip install playsound

The module generates a built-in sine-wave beep WAV at 880 Hz as default
fallback, so no external sound files are required to get started.

Usage
-----
    from sound_alarm import play_alarm

    # Default beep (blocking)
    play_alarm()

    # Custom .wav file (non-blocking — fire-and-forget)
    play_alarm("sounds/alert.wav", block=False)

    # Repeat 3 times (blocking only)
    play_alarm(repeat=3)

    # Check if any backend is available
    from sound_alarm import has_backend
    if has_backend():
        play_alarm()
"""

from __future__ import annotations

import logging
import math
import os
import struct
import sys
import tempfile
import threading
import wave

__all__ = ["play_alarm", "has_backend", "DEFAULT_TIMBRE", "SOUND_FILE"]

logger = logging.getLogger(__name__)

# ── Public constants ─────────────────────────────────────────────────────────
DEFAULT_TIMBRE = 880  # Hz — pleasant A5 note, audible but not jarring

# ═══════════════════════════════════════════════════════════════════════════
#  Custom sound file  ←  EDIT THIS to use your own .wav / .mp3 file
# ═══════════════════════════════════════════════════════════════════════════
# Set to the path of a sound file, e.g.:
#
#     SOUND_FILE = r"C:\My_project\PRM-mail\email-reg\alert.wav"
#     SOUND_FILE = "sounds/my-alert.wav"
#
# When set to None (the default), play_alarm() generates a built-in beep.
SOUND_FILE = r"C:\Users\LENOVO\Music\relaxed-narrative-bed.wav"

# ── Built-in beep WAV generator ─────────────────────────────────────────────

_DEFAULT_WAV_PATH: str | None = None
_LOCK = threading.Lock()


def _generate_beep_wav(
    frequency: float = DEFAULT_TIMBRE,
    duration_ms: int = 300,
    sample_rate: int = 44100,
) -> str:
    """Generate a sine-wave beep .wav and return its absolute path.

    The file is created in the system temp directory and persists for the
    process lifetime (cleaned up by the OS on reboot).
    A short fade-in/out envelope avoids audible click/pop artifacts.
    """
    num_samples = sample_rate * duration_ms // 1000
    amplitude = 0.3
    max_int16 = 32767
    fade_len = sample_rate // 100  # 10 ms fade

    samples = bytearray()
    for i in range(num_samples):
        # Sine
        value = amplitude * math.sin(2 * math.pi * frequency * i / sample_rate)
        # Envelope (fade in, fade out)
        if i < fade_len:
            envelope = i / fade_len
        elif i > num_samples - fade_len:
            envelope = (num_samples - i) / fade_len
        else:
            envelope = 1.0
        samples.extend(struct.pack("<h", int(value * envelope * max_int16)))

    fd, path = tempfile.mkstemp(suffix=".wav", prefix="alarm_")
    os.close(fd)

    with wave.open(path, "w") as wf:
        wf.setnchannels(1)       # mono
        wf.setsampwidth(2)       # 16-bit
        wf.setframerate(sample_rate)
        wf.writeframes(bytes(samples))

    return path


def _get_default_wav() -> str:
    """Lazy-init and cache the default beep WAV path (thread-safe)."""
    global _DEFAULT_WAV_PATH
    if _DEFAULT_WAV_PATH is None:
        with _LOCK:
            if _DEFAULT_WAV_PATH is None:
                _DEFAULT_WAV_PATH = _generate_beep_wav()
    return _DEFAULT_WAV_PATH


# ── Backend detection ───────────────────────────────────────────────────────

def _play_winsound(path: str, block: bool = True) -> None:
    """Play WAV via winsound (Windows built-in)."""
    import winsound
    flags = winsound.SND_FILENAME
    if not block:
        flags |= winsound.SND_ASYNC
    winsound.PlaySound(path, flags)


def _play_playsound(path: str, block: bool = True) -> None:
    """Play via playsound (cross-platform, .wav + .mp3)."""
    from playsound import playsound
    playsound(path, block=block)


_BackendInfo = tuple[str, callable, set[str]]
_BACKENDS: list[_BackendInfo] | None = None


def _detect_backends() -> list[_BackendInfo]:
    """Detect available playback backends (cached)."""
    global _BACKENDS
    if _BACKENDS is not None:
        return _BACKENDS

    backends: list[_BackendInfo] = []

    # 1. winsound — Windows native, zero dependencies
    if sys.platform == "win32":
        try:
            import winsound  # noqa: F401
            backends.append(("winsound", _play_winsound, {".wav"}))
        except ImportError:
            pass

    # 2. playsound — cross-platform, supports .mp3 + .wav
    try:
        import playsound  # noqa: F401
        backends.append(("playsound", _play_playsound, {".wav", ".mp3"}))
    except ImportError:
        pass

    _BACKENDS = backends
    return backends


# ── Public API ──────────────────────────────────────────────────────────────

def has_backend() -> bool:
    """Return True if at least one audio playback backend is available."""
    return len(_detect_backends()) > 0


def play_alarm(
    sound_file: str | os.PathLike | None = None,
    *,
    block: bool = True,
    repeat: int = 1,
) -> bool:
    """Play an alarm sound to get the user's attention.

    Parameters
    ----------
    sound_file
        Path to a ``.wav`` or ``.mp3`` file.
        If *None*, plays a built-in 880 Hz sine-wave beep.
    block
        If True (default), wait for playback to finish.
        If False, fire-and-forget in a daemon background thread.
    repeat
        Number of times to repeat the sound (with 300 ms pause between).
        Only meaningful when *block* is True (sequential playback).

    Returns
    -------
    bool
        True if playback was attempted, False if no audio backend is
        available on this system.
    """
    backends = _detect_backends()
    if not backends:
        logger.warning(
            "No audio playback backend found. "
            "Install playsound for cross-platform support:  pip install playsound"
        )
        return False

    # ── Resolve sound path (argument → SOUND_FILE constant → built-in beep) ──
    if sound_file is not None:
        path = str(sound_file)
    elif SOUND_FILE is not None:
        path = str(SOUND_FILE)
    else:
        path = _get_default_wav()

    if not os.path.isfile(path):
        logger.warning("Sound file not found: '%s' — falling back to default beep", path)
        path = _get_default_wav()

    # ── Pick the best backend for this file extension ──
    ext = os.path.splitext(path)[1].lower()
    backend: _BackendInfo | None = None
    for name, func, supported in backends:
        if ext in supported:
            backend = (name, func)
            break
    if backend is None:
        # Fallback: first available backend
        backend = backends[0]

    name, func = backend
    logger.debug("Playing '%s' via %s backend (block=%s, repeat=%d)",
                 path, name, block, repeat)

    # ── Play ──
    def _play_sequence() -> None:
        for i in range(repeat):
            func(path, block=(block and i == repeat - 1))
            if repeat > 1 and i < repeat - 1:
                import time  # noqa: ICI — lazy import to keep module-level clean
                time.sleep(0.3)

    if block:
        _play_sequence()
    else:
        t = threading.Thread(target=_play_sequence, daemon=True)
        t.start()

    return True


# ── Convenience wrappers ────────────────────────────────────────────────────

def play_success() -> bool:
    """Play a quick success chime (higher pitch, shorter)."""
    # Regenerate at a higher pitch so we don't mutate the cached default
    path = _generate_beep_wav(frequency=1320, duration_ms=200)
    return play_alarm(path, block=False)


def play_error() -> bool:
    """Play an error alert (lower pitch, longer, two-tone)."""
    # Two-tone: low → lower
    path1 = _generate_beep_wav(frequency=330, duration_ms=250)
    path2 = _generate_beep_wav(frequency=220, duration_ms=350)

    def _two_tone():
        import winsound
        winsound.PlaySound(path1, winsound.SND_FILENAME | winsound.SND_ASYNC)
        threading.Event().wait(0.25)
        winsound.PlaySound(path2, winsound.SND_FILENAME)

    t = threading.Thread(target=_two_tone, daemon=True)
    t.start()
    return True


# ── Self-test (python -m sound_alarm) ───────────────────────────────────────

if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG, format="%(message)s")

    if not has_backend():
        print("[!] No audio backend available on this system.")
        print("    On Windows:  winsound is built-in, should always be available.")
        print("    Cross-platform:  pip install playsound")
        sys.exit(1)

    print("♪ Playing default alarm (built-in beep, 880 Hz, blocking)...")
    play_alarm()
    print("✓ Done — did you hear it?")

    print("\n♪ Playing default alarm (non-blocking, fire-and-forget)...")
    play_alarm(block=False)
    print("  (returned immediately, sound playing in background)")

    print("\n♪ Playing repeat 3× (blocking)...")
    play_alarm(repeat=3)
    print("✓ 3× repeat done.")

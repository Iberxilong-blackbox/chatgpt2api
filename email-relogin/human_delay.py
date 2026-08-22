"""
Human Behavioral Delay Model — Python port of human-simulator/behavior.js.

Pure math, no DOM dependencies. Used for:
  - Replacing fixed time.sleep() calls with human-like delays
  - Providing intelligent inter-char delays for _type_human()
  - Generating behavior profiles for session-to-session variation

All delay values are in MILLISECONDS (matching the JS source).
"""

import math
import random
import time
from datetime import datetime


# ═══════════════════════════════════════════════════════════════════════════
# Math utilities (ported from utils/math.js)
# ═══════════════════════════════════════════════════════════════════════════

def normal_random(min_val: float, max_val: float) -> float:
    """Box-Muller transform — normal distribution bounded to [min, max].

    99.7% of values fall within 3σ of the mean.
    """
    u1 = random.random()
    u2 = random.random()
    z0 = math.sqrt(-2 * math.log(u1)) * math.cos(2 * math.pi * u2)
    mean = (min_val + max_val) / 2
    std_dev = (max_val - min_val) / 6  # 3σ boundary
    result = z0 * std_dev + mean
    return round(max(min_val, min(max_val, result)))


# ═══════════════════════════════════════════════════════════════════════════
# Poisson Timing (ported from behavior.js:PoissonTimingSimulator)
# ═══════════════════════════════════════════════════════════════════════════

# λ parameters (average events per second) per activity type
_LAMBDA_VALUES = {
    'reading': 0.3,          # one event every ~3.3s
    'typing': 1.5,           # one event every ~0.67s
    'browsing': 0.8,         # one event every ~1.25s
    'decision_making': 0.2,  # one event every ~5s
    'mouse_movement': 0.6,   # one event every ~1.67s
    'idle': 0.1,             # one event every ~10s
}

_MIN_INTERVALS = {
    'reading': 500, 'typing': 50, 'browsing': 200,
    'decision_making': 1000, 'mouse_movement': 100, 'idle': 2000,
}

_MAX_INTERVALS = {
    'reading': 10000, 'typing': 500, 'browsing': 5000,
    'decision_making': 30000, 'mouse_movement': 2000, 'idle': 60000,
}

# Circadian rhythm — activity multiplier by hour (0-23)
_ACTIVITY_CURVE = {
    0: 0.1, 1: 0.05, 2: 0.03, 3: 0.02, 4: 0.02, 5: 0.03,
    6: 0.1, 7: 0.3, 8: 0.6, 9: 0.9, 10: 1.0, 11: 1.0,
    12: 0.8, 13: 0.9, 14: 1.0, 15: 0.9, 16: 0.8, 17: 0.7,
    18: 0.6, 19: 0.5, 20: 0.4, 21: 0.3, 22: 0.2, 23: 0.15,
}


def poisson_interval(activity_type: str = 'typing') -> float:
    """Generate a Poisson-distributed interval for the given activity type.

    Poisson process formula: next_event_time = -ln(1-U) / λ
    Returns milliseconds.
    """
    lam = _LAMBDA_VALUES.get(activity_type, 0.5)
    u = random.random()
    interval_sec = -math.log(1.0 - u) / lam
    interval_ms = interval_sec * 1000

    # Adjust for time of day
    hour = datetime.now().hour
    multiplier = _ACTIVITY_CURVE.get(hour, 0.5)
    interval_ms = interval_ms / multiplier

    # Clamp to reasonable range
    min_iv = _MIN_INTERVALS.get(activity_type, 100)
    max_iv = _MAX_INTERVALS.get(activity_type, 5000)
    return round(max(min_iv, min(max_iv, interval_ms)))


def hybrid_delay(activity_type: str = 'typing', poisson_weight: float = 0.4) -> float:
    """Mix Poisson (40%) and Normal (60%) for more natural randomness.

    Returns milliseconds.
    """
    if random.random() < poisson_weight:
        return poisson_interval(activity_type)
    else:
        min_iv = _MIN_INTERVALS.get(activity_type, 100)
        max_iv = _MAX_INTERVALS.get(activity_type, 5000)
        return normal_random(min_iv, max_iv * 0.8)


# ═══════════════════════════════════════════════════════════════════════════
# Cognitive Delays (ported from behavior.js:CognitiveDelaySimulator)
# ═══════════════════════════════════════════════════════════════════════════

_DELAY_PROFILES = {
    'reading':             {'min': 1200, 'max': 3500},
    'thinking':            {'min': 800,  'max': 2200},
    'hesitation':          {'min': 300,  'max': 1500},
    'typing_preparation':  {'min': 200,  'max': 800},
    'word_pause':          {'min': 50,   'max': 400},
    'sentence_pause':      {'min': 200,  'max': 800},
    'comprehension_pause': {'min': 500,  'max': 1800},
    'decision_pause':      {'min': 1000, 'max': 4000},
}


def calculate_reading_delay(text: str) -> float:
    """Calculate reading time based on text length (Chinese char count).

    Returns milliseconds.
    """
    base = _DELAY_PROFILES['reading']
    words_per_minute = 200  # average reading speed (Chinese chars)
    char_count = len(text) if text else 0
    if char_count == 0:
        return base['min']
    calculated = (char_count / 3) / words_per_minute * 60 * 1000
    random_factor = 0.3 + random.random() * 0.4
    return round(max(base['min'], min(base['max'], calculated * random_factor)))


def calculate_thinking_delay(input_text: str, context: str = 'normal') -> float:
    """Calculate thinking time based on input complexity.

    Returns milliseconds.
    """
    base = _DELAY_PROFILES['thinking']
    factors = {
        'simple_greeting': 0.3, 'normal': 1.0, 'technical': 1.5,
        'negotiation': 2.0, 'complaint': 1.8, 'explanation': 1.6,
    }
    factor = factors.get(context, 1.0)
    length_factor = min(2.0, len(input_text) / 50)
    return normal_random(base['min'] * factor, base['max'] * factor * length_factor)


def calculate_inter_char_delay(current_char: str, next_char: str,
                               position: int, full_text: str) -> float:
    """Calculate natural delay between two characters.

    Handles punctuation pauses, word boundaries, number transitions.
    Returns milliseconds.
    """
    base = _DELAY_PROFILES['word_pause']

    # Chinese punctuation
    if current_char in '。！？，、；：':
        sp = _DELAY_PROFILES['sentence_pause']
        return normal_random(sp['min'], sp['max'])

    # English punctuation
    if current_char in '.!?':
        sp = _DELAY_PROFILES['sentence_pause']
        return normal_random(sp['min'] * 0.8, sp['max'] * 0.8)

    # Word boundary (space, end of English word)
    if current_char == ' ' or (current_char.isascii() and current_char.isalpha()
                               and next_char and not next_char.isascii()):
        return normal_random(base['min'] * 1.5, base['max'] * 1.5)

    # Number-to-non-number boundary
    if current_char.isdigit() and next_char and not next_char.isdigit():
        return normal_random(base['min'] * 1.2, base['max'] * 1.2)

    # Normal inter-char delay
    return normal_random(base['min'], base['max'])


# ═══════════════════════════════════════════════════════════════════════════
# Delay context patterns (ported from behavior.js:generateHumanDelay)
# ═══════════════════════════════════════════════════════════════════════════

_DELAY_PATTERNS = {
    'thinking':       {'min': 2000, 'max': 5000, 'activity': 'decision_making'},
    'reading':        {'min': 1000, 'max': 3000, 'activity': 'reading'},
    'typing':         {'min': 50,   'max': 200,  'activity': 'typing'},
    'clicking':       {'min': 40,   'max': 120,  'activity': 'browsing'},
    'mouse_movement': {'min': 100,  'max': 800,  'activity': 'mouse_movement'},
    'normal':         {'min': 100,  'max': 300,  'activity': 'browsing'},
}


def generate_human_delay(base_min: float = 100, base_max: float = 300,
                         context: str = 'normal') -> float:
    """Unified human delay generator — replaces _human_delay().

    Returns MILLISECONDS. Divide by 1000 for time.sleep().

    Context types:
      - 'thinking': 2000-5000ms
      - 'reading':  1000-3000ms
      - 'typing':   50-200ms
      - 'clicking': 40-120ms
      - 'mouse_movement': 100-800ms
      - 'normal':    uses base_min/base_max
    """
    pattern = _DELAY_PATTERNS.get(context, _DELAY_PATTERNS['normal'])

    if context == 'normal':
        # Use caller-provided bounds but with smarter distribution
        if random.random() < 0.4:
            return hybrid_delay(pattern['activity'], 0.6)
        else:
            return normal_random(base_min * 1000, base_max * 1000)
    else:
        if random.random() < 0.4:
            return hybrid_delay(pattern['activity'], 0.6)
        else:
            return normal_random(pattern['min'], pattern['max'])


def human_sleep(min_s: float, max_s: float, context: str = 'normal',
                label: str = None) -> None:
    """Drop-in replacement for _human_delay().

    Sleeps for a human-like random duration. Takes seconds for
    backward compatibility with the existing API.
    """
    delay_ms = generate_human_delay(min_s * 1000, max_s * 1000, context)
    delay_s = delay_ms / 1000.0
    if label:
        print(f"[*] {label} ({delay_s:.1f}s)")
    time.sleep(delay_s)


# ═══════════════════════════════════════════════════════════════════════════
# Behavior Personality Profiles (ported from behavior.js:generatePersonalityProfile)
# ═══════════════════════════════════════════════════════════════════════════

_PERSONALITY_PROFILES = {
    'quick':    {'speed': 0.7,  'accuracy': 0.9,  'pause_frequency': 0.1},
    'normal':   {'speed': 1.0,  'accuracy': 0.95, 'pause_frequency': 0.2},
    'careful':  {'speed': 1.3,  'accuracy': 0.98, 'pause_frequency': 0.3},
    'hesitant': {'speed': 1.8,  'accuracy': 0.85, 'pause_frequency': 0.4},
}


def generate_personality_profile() -> dict:
    """Randomly generate a behavior personality profile.

    Returns dict with keys: type, speed, accuracy, pause_frequency, created_at.
    'speed' is a delay multiplier: 0.7=fast, 1.0=normal, 1.8=slow.
    """
    personality = random.choice(list(_PERSONALITY_PROFILES.keys()))
    profile = dict(_PERSONALITY_PROFILES[personality])
    profile['type'] = personality
    profile['created_at'] = int(time.time() * 1000)
    return profile


# ═══════════════════════════════════════════════════════════════════════════
# Convenience: generate inter-char delays for a whole string
# ═══════════════════════════════════════════════════════════════════════════

def generate_typing_delays(text: str) -> list:
    """Generate a list of per-character delays for human-like typing.

    Returns list of (char, delay_ms) tuples.
    """
    delays = []
    for i, ch in enumerate(text):
        next_ch = text[i + 1] if i + 1 < len(text) else ''
        delay_ms = calculate_inter_char_delay(ch, next_ch, i, text)
        delays.append((ch, delay_ms))
    return delays


# ═══════════════════════════════════════════════════════════════════════════
# Self-test
# ═══════════════════════════════════════════════════════════════════════════

if __name__ == '__main__':
    print("=== Human Delay Model — Smoke Test ===\n")

    profile = generate_personality_profile()
    print(f"Personality: {profile['type']} (speed={profile['speed']}, accuracy={profile['accuracy']})")

    print("\nNormal random samples (100, 300):")
    samples = [normal_random(100, 300) for _ in range(10)]
    print(f"  {[int(s) for s in samples]}")

    print("\nPoisson intervals (typing):")
    pois = [poisson_interval('typing') for _ in range(10)]
    print(f"  {pois}")

    print("\nHybrid delays (browsing):")
    hyb = [hybrid_delay('browsing') for _ in range(10)]
    print(f"  {hyb}")

    print("\nContext delays:")
    for ctx in ['thinking', 'reading', 'typing', 'clicking', 'mouse_movement', 'normal']:
        d = generate_human_delay(100, 300, ctx)
        print(f"  {ctx}: {int(d)}ms ({d/1000:.2f}s)")

    print(f"\nReading delay for '这是一段测试文本，用于验证阅读时间计算':")
    print(f"  {calculate_reading_delay('这是一段测试文本，用于验证阅读时间计算')}ms")

    print(f"\nInter-char delays for 'Hello World!':")
    for ch, d in generate_typing_delays('Hello World!'):
        print(f"  '{ch}' -> {int(d)}ms")

    print("\n=== Done ===")

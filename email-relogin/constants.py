"""Constants for the zhuce6 ChatGPT platform."""

from __future__ import annotations

import random
from datetime import datetime
from enum import Enum


class AccountStatus(str, Enum):
    ACTIVE = "active"
    EXPIRED = "expired"
    BANNED = "banned"
    FAILED = "failed"


class TaskStatus(str, Enum):
    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"


APP_NAME = "zhuce6 ChatGPT"
APP_VERSION = "0.1.0"

OAUTH_CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann"
OAUTH_AUTH_URL = "https://auth.openai.com/oauth/authorize"
OAUTH_TOKEN_URL = "https://auth.openai.com/oauth/token"
OAUTH_REDIRECT_URI = "http://localhost:1455/auth/callback"
OAUTH_SCOPE = "openid email profile offline_access"

CHAT_WEB_CLIENT_ID = "app_X8zY6vW2pQ9tR3dE7nK1jL5gH"
CHAT_WEB_REDIRECT_URI = "https://chatgpt.com/api/auth/callback/openai"
CHAT_WEB_AUTH_URL = "https://auth.openai.com/oauth/authorize"
CHATGPT_SIGNIN_URL = "https://chatgpt.com/api/auth/signin/openai"
CHATGPT_SESSION_URL = "https://chatgpt.com/api/auth/session"

OPENAI_API_ENDPOINTS = {
    "sentinel": "https://sentinel.openai.com/backend-api/sentinel/req",
    "signup": "https://auth.openai.com/api/accounts/authorize/continue",
    "register": "https://auth.openai.com/api/accounts/user/register",
    "password_verify": "https://auth.openai.com/api/accounts/password/verify",
    "send_otp": "https://auth.openai.com/api/accounts/email-otp/send",
    "validate_otp": "https://auth.openai.com/api/accounts/email-otp/validate",
    "create_account": "https://auth.openai.com/api/accounts/create_account",
    "select_workspace": "https://auth.openai.com/api/accounts/workspace/select",
    "select_organization": "https://auth.openai.com/api/accounts/organization/select",
}

OPENAI_PAGE_TYPES = {
    "EMAIL_OTP_VERIFICATION": "email_otp_verification",
    "PASSWORD_REGISTRATION": "password",
}

OTP_CODE_PATTERN = r"(?<!\d)(\d{6})(?!\d)"
DEFAULT_PASSWORD_LENGTH = 12
PASSWORD_CHARSET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

FIRST_NAMES = [
    "James",
    "Emma",
    "Noah",
    "Olivia",
    "Liam",
    "Sophia",
    "Mia",
    "Lucas",
    "Aria",
    "Grace",
]

ERROR_MESSAGES = {
    "unsupported_region": "Unsupported IP location",
    "network_error": "Network request failed",
    "oauth_error": "OAuth exchange failed",
}


def generate_random_user_info() -> dict[str, str]:
    name = random.choice(FIRST_NAMES)
    current_year = datetime.now().year
    birth_year = random.randint(current_year - 45, current_year - 18)
    birth_month = random.randint(1, 12)
    if birth_month in {1, 3, 5, 7, 8, 10, 12}:
        birth_day = random.randint(1, 31)
    elif birth_month in {4, 6, 9, 11}:
        birth_day = random.randint(1, 30)
    else:
        birth_day = random.randint(1, 28)
    return {
        "name": name,
        "birthdate": f"{birth_year}-{birth_month:02d}-{birth_day:02d}",
    }

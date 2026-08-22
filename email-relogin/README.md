# Email Relogin Bundle

This is the standalone runtime bundle for re-logging into existing email
accounts. Run it directly from this directory:

Use `run_single_relogin.py` for M1. It creates a private `runtime/<run_id>/`
directory containing the preflight result, a restricted account-file copy,
script log, failure screenshots, and a redacted summary.
The temporary account-file copy is deleted after the summary is produced.

Before running, create local configuration files from the templates:

```bash
cp config.json.example config.json
cp zoho-config.jsonc.example config.jsonc
chmod 600 config.json config.jsonc
```

The program reads `config.json` for proxy and fingerprint settings, and reads
`config.jsonc` for Zoho IMAP credentials. Environment variables prefixed with
`ZHUCE6_ZOHO_` override the Zoho configuration.

On Ubuntu, start Xvfb and set `DISPLAY` before running the wrapper. Do not pass
`--recon`: the M1 runner relies on stage logs and failure screenshots instead.

```bash
python run_single_relogin.py --preflight-only
python run_single_relogin.py --account-json ./accounts/session-user.json --clean-stale-cdp
```

All account data, runtime results, screenshots, logs, virtualenvs, and HARs
are ignored by Git. Real configuration files must be mode `600`.

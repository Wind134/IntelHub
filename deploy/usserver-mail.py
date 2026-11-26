#!/usr/bin/env python3
"""Zero-dependency SMTP & Feishu App mailer for backup-node sentinel alerts & Beijing 12:00 daily reports."""
import argparse
import glob
import json
import os
import shutil
import smtplib
import ssl
import sys
import time
import urllib.request
from datetime import datetime, timezone, timedelta
from email.message import EmailMessage

SHANGHAI_TZ = timezone(timedelta(hours=8))
DEFAULT_ENV = os.path.expanduser("~/.config/intel-sentinel/mail.env")
FEISHU_ENV = os.path.expanduser("~/.config/intel-sentinel/feishu.env")
SENTINEL_ENV = os.path.expanduser("~/.config/intel-sentinel/sentinel.env")
BACKUP_DIR = os.path.expanduser("~/backups/intel-hub")
SENTINEL_LOG = os.path.expanduser("~/bin/sentinel.log")


def load_env(path: str) -> dict:
    cfg = {}
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                cfg[k.strip()] = v.strip().strip('"').strip("'")
    return cfg


def send_email(cfg: dict, subject: str, body: str) -> None:
    host = cfg.get("SMTP_HOST", "")
    port = int(cfg.get("SMTP_PORT", "465") or "465")
    user = cfg.get("SMTP_USER", "")
    password = cfg.get("SMTP_PASS", "")
    mail_to = cfg.get("MAIL_TO", "") or user

    if not all([host, user, password, mail_to]):
        print("mail.env not configured yet (missing SMTP_HOST/SMTP_USER/SMTP_PASS/MAIL_TO)", file=sys.stderr)
        sys.exit(2)

    msg = EmailMessage()
    msg["From"] = f"IntelHub Sentinel <{user}>"
    msg["To"] = mail_to
    msg["Subject"] = subject
    msg.set_content(body)

    ctx = ssl.create_default_context()
    if port == 465:
        with smtplib.SMTP_SSL(host, port, timeout=15, context=ctx) as s:
            s.login(user, password)
            s.send_message(msg)
    else:
        with smtplib.SMTP(host, port, timeout=15) as s:
            s.ehlo()
            s.starttls(context=ctx)
            s.ehlo()
            s.login(user, password)
            s.send_message(msg)


def build_daily_report(cfg: dict) -> tuple[str, str]:
    health_url = cfg.get("INTEL_HEALTH_URL") or os.environ.get("INTEL_HEALTH_URL", "https://intel.example.com/api/v1/health")
    now_bj = datetime.now(SHANGHAI_TZ)
    ts_str = now_bj.strftime("%Y-%m-%d %H:%M:%S UTC+8")

    # 1. Probe primary node via Cloudflare Tunnel
    health_status = "异常"
    latency_ms = -1
    t0 = time.monotonic()
    try:
        req = urllib.request.Request(health_url, headers={"User-Agent": "IntelHub-Sentinel/1.0"})
        with urllib.request.urlopen(req, timeout=10) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            latency_ms = int((time.monotonic() - t0) * 1000)
            if resp.status == 200 and '"ok":true' in raw:
                health_status = f"正常 (HTTP 200, {latency_ms}ms)"
            else:
                health_status = f"异常 (HTTP {resp.status}: {raw[:80]})"
    except Exception as e:
        health_status = f"不可达 ({e})"

    # 2. Inspect latest SQLite hot backup on backup node
    snaps = sorted(glob.glob(os.path.join(BACKUP_DIR, "intel_hub_*.db")))
    if snaps:
        latest = snaps[-1]
        st = os.stat(latest)
        age_h = (time.time() - st.st_mtime) / 3600.0
        size_mb = st.st_size / (1024 * 1024)
        backup_line = f"{os.path.basename(latest)} ({size_mb:.2f} MB, {age_h:.1f} 小时前, 共保留 {len(snaps)} 份)"
    else:
        backup_line = "警告：未发现任何备份快照！"

    # 3. Backup node disk & sentinel failure count
    disk = shutil.disk_usage("/")
    disk_free_gb = disk.free / (1024 ** 3)
    disk_total_gb = disk.total / (1024 ** 3)

    fail_count = 0
    if os.path.exists(SENTINEL_LOG):
        with open(SENTINEL_LOG, "r", encoding="utf-8", errors="ignore") as f:
            for line in f:
                if "FAIL(" in line:
                    fail_count += 1

    ok_emoji = "✅" if health_status.startswith("正常") and snaps else "⚠️"
    subject = f"{ok_emoji} [IntelHub 每日巡检] {now_bj.strftime('%Y-%m-%d')} 12:00 状态报告"
    body = f"""IntelHub 双机自动化巡检日报（北京时间每日 12:00 定时发送）
==================================================
巡检时间：{ts_str}

1. 主生产机公网隧道状态
   - 探测地址：{health_url}
   - 当前状态：{health_status}

2. SQLite 永久历史库异地备份
   - 最新快照：{backup_line}

3. 备机哨兵运行指标
   - 磁盘可用：{disk_free_gb:.1f} GB / {disk_total_gb:.1f} GB
   - 近期日志异常次数：{fail_count} 次

==================================================
本邮件由备机定时哨兵自动发送。
"""
    return subject, body


def send_feishu_app(cfg: dict, text: str) -> bool:
    app_id = cfg.get("FEISHU_APP_ID", "")
    app_secret = cfg.get("FEISHU_APP_SECRET", "")
    chat_id = cfg.get("FEISHU_CHAT_ID", "") or cfg.get("FEISHU_HOME_CHANNEL", "")
    domain = cfg.get("FEISHU_DOMAIN", "feishu").strip().lower()
    api_base = "https://open.larksuite.com" if domain == "lark" else "https://open.feishu.cn"

    if not all([app_id, app_secret, chat_id]):
        return False

    tok_req = urllib.request.Request(
        f"{api_base}/open-apis/auth/v3/tenant_access_token/internal",
        data=json.dumps({"app_id": app_id, "app_secret": app_secret}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(tok_req, timeout=12) as resp:
        tok_data = json.loads(resp.read().decode("utf-8"))
    token = tok_data.get("tenant_access_token", "")
    if not token:
        raise RuntimeError(f"feishu token failed: {tok_data.get('msg', 'unknown')}")

    msg_req = urllib.request.Request(
        f"{api_base}/open-apis/im/v1/messages?receive_id_type=chat_id",
        data=json.dumps({
            "receive_id": chat_id,
            "msg_type": "text",
            "content": json.dumps({"text": text}, ensure_ascii=False),
        }, ensure_ascii=False).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {token}",
        },
        method="POST",
    )
    with urllib.request.urlopen(msg_req, timeout=12) as resp:
        res_data = json.loads(resp.read().decode("utf-8"))
    if res_data.get("code", 0) != 0:
        raise RuntimeError(f"feishu send failed: {res_data.get('msg', res_data)}")
    return True


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--env", default=DEFAULT_ENV)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_alert = sub.add_parser("alert")
    p_alert.add_argument("--subject", required=True)
    p_alert.add_argument("--body", required=True)

    sub.add_parser("daily-report")
    sub.add_parser("test")

    args = parser.parse_args()
    cfg = load_env(args.env)
    feishu_cfg = load_env(FEISHU_ENV)
    sentinel_cfg = load_env(SENTINEL_ENV)
    merged = {**sentinel_cfg, **feishu_cfg, **cfg}

    if args.cmd == "alert":
        body_text = args.body.replace("\\n", "\n")
        ok_any = False
        try:
            send_email(merged, args.subject, body_text)
            print("ALERT_EMAIL_SENT")
            ok_any = True
        except Exception as e:
            print(f"ALERT_EMAIL_ERR: {e}", file=sys.stderr)
        try:
            if send_feishu_app(merged, f"{args.subject}\n\n{body_text}"):
                print("ALERT_FEISHU_SENT")
                ok_any = True
        except Exception as e:
            print(f"ALERT_FEISHU_ERR: {e}", file=sys.stderr)
        if not ok_any:
            sys.exit(1)
    elif args.cmd == "daily-report":
        subj, body = build_daily_report(merged)
        send_email(merged, subj, body)
        print(f"[{datetime.now(SHANGHAI_TZ).strftime('%Y-%m-%d %H:%M:%S')}] DAILY_EMAIL_SENT")
    elif args.cmd == "test":
        now_bj = datetime.now(SHANGHAI_TZ).strftime("%Y-%m-%d %H:%M:%S UTC+8")
        subj = "✅ [IntelHub] 备机哨兵双通道告警测试成功"
        body = f"测试时间：{now_bj}\n\n你的备机故障告警（邮件 + 飞书双通道）与每日北京时间 12:00 邮件巡检已就绪。"
        try:
            send_email(merged, subj, body)
            print("TEST_EMAIL_SENT")
        except BaseException as e:
            print(f"TEST_EMAIL_SKIP_OR_ERR: {e}", file=sys.stderr)
        try:
            if send_feishu_app(merged, f"{subj}\n\n{body}"):
                print("TEST_FEISHU_SENT")
            else:
                print("TEST_FEISHU_NOT_CONFIGURED", file=sys.stderr)
        except Exception as e:
            print(f"TEST_FEISHU_ERR: {e}", file=sys.stderr)


if __name__ == "__main__":
    main()

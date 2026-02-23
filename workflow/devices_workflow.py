import argparse
import json
import os
import re
import sys
import time
from dataclasses import dataclass
from typing import Any, Dict, Optional, Tuple, List

import requests

DEFAULT_BASE_CANDIDATES = [
    "http://chirpstack:8080",
    "http://chirpstack-rest-api:8090"
]

ENV_BASE_URL = os.getenv("CHIRPSTACK_BASE_URL", "").strip()
ENV_API_KEY = os.getenv("CHIRPSTACK_API_KEY", "PUT_YOUR_API_KEY_HERE").strip()
ENV_TENANT_ID = os.getenv("CHIRPSTACK_TENANT_ID", "PUT_YOUR_TENANT_ID_HERE").strip()
SIM_TOML_PATH = os.getenv("SIM_TOML_PATH", "../simulator/chirpstack-simulator.toml").strip()

DEFAULT_TIMEOUT_S = int(os.getenv("HTTP_TIMEOUT_S", "20"))
DEFAULT_RETRIES = int(os.getenv("HTTP_RETRIES", "4"))
DEFAULT_RETRY_BACKOFF_S = float(os.getenv("HTTP_RETRY_BACKOFF_S", "1.2"))

DEFAULT_REGION_ID = os.getenv("DEFAULT_REGION_ID", "us915_0").strip()


def log(msg: str) -> None:
    print(msg, flush=True)

def fatal(msg: str, code: int = 2) -> None:
    print(f"[workflow] ERROR: {msg}", file=sys.stderr, flush=True)
    sys.exit(code)

def looks_like_placeholder(s: str) -> bool:
    return ("PUT_YOUR_" in s) or (s.strip() == "")

def is_uuidish(s: str) -> bool:
    return bool(re.match(r"^[0-9a-fA-F-]{16,}$", s))

def clamp_json(obj: Any, max_chars: int = 800) -> str:
    try:
        s = json.dumps(obj, ensure_ascii=False)
    except Exception:
        s = str(obj)
    return (s[:max_chars] + "…") if len(s) > max_chars else s

@dataclass
class ApiClient:
    base_url: str
    api_key: str
    timeout_s: int = DEFAULT_TIMEOUT_S
    retries: int = DEFAULT_RETRIES
    backoff_s: float = DEFAULT_RETRY_BACKOFF_S

    def headers(self) -> Dict[str, str]:
        return {
            "Authorization": f"Bearer {self.api_key}",
            "Content-Type": "application/json",
            "Accept": "application/json",
        }

    def _request(self, method: str, path: str, payload: Optional[dict] = None, params: Optional[dict] = None) -> requests.Response:
        url = self.base_url.rstrip("/") + path
        last_exc = None

        for attempt in range(1, self.retries + 1):
            try:
                r = requests.request(
                    method=method,
                    url=url,
                    headers=self.headers(),
                    data=json.dumps(payload) if payload is not None else None,
                    params=params,
                    timeout=self.timeout_s,
                )

                if r.status_code >= 500:
                    log(f"[workflow] WARN {method} {url} -> {r.status_code} (attempt {attempt}/{self.retries})")
                    time.sleep(self.backoff_s * attempt)
                    continue
                return r
            except requests.RequestException as e:
                last_exc = e
                log(f"[workflow] WARN {method} {url} network error: {e} (attempt {attempt}/{self.retries})")
                time.sleep(self.backoff_s * attempt)

        raise RuntimeError(f"{method} {url} failed after {self.retries} attempts: {last_exc}")

    def get_json(self, path: str, params: Optional[dict] = None) -> dict:
        r = self._request("GET", path, params=params)
        if not r.ok:
            raise RuntimeError(f"GET {path} -> {r.status_code}: {r.text[:600]}")
        return r.json() if r.text else {}

    def post_json(self, path: str, payload: dict) -> dict:
        r = self._request("POST", path, payload=payload)
        if not r.ok:
            raise RuntimeError(f"POST {path} -> {r.status_code}: {r.text[:600]}")
        return r.json() if r.text else {}

    def put_json(self, path: str, payload: dict) -> dict:
        r = self._request("PUT", path, payload=payload)
        if not r.ok:
            raise RuntimeError(f"PUT {path} -> {r.status_code}: {r.text[:600]}")
        return r.json() if r.text else {}

    def delete(self, path: str) -> None:
        r = self._request("DELETE", path)
        if not r.ok:
            raise RuntimeError(f"DELETE {path} -> {r.status_code}: {r.text[:600]}")

def detect_base_url(api_key: str, prefer: Optional[str] = None) -> str:
    candidates: List[str] = []
    if prefer:
        candidates.append(prefer)
    if ENV_BASE_URL:
        candidates.append(ENV_BASE_URL)
    candidates.extend(DEFAULT_BASE_CANDIDATES)

    seen = set()
    ordered = []
    for c in candidates:
        if c and c not in seen:
            ordered.append(c)
            seen.add(c)

    probe_paths = [
        ("/api/tenants?limit=1", "tenants"),
        ("/api/applications?limit=1", "applications"),
    ]

    for base in ordered:
        client = ApiClient(base_url=base, api_key=api_key)
        for path, label in probe_paths:
            try:
                r = client._request("GET", path.split("?")[0], params={"limit": 1} if "limit" in path else None)
                if r.status_code in (401, 403):
                    log(f"[workflow] endpoint probe OK (auth required): {base}")
                    return base
                if r.ok:
                    log(f"[workflow] endpoint probe OK ({label}): {base}")
                    return base
            except Exception:
                continue
    fallback = (prefer or ENV_BASE_URL or DEFAULT_BASE_CANDIDATES[-1])
    log(f"[workflow] WARN could not auto-detect REST endpoint; falling back to {fallback}")
    return fallback

def ensure_simulator_toml(sim_path: str, api_key: str, tenant_id: str) -> None:
    if not os.path.exists(sim_path):
        fatal(f"SIM_TOML_PATH not found: {sim_path}")

    with open(sim_path, "r", encoding="utf-8") as f:
        original = f.read()

    backup_path = sim_path + ".bak"
    if not os.path.exists(backup_path):
        with open(backup_path, "w", encoding="utf-8") as f:
            f.write(original)
        log(f"[workflow] simulator TOML backup created: {backup_path}")

    updated = original.replace("PUT_YOUR_API_KEY_HERE", api_key).replace("PUT_YOUR_TENANT_ID_HERE", tenant_id)

    if updated == original:
        log("[workflow] simulator TOML already patched (or placeholders not present).")
        return

    with open(sim_path, "w", encoding="utf-8") as f:
        f.write(updated)

    log(f"[workflow] simulator TOML updated: {sim_path}")

def find_by_name(items: list, name: str, name_key: str = "name") -> Optional[dict]:
    for it in items or []:
        if isinstance(it, dict) and it.get(name_key) == name:
            return it
    return None

def list_tenants(client: ApiClient, limit: int = 50) -> list:
    data = client.get_json("/api/tenants", params={"limit": limit, "offset": 0})
    return data.get("result", []) if isinstance(data, dict) else []

def ensure_tenant(client: ApiClient, tenant_id: str, tenant_name: str = "demo-tenant") -> str:
    if tenant_id and not looks_like_placeholder(tenant_id) and is_uuidish(tenant_id):
        log(f"[workflow] tenant_id provided: {tenant_id}")
        return tenant_id
    tenants = list_tenants(client)
    found = find_by_name(tenants, tenant_name)
    if found and "id" in found:
        tid = found["id"]
        log(f"[workflow] tenant exists: {tenant_name} -> {tid}")
        return tid
    payload = {
        "tenant": {
            "name": tenant_name,
            "description": "Provisioned by workflow",
        }
    }
    created = client.post_json("/api/tenants", payload)
    tid = created.get("id") or (created.get("tenant", {}) if isinstance(created, dict) else {}).get("id")
    if not tid:
        fatal(f"tenant create returned no id: {created}")
    log(f"[workflow] tenant created: {tenant_name} -> {tid}")
    return tid

def list_applications(client: ApiClient, tenant_id: str, limit: int = 50) -> list:
    data = client.get_json("/api/applications", params={"limit": limit, "offset": 0, "tenantId": tenant_id})
    return data.get("result", []) if isinstance(data, dict) else []

def ensure_application(client: ApiClient, tenant_id: str, app_name: str = "demo-app") -> str:
    apps = list_applications(client, tenant_id)
    found = find_by_name(apps, app_name)
    if found and "id" in found:
        aid = found["id"]
        log(f"[workflow] application exists: {app_name} -> {aid}")
        return aid

    payload = {
        "application": {
            "tenantId": tenant_id,
            "name": app_name,
            "description": "Provisioned by workflow",
        }
    }
    created = client.post_json("/api/applications", payload)
    aid = created.get("id") or (created.get("application", {}) if isinstance(created, dict) else {}).get("id")
    if not aid:
        fatal(f"application create returned no id: {created}")
    log(f"[workflow] application created: {app_name} -> {aid}")
    return aid

def list_device_profiles(client: ApiClient, tenant_id: str, limit: int = 50) -> list:
    data = client.get_json("/api/device-profiles", params={"limit": limit, "offset": 0, "tenantId": tenant_id})
    return data.get("result", []) if isinstance(data, dict) else []

def ensure_device_profile(client: ApiClient, tenant_id: str, name: str = "demo-device-profile", region_id: str = DEFAULT_REGION_ID) -> str:
    profiles = list_device_profiles(client, tenant_id)
    found = find_by_name(profiles, name)
    if found and "id" in found:
        pid = found["id"]
        log(f"[workflow] device-profile exists: {name} -> {pid}")
        return pid

    # URGENTE: Ajustar los parámetros LoRaWAN (region, macVersion, regParamsRevision, etc)
        "deviceProfile": {
            "tenantId": tenant_id,
            "name": name,
            "region": region_id,
            # VERIFICAR ESTOS DATOS
            # "macVersion": "LORAWAN_1_0_3",
            # "regParamsRevision": "RP002_1_0_3",
            # "supportsOtaa": True,
            # "supportsClassB": False,
            # "supportsClassC": False,
        }
    created = client.post_json("/api/device-profiles")
    pid = created.get("id") or (created.get("deviceProfile", {}) if isinstance(created, dict) else {}).get("id")
    if not pid:
        fatal(f"device-profile create returned no id: {created}")
    log(f"[workflow] device-profile created: {name} -> {pid}")
    return pid

def list_gateways(client: ApiClient, tenant_id: str, limit: int = 50) -> list:
    data = client.get_json("/api/gateways", params={"limit": limit, "offset": 0, "tenantId": tenant_id})
    return data.get("result", []) if isinstance(data, dict) else []

def ensure_gateway(client: ApiClient, tenant_id: str, gateway_name: str = "demo-gateway", gateway_id: str = "") -> str:
    gws = list_gateways(client, tenant_id)
    found = find_by_name(gws, gateway_name)
    if found and "gatewayId" in found:
        gid = found["gatewayId"]
        log(f"[workflow] gateway exists: {gateway_name} -> {gid}")
        return gid

    if not gateway_id:
        gateway_id = "0102030405060708"

    payload = {
        "gateway": {
            "tenantId": tenant_id,
            "gatewayId": gateway_id,
            "name": gateway_name,
            "description": "Provisioned by workflow",
        }
    }
    _ = client.post_json("/api/gateways", payload)
    log(f"[workflow] gateway created: {gateway_name} -> {gateway_id}")
    return gateway_id

def list_devices(client: ApiClient, application_id: str, limit: int = 50) -> list:
    data = client.get_json("/api/devices", params={"limit": limit, "offset": 0, "applicationId": application_id})
    return data.get("result", []) if isinstance(data, dict) else []

def ensure_device(client: ApiClient, application_id: str, device_profile_id: str, dev_eui: str, name: str) -> str:
    devices = list_devices(client, application_id)
    for d in devices:
        if d.get("devEui") == dev_eui or d.get("name") == name:
            log(f"[workflow] device exists: {name} -> {d.get('devEui')}")
            return d.get("devEui", dev_eui)

    payload = {
        "device": {
            "applicationId": application_id,
            "deviceProfileId": device_profile_id,
            "name": name,
            "devEui": dev_eui,
            "description": "Provisioned by workflow",
            # Optional:
            # "isDisabled": False
        }
    }
    _ = client.post_json("/api/devices", payload)
    log(f"[workflow] device created: {name} -> {dev_eui}")
    return dev_eui

def cmd_doctor(args: argparse.Namespace) -> None:
    if looks_like_placeholder(args.api_key):
        fatal("CHIRPSTACK_API_KEY is placeholder. Set env CHIRPSTACK_API_KEY.")

    base = detect_base_url(args.api_key, prefer=args.base_url)
    log(f"[workflow] using base_url: {base}")

    client = ApiClient(base_url=base, api_key=args.api_key)

    try:
        data = client.get_json("/api/tenants", params={"limit": 1, "offset": 0})
        log("[workflow] REST OK: /api/tenants reachable")
        log(f"[workflow] sample response keys: {list(data.keys()) if isinstance(data, dict) else type(data)}")
    except Exception as e:
        fatal(f"REST doctor failed: {e}")

    if args.sim_toml:
        if os.path.exists(args.sim_toml):
            log(f"[workflow] simulator TOML found: {args.sim_toml}")
        else:
            fatal(f"simulator TOML not found: {args.sim_toml}")

def cmd_simulator(args: argparse.Namespace) -> None:
    if looks_like_placeholder(args.api_key):
        fatal("CHIRPSTACK_API_KEY is placeholder. Set env CHIRPSTACK_API_KEY.")
    if looks_like_placeholder(args.tenant_id) or not is_uuidish(args.tenant_id):
        fatal("CHIRPSTACK_TENANT_ID missing/invalid. Set env CHIRPSTACK_TENANT_ID (UUID).")

    ensure_simulator_toml(args.sim_toml, args.api_key, args.tenant_id)
    log("[workflow] OK. Now run:")
    log("  docker compose --profile sim up -d chirpstack-simulator")

def cmd_provision(args: argparse.Namespace) -> None:
    if looks_like_placeholder(args.api_key):
        fatal("CHIRPSTACK_API_KEY is placeholder. Set env CHIRPSTACK_API_KEY.")

    base = detect_base_url(args.api_key, prefer=args.base_url)
    client = ApiClient(base_url=base, api_key=args.api_key)

    tenant_id = ensure_tenant(client, args.tenant_id, tenant_name=args.tenant_name)
    app_id = ensure_application(client, tenant_id, app_name=args.app_name)
    profile_id = ensure_device_profile(client, tenant_id, name=args.device_profile_name, region_id=args.region_id)
    _gateway_id = ensure_gateway(client, tenant_id, gateway_name=args.gateway_name, gateway_id=args.gateway_id)

    if args.devices:
        for idx, dev_eui in enumerate(args.devices, start=1):
            dev_eui = dev_eui.strip()
            if not re.match(r"^[0-9A-Fa-f]{16}$", dev_eui):
                fatal(f"Invalid DevEUI '{dev_eui}' (expected 16 hex chars)")
            ensure_device(client, app_id, profile_id, dev_eui, name=f"demo-device-{idx}")
    else:
        log("[workflow] No --devices provided. Skipping device creation.")
        log("[workflow] TIP: pass --devices 0102030405060701 0102030405060702 ...")

    log("[workflow] Provision complete.")
    log(f"[workflow] tenant_id={tenant_id}")
    log(f"[workflow] application_id={app_id}")
    log(f"[workflow] device_profile_id={profile_id}")

def cmd_run(args: argparse.Namespace) -> None:
    cmd_doctor(args)
    cmd_provision(args)
    if args.patch_sim:
        cmd_simulator(args)

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="devices_workflow.py",
        description="ChirpStack provisioning + simulator workflow (Prueba-Modbus-TCP)",
    )
    p.add_argument("--base-url", default=ENV_BASE_URL or "", help="ChirpStack REST base URL (optional).")
    p.add_argument("--api-key", default=ENV_API_KEY, help="ChirpStack API key (Bearer).")
    p.add_argument("--tenant-id", default=ENV_TENANT_ID, help="Tenant UUID (optional; will create/find if absent).")
    p.add_argument("--sim-toml", default=SIM_TOML_PATH, help="Path to chirpstack-simulator.toml")

    sub = p.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("doctor", help="Validate API key + detect REST endpoint.")
    sp.set_defaults(func=cmd_doctor)

    sp = sub.add_parser("simulator", help="Patch chirpstack-simulator.toml placeholders.")
    sp.set_defaults(func=cmd_simulator)

    sp = sub.add_parser("provision", help="Idempotently provision tenant/app/profile/gateway/devices.")
    sp.add_argument("--tenant-name", default="demo-tenant")
    sp.add_argument("--app-name", default="demo-app")
    sp.add_argument("--device-profile-name", default="demo-device-profile")
    sp.add_argument("--region-id", default=DEFAULT_REGION_ID)
    sp.add_argument("--gateway-name", default="demo-gateway")
    sp.add_argument("--gateway-id", default="", help="Gateway ID (EUI64 hex). If empty uses demo placeholder.")
    sp.add_argument("--devices", nargs="*", default=[], help="List of DevEUIs (16 hex chars).")
    sp.set_defaults(func=cmd_provision)

    sp = sub.add_parser("run", help="doctor + provision + (optional) simulator patch")
    sp.add_argument("--tenant-name", default="demo-tenant")
    sp.add_argument("--app-name", default="demo-app")
    sp.add_argument("--device-profile-name", default="demo-device-profile")
    sp.add_argument("--region-id", default=DEFAULT_REGION_ID)
    sp.add_argument("--gateway-name", default="demo-gateway")
    sp.add_argument("--gateway-id", default="")
    sp.add_argument("--devices", nargs="*", default=[], help="List of DevEUIs (16 hex chars).")
    sp.add_argument("--patch-sim", action="store_true", help="Also patch chirpstack-simulator.toml")
    sp.set_defaults(func=cmd_run)

    return p

def main() -> None:
    parser = build_parser()
    args = parser.parse_args()
    args.func(args)

if __name__ == "__main__":
    main()

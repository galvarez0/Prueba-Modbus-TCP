import os
import json
import requests

CHIRPSTACK_BASE_URL = os.getenv("CHIRPSTACK_BASE_URL", "http://chirpstack:8080")
API_KEY = os.getenv("CHIRPSTACK_API_KEY", "PUT_YOUR_API_KEY_HERE")        # URGENTE: REEMPLAZAR
TENANT_ID = os.getenv("CHIRPSTACK_TENANT_ID", "PUT_YOUR_TENANT_ID_HERE")  # URGENTE: REEMPLAZAR

SIM_TOML_PATH = os.getenv("SIM_TOML_PATH", "../simulator/chirpstack-simulator.toml")

def headers():
    return {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}

def ensure_simulator_toml():
    with open(SIM_TOML_PATH, "r", encoding="utf-8") as f:
        txt = f.read()
    txt = txt.replace("PUT_YOUR_API_KEY_HERE", API_KEY).replace("PUT_YOUR_TENANT_ID_HERE", TENANT_ID)
    with open(SIM_TOML_PATH, "w", encoding="utf-8") as f:
        f.write(txt)
    print(f"[workflow] updated simulator config: {SIM_TOML_PATH}")

def api_post(path: str, payload: dict):
    url = CHIRPSTACK_BASE_URL.rstrip("/") + path
    r = requests.post(url, headers=headers(), data=json.dumps(payload), timeout=20)
    if not r.ok:
        raise RuntimeError(f"POST {url} -> {r.status_code}: {r.text[:500]}")
    return r.json() if r.text else {}

def main():
    ensure_simulator_toml()

    # Esqueleto provisional
    # app = api_post("/api/applications", {"tenantId": TENANT_ID, "name": "demo-app", "description": "demo"})
    # print(app)

    print("[workflow] Provisioning skeleton ready. (See URGENTE comments)")
    print("[workflow] To run simulator: docker compose --profile sim up -d chirpstack-simulator")

if __name__ == "__main__":
    main()

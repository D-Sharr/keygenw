from http.server import BaseHTTPRequestHandler
import json
import base64
import datetime
import urllib.request
import urllib.error
import concurrent.futures
from cryptography.hazmat.primitives.asymmetric import x25519
from cryptography.hazmat.primitives import serialization

# ==============================================================================
# 1. SETTINGS
# ==============================================================================
# Ideally, move these to Vercel Environment Variables for security
ADMIN_PASSWORD = "adminSharr@26"
BASE_URL = "https://h2api.arakan.info"
GENERATE_COUNT = 5         
ACCOUNT_TYPE = "free"
MAX_WORKERS = 10           

# ==============================================================================
# CORE FUNCTIONS
# ==============================================================================

def generate_and_register(_):
    """Generates keys and registers with Cloudflare."""
    try:
        # --- A. Generate Keys ---
        priv_key_obj = x25519.X25519PrivateKey.generate()
        pub_key_obj = priv_key_obj.public_key()

        priv_b64 = base64.b64encode(priv_key_obj.private_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption()
        )).decode('utf-8')

        pub_b64 = base64.b64encode(pub_key_obj.public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw
        )).decode('utf-8')

        # --- B. Register with Cloudflare ---
        timestamp = datetime.datetime.utcnow().isoformat()[:-3] + "+00:00"

        payload = {
            "key": pub_b64,
            "install_id": "",
            "fcm_token": "",
            "tos": timestamp,
            "model": "Android",
            "type": "Android",
            "locale": "en_US"
        }

        headers = {
            "User-Agent": "okhttp/3.12.1",
            "Content-Type": "application/json; charset=UTF-8",
        }

        req = urllib.request.Request(
            "https://api.cloudflareclient.com/v0a2404/reg",
            data=json.dumps(payload).encode('utf-8'),
            headers=headers,
            method="POST"
        )

        with urllib.request.urlopen(req, timeout=8) as response:
            if response.status == 200:
                json_resp = json.loads(response.read().decode('utf-8'))
                v6 = json_resp['config']['interface']['addresses']['v6']
                return {
                    "password": priv_b64,
                    "ip": v6,
                    "server": "162.159.192.1"
                }

    except Exception as e:
        print(f"Error in worker: {e}")
        return None

def upload_batch(configs):
    """Uploads the batch to your server."""
    if not configs:
        return {"status": "error", "message": "No configs to upload"}

    url = f"{BASE_URL}/admin/api/configs"
    headers = {
        "Content-Type": "application/json",
        "x-auth-key": ADMIN_PASSWORD,
        "User-Agent": "Python-Urllib/VercelBot"
    }

    payload = {
        "configs": configs,
        "type": ACCOUNT_TYPE
    }

    try:
        req = urllib.request.Request(
            url,
            data=json.dumps(payload).encode('utf-8'),
            headers=headers,
            method="POST"
        )
        with urllib.request.urlopen(req) as response:
            return {
                "status": "success", 
                "server_code": response.status, 
                "count": len(configs),
                "response": response.read().decode('utf-8')
            }

    except urllib.error.HTTPError as e:
        return {"status": "failed", "code": e.code, "reason": e.reason}
    except Exception as e:
        return {"status": "error", "message": str(e)}

# ==============================================================================
# VERCEL HANDLER
# ==============================================================================

class handler(BaseHTTPRequestHandler):
    def do_GET(self):
        # 1. Run Generation Logic
        valid_configs = []
        
        # We use ThreadPoolExecutor to run this concurrently
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as executor:
            futures = [executor.submit(generate_and_register, i) for i in range(GENERATE_COUNT)]
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                if result:
                    valid_configs.append(result)

        # 2. Upload Logic
        upload_result = {}
        if valid_configs:
            upload_result = upload_batch(valid_configs)
        else:
            upload_result = {"status": "failed", "message": "No valid keys generated"}

        # 3. Send Response back to the caller
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        
        response_data = {
            "generated_count": len(valid_configs),
            "upload_result": upload_result
        }
        
        self.wfile.write(json.dumps(response_data).encode('utf-8'))
        return
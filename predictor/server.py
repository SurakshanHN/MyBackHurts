#!/usr/bin/env python3
"""
Stand-in Prediction Service (Part 6)
Implements draft-1 /predict HTTP contract using a scheduled demo surge profile.
"""

from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os
import time
import urllib.parse

class ScheduleModel:
    def __init__(self):
        self.base_rps = float(os.getenv("BASE_RPS", "60.0"))
        self.peak_rps = float(os.getenv("PEAK_RPS", "490.0"))
        env_epoch = os.getenv("SURGE_START_EPOCH")
        self.surge_start_epoch = float(env_epoch) if env_epoch else None

    def predict(self, target_time: float) -> float:
        """
        Calculates scheduled RPS at target_time based on surge profile:
        - 60 rps base
        - surge starts at SURGE_START (surge_start_epoch)
        - ramps 10s to PEAK_RPS (490)
        - 150s peak (490)
        - 10s down to base (60)
        - base after
        """
        if self.surge_start_epoch is None:
            return self.base_rps

        t = target_time
        s = self.surge_start_epoch

        if t < s:
            return self.base_rps
        elif s <= t <= s + 10.0:
            # 10s ramp up
            ratio = (t - s) / 10.0
            return self.base_rps + (self.peak_rps - self.base_rps) * ratio
        elif s + 10.0 < t <= s + 160.0:
            # 150s peak
            return self.peak_rps
        elif s + 160.0 < t <= s + 170.0:
            # 10s ramp down
            ratio = (t - (s + 160.0)) / 10.0
            return self.peak_rps - (self.peak_rps - self.base_rps) * ratio
        else:
            return self.base_rps

def load_model():
    """
    Isolated model loader. Currently returns the stand-in ScheduleModel.
    NOTE: When the teammate's trained model is delivered, load their .pkl artifact here:
        # import pickle
        # with open("model.pkl", "rb") as f:
        #     return pickle.load(f)
    """
    return ScheduleModel()

# Global model instance and failure mode configuration
model = load_model()
fail_mode = os.getenv("FAIL_MODE", "").lower()

class PredictorHandler(BaseHTTPRequestHandler):
    def _send_json(self, status: int, data: dict):
        body = json.dumps(data).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_text(self, status: int, text: str, content_type: str = "text/plain"):
        body = text.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _handle_fail_mode(self) -> bool:
        """Handles FAIL_MODE simulation. Returns True if response was sent."""
        global fail_mode
        if fail_mode == "error":
            self._send_json(500, {"error": "simulated failure mode 'error'"})
            return True
        elif fail_mode == "slow":
            time.sleep(10)
            return False  # Continue after sleeping 10s
        elif fail_mode == "garbage":
            self._send_text(200, "GARBAGE_NON_JSON_BODY_<<<>>>", content_type="text/plain")
            return True
        return False

    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        if url.path == "/healthz":
            self._send_text(200, "ok\n")
            return

        if url.path in ("/predict", "/predict/predict"):
            if self._handle_fail_mode():
                return

            params = urllib.parse.parse_qs(url.query)
            horizon = 90
            if "horizon" in params:
                try:
                    horizon = int(float(params["horizon"][0]))
                except ValueError:
                    horizon = 90

            now = time.time()
            target_time = now + horizon
            predicted_rps = model.predict(target_time)

            resp = {
                "predicted_rps": round(predicted_rps, 2),
                "horizon_seconds": horizon,
                "generated_at": now,
                "model": "standin-schedule",
                "contract": "draft-1"
            }
            self._send_json(200, resp)
            return

        self._send_json(404, {"error": "not found"})

    def do_POST(self):
        global fail_mode
        url = urllib.parse.urlparse(self.path)

        length = int(self.headers.get("Content-Length", 0))
        body_bytes = self.rfile.read(length) if length > 0 else b"{}"
        try:
            payload = json.loads(body_bytes.decode("utf-8")) if body_bytes else {}
        except Exception:
            payload = {}

        if url.path in ("/predict", "/predict/predict"):
            if self._handle_fail_mode():
                return

            # Read and ignore window telemetry; a real model would use it for autoregressive inference
            _ = payload.get("window", [])

            horizon_val = payload.get("horizon")
            if horizon_val is not None:
                try:
                    horizon = int(float(horizon_val))
                except ValueError:
                    horizon = int(os.getenv("DEFAULT_HORIZON", "90"))
            else:
                horizon = int(os.getenv("DEFAULT_HORIZON", "90"))

            now = time.time()
            target_time = now + horizon
            predicted_rps = model.predict(target_time)

            resp = {
                "predicted_rps": round(predicted_rps, 2),
                "horizon_seconds": horizon,
                "generated_at": now,
                "model": "standin-schedule",
                "contract": "draft-1"
            }
            self._send_json(200, resp)
            return

        if url.path == "/schedule":
            if "surge_start_epoch" in payload:
                model.surge_start_epoch = float(payload["surge_start_epoch"])
            self._send_json(200, {
                "status": "ok",
                "surge_start_epoch": model.surge_start_epoch,
                "base_rps": model.base_rps,
                "peak_rps": model.peak_rps
            })
            return

        if url.path == "/fail":
            fail_mode = payload.get("mode", "").lower()
            self._send_json(200, {
                "status": "ok",
                "fail_mode": fail_mode
            })
            return

        self._send_json(404, {"error": "not found"})

    def log_message(self, format, *args):
        # Concise logging to stdout
        print(f"[{self.log_date_time_string()}] {self.address_string()} - {format % args}")

def main():
    port = int(os.getenv("PORT", "8000"))
    server = HTTPServer(("0.0.0.0", port), PredictorHandler)
    print(f"Stand-in Predictor service running on port {port}...")
    server.serve_forever()

if __name__ == "__main__":
    main()

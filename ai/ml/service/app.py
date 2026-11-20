"""FastAPI ML service: failure-risk inference and short-term load forecasting."""

import json
import os

import numpy as np
import xgboost as xgb
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from features import FEATURES, HORIZON_MINUTES, MODEL_VERSION

app = FastAPI(title="ml-service")
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["GET", "POST", "OPTIONS"],
    allow_headers=["Content-Type", "X-Correlation-ID"],
)

MODEL_DIR = os.environ.get("MODEL_DIR", "/app/models")

clf = None
reg5 = None
reg15 = None
metrics = {}
load_error = None


def _load_models():
    global clf, reg5, reg15, metrics, load_error
    try:
        clf = xgb.XGBClassifier()
        clf.load_model(os.path.join(MODEL_DIR, "risk_model.json"))
        reg5 = xgb.XGBRegressor()
        reg5.load_model(os.path.join(MODEL_DIR, "forecast_t5.json"))
        reg15 = xgb.XGBRegressor()
        reg15.load_model(os.path.join(MODEL_DIR, "forecast_t15.json"))
        metrics_path = os.path.join(MODEL_DIR, "metrics.json")
        if os.path.exists(metrics_path):
            with open(metrics_path) as f:
                metrics = json.load(f)
    except Exception as exc:  # noqa: BLE001 - surface the reason in /healthz
        load_error = str(exc)
        clf = reg5 = reg15 = None


_load_models()


def _vector(features: dict) -> np.ndarray:
    return np.array([[float(features.get(name, 0.0)) for name in FEATURES]], dtype=np.float32)


class RiskRequest(BaseModel):
    service: str
    features: dict


class ForecastRequest(BaseModel):
    service: str
    window: list[dict]


@app.get("/healthz")
def healthz():
    return {
        "status": "ok",
        "model_loaded": clf is not None,
        "model_version": MODEL_VERSION,
        "load_error": load_error,
    }


@app.get("/model")
def model_info():
    return {"model_version": MODEL_VERSION, "loaded": clf is not None, "metrics": metrics}


@app.post("/risk")
def risk(req: RiskRequest):
    if clf is None:
        return JSONResponse({"error": "model not loaded", "detail": load_error}, status_code=503)
    x = _vector(req.features)
    prob = float(clf.predict_proba(x)[0, 1])
    dm = xgb.DMatrix(x, feature_names=FEATURES)
    contribs = clf.get_booster().predict(dm, pred_contribs=True)[0]
    pairs = list(zip(FEATURES, contribs[:-1]))
    pairs.sort(key=lambda kv: abs(kv[1]), reverse=True)
    top = [{"name": name, "contribution": float(value)} for name, value in pairs[:5]]
    positive = [name for name, value in pairs if value > 0]
    level = "low" if prob < 0.3 else "medium" if prob < 0.6 else "high"
    return {
        "failure_probability": prob,
        "risk_level": level,
        "horizon_minutes": HORIZON_MINUTES,
        "predicted_cause": positive[0] if level != "low" and positive else None,
        "top_features": top,
        "model_version": MODEL_VERSION,
    }


@app.post("/forecast")
def forecast(req: ForecastRequest):
    if reg5 is None or reg15 is None:
        return JSONResponse({"error": "model not loaded", "detail": load_error}, status_code=503)
    if not req.window:
        return JSONResponse({"error": "window must not be empty"}, status_code=400)
    last = req.window[-1]
    x = _vector(last)
    return {
        "requests_5m": float(reg5.predict(x)[0]),
        "requests_15m": float(reg15.predict(x)[0]),
        "cpu_pct_5m": None,
        "db_conn_pct_5m": float(last.get("db_conn_pct", 0.0)),
        "model_version": MODEL_VERSION,
    }

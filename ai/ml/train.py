"""Train the risk classifier and load forecasters, evaluate honestly, save artifacts.

Usage:
  python train.py
Artifacts are written to $MODEL_DIR (default: ai/ml/models).
"""

import json
import os

import numpy as np
from sklearn.metrics import (
    confusion_matrix,
    mean_absolute_error,
    precision_recall_fscore_support,
    roc_auc_score,
)
from xgboost import XGBClassifier, XGBRegressor

from dataset import build_dataset
from features import FEATURES, HORIZON_MINUTES, MODEL_VERSION, TARGET

MODELS_DIR = os.environ.get("MODEL_DIR", os.path.join(os.path.dirname(os.path.abspath(__file__)), "models"))
os.makedirs(MODELS_DIR, exist_ok=True)

RISK_THRESHOLD = 0.5
TRIVIAL_DB_CONN_THRESHOLD = 0.55


def classification_metrics(y_true, y_pred, y_prob):
    precision, recall, f1, _ = precision_recall_fscore_support(
        y_true, y_pred, average="binary", zero_division=0
    )
    auc = roc_auc_score(y_true, y_prob) if len(np.unique(y_true)) > 1 else float("nan")
    tn, fp, fn, tp = confusion_matrix(y_true, y_pred, labels=[0, 1]).ravel()
    false_alarm = fp / (fp + tn) if (fp + tn) else 0.0
    return {
        "precision": float(precision),
        "recall": float(recall),
        "f1": float(f1),
        "roc_auc": float(auc),
        "false_alarm_rate": float(false_alarm),
        "tp": int(tp),
        "fp": int(fp),
        "tn": int(tn),
        "fn": int(fn),
    }


def main():
    df = build_dataset().sort_values("ts").reset_index(drop=True)
    split = int(len(df) * 0.7)
    train = df.iloc[:split]
    test = df.iloc[split:]

    X_train, y_train = train[FEATURES], train[TARGET]
    X_test, y_test = test[FEATURES], test[TARGET]

    clf = XGBClassifier(
        n_estimators=200,
        max_depth=5,
        learning_rate=0.1,
        subsample=0.9,
        colsample_bytree=0.9,
        eval_metric="logloss",
        random_state=42,
        n_jobs=2,
    )
    clf.fit(X_train, y_train)

    y_prob = clf.predict_proba(X_test)[:, 1]
    y_pred = (y_prob >= RISK_THRESHOLD).astype(int)
    model_metrics = classification_metrics(y_test, y_pred, y_prob)

    # Trivial threshold baseline: the classic "scale on a single saturated signal" rule.
    base_pred = (X_test["db_conn_pct"] >= TRIVIAL_DB_CONN_THRESHOLD).astype(int)
    base_metrics = classification_metrics(y_test, base_pred, base_pred.astype(float))

    # Mean prediction lead time: how far before the incident the model fires.
    lead_times = test.loc[y_pred == 1, "minutes_to_failure"].dropna()
    lead_time = float(lead_times.mean()) if len(lead_times) else float("nan")

    reg5 = XGBRegressor(n_estimators=150, max_depth=4, learning_rate=0.1, random_state=42, n_jobs=2)
    reg15 = XGBRegressor(n_estimators=150, max_depth=4, learning_rate=0.1, random_state=42, n_jobs=2)
    reg5.fit(X_train, train["requests_t5"])
    reg15.fit(X_train, train["requests_t15"])
    mae5 = float(mean_absolute_error(test["requests_t5"], reg5.predict(X_test)))
    mae15 = float(mean_absolute_error(test["requests_t15"], reg15.predict(X_test)))

    clf.get_booster().save_model(os.path.join(MODELS_DIR, "risk_model.json"))
    reg5.get_booster().save_model(os.path.join(MODELS_DIR, "forecast_t5.json"))
    reg15.get_booster().save_model(os.path.join(MODELS_DIR, "forecast_t15.json"))
    with open(os.path.join(MODELS_DIR, "feature_names.json"), "w") as f:
        json.dump(FEATURES, f, indent=2)
    with open(os.path.join(MODELS_DIR, "model_version.txt"), "w") as f:
        f.write(MODEL_VERSION + "\n")

    metrics = {
        "model_version": MODEL_VERSION,
        "horizon_minutes": HORIZON_MINUTES,
        "risk_threshold": RISK_THRESHOLD,
        "n_train": int(len(train)),
        "n_test": int(len(test)),
        "positive_rate_test": float(y_test.mean()),
        "classifier": model_metrics,
        "trivial_baseline": {
            "rule": f"db_conn_pct >= {TRIVIAL_DB_CONN_THRESHOLD}",
            **base_metrics,
        },
        "mean_prediction_lead_time_min": lead_time,
        "forecast": {"t5_mae": mae5, "t15_mae": mae15},
    }
    with open(os.path.join(MODELS_DIR, "metrics.json"), "w") as f:
        json.dump(metrics, f, indent=2)

    print(json.dumps(metrics, indent=2))


if __name__ == "__main__":
    main()

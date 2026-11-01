-- Initial migration: run once against the selected database/schema.
CREATE TABLE daily_health (
    day date PRIMARY KEY,
    steps integer CHECK (steps >= 0),
    distance_m numeric CHECK (distance_m >= 0),
    calories_total integer CHECK (calories_total >= 0),
    calories_active integer CHECK (calories_active >= 0),
    calories_resting integer CHECK (calories_resting >= 0),
    heart_rate_resting smallint CHECK (heart_rate_resting >= 0),
    heart_rate_min smallint CHECK (heart_rate_min >= 0),
    heart_rate_max smallint CHECK (heart_rate_max >= 0),
    moderate_minutes smallint CHECK (moderate_minutes >= 0),
    vigorous_minutes smallint CHECK (vigorous_minutes >= 0),
    stress_avg smallint CHECK (stress_avg BETWEEN 0 AND 100),
    stress_max smallint CHECK (stress_max BETWEEN 0 AND 100),
    body_battery_min smallint CHECK (body_battery_min BETWEEN 0 AND 100),
    body_battery_max smallint CHECK (body_battery_max BETWEEN 0 AND 100),
    body_battery_latest smallint CHECK (body_battery_latest BETWEEN 0 AND 100),
    body_battery_charged smallint CHECK (body_battery_charged >= 0),
    body_battery_drained smallint CHECK (body_battery_drained >= 0)
);

CREATE TABLE daily_sleep (
    day date PRIMARY KEY REFERENCES daily_health(day),
    started_at timestamptz,
    ended_at timestamptz,
    duration_s integer CHECK (duration_s >= 0),
    nap_s integer CHECK (nap_s >= 0),
    deep_s integer CHECK (deep_s >= 0),
    light_s integer CHECK (light_s >= 0),
    rem_s integer CHECK (rem_s >= 0),
    awake_s integer CHECK (awake_s >= 0),
    unmeasurable_s integer CHECK (unmeasurable_s >= 0),
    score smallint CHECK (score BETWEEN 0 AND 100),
    heart_rate_avg numeric CHECK (heart_rate_avg >= 0),
    stress_avg numeric CHECK (stress_avg BETWEEN 0 AND 100),
    respiration_avg numeric CHECK (respiration_avg >= 0),
    spo2_avg numeric CHECK (spo2_avg BETWEEN 0 AND 100),
    CHECK (ended_at >= started_at)
);

CREATE TABLE daily_hrv (
    day date PRIMARY KEY REFERENCES daily_health(day),
    night_avg integer CHECK (night_avg >= 0),
    weekly_avg integer CHECK (weekly_avg >= 0),
    status text,
    baseline_low integer CHECK (baseline_low >= 0),
    baseline_high integer CHECK (baseline_high >= 0),
    baseline_low_upper integer CHECK (baseline_low_upper >= 0)
);

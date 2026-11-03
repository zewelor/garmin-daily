ALTER TABLE daily_health
    ADD COLUMN respiration_avg smallint CHECK (respiration_avg >= 0),
    ADD COLUMN respiration_min smallint CHECK (respiration_min >= 0),
    ADD COLUMN respiration_max smallint CHECK (respiration_max >= 0),
    ADD COLUMN respiration_latest smallint CHECK (respiration_latest >= 0),
    ADD COLUMN stress_rest_ms integer CHECK (stress_rest_ms >= 0),
    ADD COLUMN stress_activity_ms integer CHECK (stress_activity_ms >= 0),
    ADD COLUMN stress_uncategorized_ms integer CHECK (stress_uncategorized_ms >= 0),
    ADD COLUMN stress_low_ms integer CHECK (stress_low_ms >= 0),
    ADD COLUMN stress_medium_ms integer CHECK (stress_medium_ms >= 0),
    ADD COLUMN stress_high_ms integer CHECK (stress_high_ms >= 0);

ALTER TABLE activities
    ADD COLUMN heart_rate_zone_1_s numeric CHECK (heart_rate_zone_1_s >= 0),
    ADD COLUMN heart_rate_zone_2_s numeric CHECK (heart_rate_zone_2_s >= 0),
    ADD COLUMN heart_rate_zone_3_s numeric CHECK (heart_rate_zone_3_s >= 0),
    ADD COLUMN heart_rate_zone_4_s numeric CHECK (heart_rate_zone_4_s >= 0),
    ADD COLUMN heart_rate_zone_5_s numeric CHECK (heart_rate_zone_5_s >= 0);

CREATE TABLE weight_measurements (
    measured_at timestamptz PRIMARY KEY,
    day date NOT NULL,
    weight_kg numeric NOT NULL CHECK (weight_kg >= 0)
);

CREATE TABLE activities (
    activity_id bigint PRIMARY KEY CHECK (activity_id > 0),
    day date NOT NULL,
    started_at timestamptz NOT NULL,
    name text,
    sport text NOT NULL CHECK (length(btrim(sport)) > 0),
    duration_s numeric CHECK (duration_s IS NULL OR duration_s >= 0),
    distance_m numeric CHECK (distance_m IS NULL OR distance_m >= 0),
    average_speed_mps numeric CHECK (average_speed_mps IS NULL OR average_speed_mps >= 0),
    calories numeric CHECK (calories IS NULL OR calories >= 0),
    heart_rate_avg numeric CHECK (heart_rate_avg IS NULL OR heart_rate_avg >= 0),
    heart_rate_max numeric CHECK (heart_rate_max IS NULL OR heart_rate_max >= 0),
    training_effect_aerobic numeric CHECK (training_effect_aerobic IS NULL OR training_effect_aerobic >= 0),
    training_effect_anaerobic numeric CHECK (training_effect_anaerobic IS NULL OR training_effect_anaerobic >= 0),
    training_load numeric CHECK (training_load IS NULL OR training_load >= 0),
    sets integer CHECK (sets IS NULL OR sets >= 0),
    reps integer CHECK (reps IS NULL OR reps >= 0),
    depth_max_cm numeric CHECK (depth_max_cm IS NULL OR depth_max_cm >= 0),
    body_battery_change smallint CHECK (body_battery_change IS NULL OR body_battery_change BETWEEN -100 AND 100)
);

CREATE TABLE activity_exercises (
    activity_id bigint NOT NULL REFERENCES activities(activity_id) ON DELETE CASCADE,
    exercise_index integer NOT NULL CHECK (exercise_index >= 0),
    category text,
    subcategory text,
    sets integer CHECK (sets IS NULL OR sets >= 0),
    reps integer CHECK (reps IS NULL OR reps >= 0),
    duration_ms numeric CHECK (duration_ms IS NULL OR duration_ms >= 0),
    max_weight_g numeric CHECK (max_weight_g IS NULL OR max_weight_g >= 0),
    volume_g numeric CHECK (volume_g IS NULL OR volume_g >= 0),
    PRIMARY KEY (activity_id, exercise_index)
);

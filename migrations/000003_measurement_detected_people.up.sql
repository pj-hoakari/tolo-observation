ALTER TABLE measurements ADD COLUMN mean_detected_people DOUBLE PRECISION CHECK (mean_detected_people >= 0);

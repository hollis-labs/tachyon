-- Ceremony meter (CW-20261114-0023f): one row per day with that day's rolling 7-day figures, so the meter can
-- compare this week with last week (local events are kept 7 days). Additive.
CREATE TABLE ceremony_daily (
 day TEXT PRIMARY KEY,          -- YYYY-MM-DD (UTC)
 activity INTEGER NOT NULL,     -- agent events collected in the 7 days to that day
 done INTEGER NOT NULL,         -- Torque tasks closed done in those 7 days
 gates INTEGER NOT NULL,        -- gates raised in those 7 days
 computed_ns INTEGER NOT NULL
);

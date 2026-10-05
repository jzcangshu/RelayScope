-- Historical payments have no intervals and cannot be refunded automatically.
CREATE TABLE membership_order_intervals (
 order_id INTEGER PRIMARY KEY REFERENCES ldc_orders(id),
 user_id INTEGER NOT NULL REFERENCES users(id),
 starts_at INTEGER NOT NULL,
 ends_at INTEGER NOT NULL
);
CREATE INDEX membership_order_intervals_user_idx ON membership_order_intervals(user_id, starts_at);
CREATE TABLE order_refund_attempts (
 order_id INTEGER PRIMARY KEY REFERENCES ldc_orders(id),
 status TEXT NOT NULL CHECK(status IN ('running','uncertain','succeeded','rejected')),
 updated_at INTEGER NOT NULL
);

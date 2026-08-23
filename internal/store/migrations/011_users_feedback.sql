CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    linuxdo_id TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE feedback (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX feedback_created_idx ON feedback(created_at DESC, id DESC);

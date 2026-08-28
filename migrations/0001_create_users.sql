CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    email         text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- The unique index ignores letter case. The user service also lowercases
-- emails on write.
CREATE UNIQUE INDEX users_email_idx ON users (lower(email));

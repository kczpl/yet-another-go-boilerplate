CREATE TABLE notes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    text       text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The index matches the list query: the notes of one user, newest first.
CREATE INDEX notes_user_id_idx ON notes (user_id, created_at DESC, id DESC);

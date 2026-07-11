-- +goose Up
CREATE TABLE notes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 255),
    content    text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notes_created_at_idx ON notes (created_at DESC, id DESC);

-- +goose Down
DROP TABLE notes;

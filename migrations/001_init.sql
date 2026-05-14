-- Run this SQL in your Neon dashboard (SQL Editor)

CREATE TABLE IF NOT EXISTS proposals (
    id          SERIAL PRIMARY KEY,
    user_id     BIGINT,                          -- NULL if anonymous
    username    TEXT,                             -- NULL if anonymous
    first_name  TEXT,                             -- NULL if anonymous
    type        TEXT NOT NULL                     -- 'game' | 'stream' | 'anon'
                    CHECK (type IN ('game', 'stream', 'anon')),
    content     TEXT NOT NULL
                    CHECK (char_length(content) BETWEEN 1 AND 4000),
    message_id  BIGINT,                          -- msg id in streamer chat
    status      TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'archived', 'top')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS votes (
    proposal_id INT NOT NULL REFERENCES proposals(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL,
    value       SMALLINT NOT NULL CHECK (value IN (1, -1)),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (proposal_id, user_id)
);

-- Indexes for common queries
CREATE INDEX IF NOT EXISTS idx_proposals_status    ON proposals(status);
CREATE INDEX IF NOT EXISTS idx_proposals_type      ON proposals(type);
CREATE INDEX IF NOT EXISTS idx_proposals_created   ON proposals(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_votes_proposal      ON votes(proposal_id);

-- View: proposals with vote totals (used for top/list queries)
CREATE OR REPLACE VIEW proposals_with_votes AS
    SELECT
        p.*,
        COALESCE(SUM(CASE WHEN v.value = 1  THEN 1 ELSE 0 END), 0) AS likes,
        COALESCE(SUM(CASE WHEN v.value = -1 THEN 1 ELSE 0 END), 0) AS dislikes
    FROM proposals p
    LEFT JOIN votes v ON v.proposal_id = p.id
    GROUP BY p.id;
